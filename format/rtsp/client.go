// Package rtsp
// Created by RTT.
// Author: teocci@yandex.com on 2021-Oct-27
package rtsp

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	//"log"
	"net"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/teocci/go-stream-av/av"
	"github.com/teocci/go-stream-av/av/avutil"
	"github.com/teocci/go-stream-av/codec"
	"github.com/teocci/go-stream-av/codec/aacparser"
	"github.com/teocci/go-stream-av/codec/h264parser"
	"github.com/teocci/go-stream-av/format/rtsp/sdp"
	"github.com/teocci/go-stream-av/utils/bits/pio"
)

var ErrCodecDataChange = fmt.Errorf("rtsp: codec data change, please call HandleCodecDataChange()")

var DebugRtp = false
var DebugRtsp = false
var SkipErrRtpBlock = false

const (
	stageOptionsDone = iota + 1
	stageDescribeDone
	stageSetupDone
	stageWaitCodecData
	stageCodecDataDone
)

type Client struct {
	DebugRtsp bool
	DebugRtp  bool
	Headers   []string

	SkipErrRtpBlock bool

	RtspTimeout          time.Duration
	RtpTimeout           time.Duration
	RtpKeepAliveTimeout  time.Duration
	rtpKeepaliveTimer    time.Time
	rtpKeepaliveEnterCnt int

	stage int

	setupIdx []int
	setupMap []int

	authHeaders func(method string) []string

	url         *url.URL
	conn       *connWithTimeout
	brConn     *bufio.Reader
	requestUri string
	cseq        uint
	streams     []*Stream
	streamsintf []av.CodecData
	session     string
	body        io.Reader
}

type Request struct {
	Header []string
	Uri    string
	Method string
}

type Response struct {
	StatusCode    int
	Headers       textproto.MIMEHeader
	ContentLength int
	Body          []byte

	Block []byte
}

func DialTimeout(uri string, timeout time.Duration) (self *Client, err error) {
	var URL *url.URL
	if URL, err = url.Parse(uri); err != nil {
		return
	}

	if _, _, err := net.SplitHostPort(URL.Host); err != nil {
		URL.Host = URL.Host + ":554"
	}

	dailer := net.Dialer{Timeout: timeout}
	var conn net.Conn
	if conn, err = dailer.Dial("tcp", URL.Host); err != nil {
		return
	}

	u2 := *URL
	u2.User = nil

	connt := &connWithTimeout{Conn: conn}

	self = &Client{
		conn:            connt,
		brConn:          bufio.NewReaderSize(connt, 256),
		url:             URL,
		requestUri:      u2.String(),
		DebugRtp:        DebugRtp,
		DebugRtsp:       DebugRtsp,
		SkipErrRtpBlock: SkipErrRtpBlock,
	}
	return
}

func Dial(uri string) (self *Client, err error) {
	return DialTimeout(uri, 0)
}

func (c *Client) allCodecDataReady() bool {
	for _, si := range c.setupIdx {
		stream := c.streams[si]
		if stream.CodecData == nil {
			return false
		}
	}
	return true
}

func (c *Client) probe() (err error) {
	for {
		if c.allCodecDataReady() {
			break
		}
		if _, err = c.readPacket(); err != nil {
			return
		}
	}
	c.stage = stageCodecDataDone
	return
}

func (c *Client) prepare(stage int) (err error) {
	for c.stage < stage {
		switch c.stage {
		case 0:
			if err = c.Options(); err != nil {
				return
			}
		case stageOptionsDone:
			if _, err = c.Describe(); err != nil {
				return
			}
		case stageDescribeDone:
			if err = c.SetupAll(); err != nil {
				return
			}

		case stageSetupDone:
			if err = c.Play(); err != nil {
				return
			}

		case stageWaitCodecData:
			if err = c.probe(); err != nil {
				return
			}
		}
	}
	return
}

func (c *Client) Streams() (streams []av.CodecData, err error) {
	if err = c.prepare(stageCodecDataDone); err != nil {
		return
	}
	for _, si := range c.setupIdx {
		stream := c.streams[si]
		streams = append(streams, stream.CodecData)
	}
	return
}

func (c *Client) SendRtpKeepalive() (err error) {
	if c.RtpKeepAliveTimeout > 0 {
		if c.rtpKeepaliveTimer.IsZero() {
			c.rtpKeepaliveTimer = time.Now()
		} else if time.Now().Sub(c.rtpKeepaliveTimer) > c.RtpKeepAliveTimeout {
			c.rtpKeepaliveTimer = time.Now()
			if c.DebugRtsp {
				fmt.Println("rtp: keep alive")
			}
			req := Request{
				Method: "OPTIONS",
				Uri:    c.requestUri,
			}
			if c.session != "" {
				req.Header = append(req.Header, "Session: "+c.session)
			}
			if err = c.WriteRequest(req); err != nil {
				return
			}
		}
	}
	return
}

func (c *Client) WriteRequest(req Request) (err error) {
	c.conn.Timeout = c.RtspTimeout
	c.cseq++

	buf := &bytes.Buffer{}

	fmt.Fprintf(buf, "%s %s RTSP/1.0\r\n", req.Method, req.Uri)
	fmt.Fprintf(buf, "CSeq: %d\r\n", c.cseq)

	if c.authHeaders != nil {
		headers := c.authHeaders(req.Method)
		for _, s := range headers {
			io.WriteString(buf, s)
			io.WriteString(buf, "\r\n")
		}
	}
	for _, s := range req.Header {
		io.WriteString(buf, s)
		io.WriteString(buf, "\r\n")
	}
	for _, s := range c.Headers {
		io.WriteString(buf, s)
		io.WriteString(buf, "\r\n")
	}
	io.WriteString(buf, "\r\n")

	bufout := buf.Bytes()

	if c.DebugRtsp {
		fmt.Print("> ", string(bufout))
	}

	if _, err = c.conn.Write(bufout); err != nil {
		return
	}

	return
}

func (c *Client) parseBlockHeader(h []byte) (length int, no int, valid bool) {
	length = int(h[2])<<8 + int(h[3])
	no = int(h[1])
	if no/2 >= len(c.streams) {
		return
	}

	if no%2 == 0 { // rtp
		if length < 8 {
			return
		}

		// V=2
		if h[4]&0xc0 != 0x80 {
			return
		}

		stream := c.streams[no/2]
		if int(h[5]&0x7f) != stream.Sdp.PayloadType {
			return
		}

		timestamp := binary.BigEndian.Uint32(h[8:12])
		if stream.firstTimestamp != 0 {
			timestamp -= stream.firstTimestamp
			if timestamp < stream.timestamp {
				return
			} else if timestamp-stream.timestamp > uint32(stream.timeScale()*60*60) {
				return
			}
		}
	} else { // rtcp
	}

	valid = true
	return
}

func (c *Client) parseHeaders(b []byte) (statusCode int, headers textproto.MIMEHeader, err error) {
	var line string
	r := textproto.NewReader(bufio.NewReader(bytes.NewReader(b)))
	if line, err = r.ReadLine(); err != nil {
		err = fmt.Errorf("rtsp: header invalid")
		return
	}

	if codes := strings.Split(line, " "); len(codes) >= 2 {
		if statusCode, err = strconv.Atoi(codes[1]); err != nil {
			err = fmt.Errorf("rtsp: header invalid: %s", err)
			return
		}
	}

	headers, _ = r.ReadMIMEHeader()
	return
}

func (c *Client) handleResp(res *Response) (err error) {
	if sess := res.Headers.Get("Session"); sess != "" && c.session == "" {
		if fields := strings.Split(sess, ";"); len(fields) > 0 {
			c.session = fields[0]
		}
	}
	if res.StatusCode == 302 {
		if err = c.handle302(res); err != nil {
			return
		}
	}
	if res.StatusCode == 401 {
		if err = c.handle401(res); err != nil {
			return
		}
	}
	return
}

func (c *Client) handle302(res *Response) (err error) {
	/*
		RTSP/1.0 200 OK
		CSeq: 302
	*/
	newLocation := res.Headers.Get("Location")
	fmt.Printf("\tRedirecting stream to other location: %s\n", newLocation)

	err = c.Close()
	if err != nil {
		return err
	}

	newConnect, err := Dial(newLocation)
	if err != nil {
		return err
	}

	c.requestUri = newLocation
	c.conn = newConnect.conn
	c.brConn = newConnect.brConn

	return err
}

func (c *Client) handle401(res *Response) (err error) {
	/*
		RTSP/1.0 401 Unauthorized
		CSeq: 2
		Date: Wed, May 04 2016 10:10:51 GMT
		WWW-Authenticate: Digest realm="LIVE555 Streaming Media", nonce="c633aaf8b83127633cbe98fac1d20d87"
	*/
	authval := res.Headers.Get("WWW-Authenticate")
	hdrval := strings.SplitN(authval, " ", 2)
	var realm, nonce string

	if len(hdrval) == 2 {
		for _, field := range strings.Split(hdrval[1], ",") {
			field = strings.Trim(field, ", ")
			if keyval := strings.Split(field, "="); len(keyval) == 2 {
				key := keyval[0]
				val := strings.Trim(keyval[1], `"`)
				switch key {
				case "realm":
					realm = val
				case "nonce":
					nonce = val
				}
			}
		}

		if realm != "" {
			var username string
			var password string

			if c.url.User == nil {
				err = fmt.Errorf("rtsp: no username")
				return
			}
			username = c.url.User.Username()
			password, _ = c.url.User.Password()

			c.authHeaders = func(method string) []string {
				var headers []string
				if nonce == "" {
					headers = []string{
						fmt.Sprintf(`Authorization: Basic %s`, base64.StdEncoding.EncodeToString([]byte(username+":"+password))),
					}
				} else {
					hs1 := md5hash(username + ":" + realm + ":" + password)
					hs2 := md5hash(method + ":" + c.requestUri)
					response := md5hash(hs1 + ":" + nonce + ":" + hs2)
					headers = []string{fmt.Sprintf(
						`Authorization: Digest username="%s", realm="%s", nonce="%s", uri="%s", response="%s"`,
						username, realm, nonce, c.requestUri, response)}
				}
				return headers
			}
		}
	}

	return
}

func (c *Client) findRTSP() (block []byte, data []byte, err error) {
	const (
		R = iota + 1
		T
		S
		Header
		Dollar
	)
	var _peek [8]byte
	peek := _peek[0:0]
	stat := 0

	for i := 0; ; i++ {
		var b byte
		if b, err = c.brConn.ReadByte(); err != nil {
			return
		}
		switch b {
		case 'R':
			if stat == 0 {
				stat = R
			}
		case 'T':
			if stat == R {
				stat = T
			}
		case 'S':
			if stat == T {
				stat = S
			}
		case 'P':
			if stat == S {
				stat = Header
			}
		case '$':
			if stat != Dollar {
				stat = Dollar
				peek = _peek[0:0]
			}
		default:
			if stat != Dollar {
				stat = 0
				peek = _peek[0:0]
			}
		}

		if false && c.DebugRtp {
			fmt.Println("rtsp: findRTSP", i, b)
		}

		if stat != 0 {
			peek = append(peek, b)
		}
		if stat == Header {
			data = peek
			return
		}

		if stat == Dollar && len(peek) >= 12 {
			if c.DebugRtp {
				fmt.Println("rtsp: dollar at", i, len(peek))
			}
			if blocklen, _, ok := c.parseBlockHeader(peek); ok {
				left := blocklen + 4 - len(peek)
				if left >= 0 {
					block = append(peek, make([]byte, left)...)
					if _, err = io.ReadFull(c.brConn, block[len(peek):]); err != nil {
						return
					}
					return
				} else {
					fmt.Println("Left < 0 ", blocklen, len(peek), left)
				}
			}
			stat = 0
			peek = _peek[0:0]
		}
	}

	return
}

func (c *Client) readLFLF() (block []byte, data []byte, err error) {
	const (
		LF = iota + 1
		LFLF
	)
	peek := []byte{}
	stat := 0
	dollarpos := -1
	lpos := 0
	pos := 0

	for {
		var b byte
		if b, err = c.brConn.ReadByte(); err != nil {
			return
		}
		switch b {
		case '\n':
			if stat == 0 {
				stat = LF
				lpos = pos
			} else if stat == LF {
				if pos-lpos <= 2 {
					stat = LFLF
				} else {
					lpos = pos
				}
			}
		case '$':
			dollarpos = pos
		}
		peek = append(peek, b)

		if stat == LFLF {
			data = peek
			return
		} else if dollarpos != -1 && dollarpos-pos >= 12 {
			hdrlen := dollarpos - pos
			start := len(peek) - hdrlen
			if blocklen, _, ok := c.parseBlockHeader(peek[start:]); ok {
				block = append(peek[start:], make([]byte, blocklen+4-hdrlen)...)
				if _, err = io.ReadFull(c.brConn, block[hdrlen:]); err != nil {
					return
				}
				return
			}
			dollarpos = -1
		}

		pos++
	}

	return
}

func (c *Client) readResp(b []byte) (res Response, err error) {
	if res.StatusCode, res.Headers, err = c.parseHeaders(b); err != nil {
		return
	}
	res.ContentLength, _ = strconv.Atoi(res.Headers.Get("Content-Length"))
	if res.ContentLength > 0 {
		res.Body = make([]byte, res.ContentLength)
		if _, err = io.ReadFull(c.brConn, res.Body); err != nil {
			return
		}
	}
	if err = c.handleResp(&res); err != nil {
		return
	}
	return
}

func (c *Client) poll() (res Response, err error) {
	var block []byte
	var rtsp []byte
	var headers []byte

	c.conn.Timeout = c.RtspTimeout
	for {
		if block, rtsp, err = c.findRTSP(); err != nil {
			return
		}
		if len(block) > 0 {
			res.Block = block
			return
		} else {
			if block, headers, err = c.readLFLF(); err != nil {
				return
			}
			if len(block) > 0 {
				res.Block = block
				return
			}
			if res, err = c.readResp(append(rtsp, headers...)); err != nil {
				return
			}
		}
		return
	}

	return
}

func (c *Client) ReadResponse() (res Response, err error) {
	for {
		if res, err = c.poll(); err != nil {
			return
		}
		if res.StatusCode > 0 {
			return
		}
	}
	return
}

func (c *Client) SetupAll() (err error) {
	var idx []int
	for i := range c.streams {
		idx = append(idx, i)
	}
	return c.Setup(idx)
}

func (c *Client) Setup(idx []int) (err error) {
	if err = c.prepare(stageDescribeDone); err != nil {
		return
	}

	c.setupMap = make([]int, len(c.streams))
	for i := range c.setupMap {
		c.setupMap[i] = -1
	}
	c.setupIdx = idx

	for i, si := range idx {
		c.setupMap[si] = i

		uri := ""
		control := c.streams[si].Sdp.Control
		if strings.HasPrefix(control, "rtsp://") {
			uri = control
		} else {
			uri = c.requestUri + "/" + control
		}
		req := Request{Method: "SETUP", Uri: uri}
		req.Header = append(req.Header, fmt.Sprintf("Transport: RTP/AVP/TCP;unicast;interleaved=%d-%d", si*2, si*2+1))
		if c.session != "" {
			req.Header = append(req.Header, "Session: "+c.session)
		}
		if err = c.WriteRequest(req); err != nil {
			return
		}
		if _, err = c.ReadResponse(); err != nil {
			return
		}
	}

	if c.stage == stageDescribeDone {
		c.stage = stageSetupDone
	}
	return
}

func md5hash(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

func (c *Client) Describe() (streams []sdp.Media, err error) {
	var res Response

	for i := 0; i < 2; i++ {
		req := Request{
			Method: "DESCRIBE",
			Uri:    c.requestUri,
			Header: []string{"Accept: application/sdp"},
		}
		if err = c.WriteRequest(req); err != nil {
			return
		}
		if res, err = c.ReadResponse(); err != nil {
			return
		}
		if res.StatusCode == 200 {
			break
		}
	}
	if res.ContentLength == 0 {
		err = fmt.Errorf("rtsp: Describe failed, StatusCode=%d", res.StatusCode)
		return
	}

	body := string(res.Body)

	if c.DebugRtsp {
		fmt.Println("<", body)
	}

	_, medias := sdp.Parse(body)

	c.streams = []*Stream{}
	for _, media := range medias {
		stream := &Stream{Sdp: media, client: c}
		stream.makeCodecData()
		c.streams = append(c.streams, stream)
		streams = append(streams, media)
	}
	c.stage = stageDescribeDone
	return
}

func (c *Client) Options() (err error) {
	req := Request{
		Method: "OPTIONS",
		Uri:    c.requestUri,
	}
	if c.session != "" {
		req.Header = append(req.Header, "Session: "+c.session)
	}
	if err = c.WriteRequest(req); err != nil {
		return
	}
	if _, err = c.ReadResponse(); err != nil {
		return
	}
	c.stage = stageOptionsDone
	return
}

func (c *Client) HandleCodecDataChange() (_newcli *Client, err error) {
	newcli := &Client{}
	*newcli = *c

	newcli.streams = []*Stream{}
	for _, stream := range c.streams {
		newstream := &Stream{}
		*newstream = *stream
		newstream.client = newcli

		if newstream.isCodecDataChange() {
			if err = newstream.makeCodecData(); err != nil {
				return
			}
			newstream.clearCodecDataChange()
		}
		newcli.streams = append(newcli.streams, newstream)
	}

	_newcli = newcli
	return
}

func (s *Stream) clearCodecDataChange() {
	s.spsChanged = false
	s.ppsChanged = false
}

func (s *Stream) isCodecDataChange() bool {
	if s.spsChanged && s.ppsChanged {
		return true
	}
	return false
}

func (s *Stream) timeScale() int {
	t := s.Sdp.TimeScale
	if t == 0 {
		// https://tools.ietf.org/html/rfc5391
		t = 8000
	}
	return t
}

func (s *Stream) makeCodecData() (err error) {
	media := s.Sdp
	if media.PayloadType >= 96 && media.PayloadType <= 127 {
		switch media.Type {
		case av.H264:
			for _, nalu := range media.SpropParameterSets {
				if len(nalu) > 0 {
					s.handleH264Payload(0, nalu)
				}
			}

			if len(s.sps) == 0 || len(s.pps) == 0 {
				if nalus, typ := h264parser.SplitNALUs(media.Config); typ != h264parser.NALU_RAW {
					for _, nalu := range nalus {
						if len(nalu) > 0 {
							s.handleH264Payload(0, nalu)
						}
					}
				}
			}

			if len(s.sps) > 0 && len(s.pps) > 0 {
				if s.CodecData, err = h264parser.NewCodecDataFromSPSAndPPS(s.sps, s.pps); err != nil {
					err = fmt.Errorf("rtsp: h264 sps/pps invalid: %s", err)
					return
				}
			} else {
				err = fmt.Errorf("rtsp: missing h264 sps or pps")
				return
			}

		case av.AAC:
			if len(media.Config) == 0 {
				err = fmt.Errorf("rtsp: aac sdp config missing")
				return
			}
			if s.CodecData, err = aacparser.NewCodecDataFromMPEG4AudioConfigBytes(media.Config); err != nil {
				err = fmt.Errorf("rtsp: aac sdp config invalid: %s", err)
				return
			}
		case av.OPUS:
			channelLayout := av.CH_MONO
			if media.ChannelCount == 2 {
				channelLayout = av.CH_STEREO
			}

			s.CodecData = codec.NewOpusCodecData(media.TimeScale, channelLayout)
		default:
			err = fmt.Errorf("rtsp: Type=%d unsupported", media.Type)
			return
		}
	} else {
		switch media.PayloadType {
		case 0:
			s.CodecData = codec.NewPCMMulawCodecData()

		case 8:
			s.CodecData = codec.NewPCMAlawCodecData()

		default:
			err = fmt.Errorf("rtsp: PayloadType=%d unsupported", media.PayloadType)
			return
		}
	}
	return
}

func (s *Stream) handleBuggyAnnexbH264Packet(timestamp uint32, packet []byte) (isBuggy bool, err error) {
	if len(packet) >= 4 && packet[0] == 0 && packet[1] == 0 && packet[2] == 0 && packet[3] == 1 {
		isBuggy = true
		if nalus, typ := h264parser.SplitNALUs(packet); typ != h264parser.NALU_RAW {
			for _, nalu := range nalus {
				if len(nalu) > 0 {
					if err = s.handleH264Payload(timestamp, nalu); err != nil {
						return
					}
				}
			}
		}
	}
	return
}

func (s *Stream) handleH264Payload(timestamp uint32, packet []byte) (err error) {
	if len(packet) < 2 {
		err = fmt.Errorf("rtp: h264 packet too short")
		return
	}

	var isBuggy bool
	if isBuggy, err = s.handleBuggyAnnexbH264Packet(timestamp, packet); isBuggy {
		return
	}

	naluType := packet[0] & 0x1f

	/*
		Table 7-1 – NAL unit type codes
		1   ￼Coded slice of a non-IDR picture
		5    Coded slice of an IDR picture
		6    Supplemental enhancement information (SEI)
		7    Sequence parameter set
		8    Picture parameter set
		1-23     NAL unit  Single NAL unit packet             5.6
		24       STAP-A    Single-time aggregation packet     5.7.1
		25       STAP-B    Single-time aggregation packet     5.7.1
		26       MTAP16    Multi-time aggregation packet      5.7.2
		27       MTAP24    Multi-time aggregation packet      5.7.2
		28       FU-A      Fragmentation unit                 5.8
		29       FU-B      Fragmentation unit                 5.8
		30-31    reserved                                     -
	*/
	switch {
	case naluType >= 1 && naluType <= 5:
		if naluType == 5 {
			s.pkt.IsKeyFrame = true
		}
		s.gotPacket = true
		// raw nalu to avcc
		b := make([]byte, 4+len(packet))
		pio.PutU32BE(b[0:4], uint32(len(packet)))
		copy(b[4:], packet)
		s.pkt.Data = b
		s.timestamp = timestamp

	case naluType == 7: // sps
		if s.client != nil && s.client.DebugRtp {
			fmt.Println("rtsp: got sps")
		}
		if len(s.sps) == 0 {
			s.sps = packet
			s.makeCodecData()
		} else if bytes.Compare(s.sps, packet) != 0 {
			s.spsChanged = true
			s.sps = packet
			if s.client != nil && s.client.DebugRtp {
				fmt.Println("rtsp: sps changed")
			}
		}

	case naluType == 8: // pps
		if s.client != nil && s.client.DebugRtp {
			fmt.Println("rtsp: got pps")
		}
		if len(s.pps) == 0 {
			s.pps = packet
			s.makeCodecData()
		} else if bytes.Compare(s.pps, packet) != 0 {
			s.ppsChanged = true
			s.pps = packet
			if s.client != nil && s.client.DebugRtp {
				fmt.Println("rtsp: pps changed")
			}
		}

	case naluType == 28: // FU-A
		/*
			0                   1                   2                   3
			0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
			+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
			| FU indicator  |   FU header   |                               |
			+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+                               |
			|                                                               |
			|                         FU payload                            |
			|                                                               |
			|                               +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
			|                               :...OPTIONAL RTP padding        |
			+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
			Figure 14.  RTP payload format for FU-A

			The FU indicator octet has the following format:
			+---------------+
			|0|1|2|3|4|5|6|7|
			+-+-+-+-+-+-+-+-+
			|F|NRI|  Type   |
			+---------------+


			The FU header has the following format:
			+---------------+
			|0|1|2|3|4|5|6|7|
			+-+-+-+-+-+-+-+-+
			|S|E|R|  Type   |
			+---------------+

			S: 1 bit
			When set to one, the Start bit indicates the start of a fragmented
			NAL unit.  When the following FU payload is not the start of a
			fragmented NAL unit payload, the Start bit is set to zero.

			E: 1 bit
			When set to one, the End bit indicates the end of a fragmented NAL
			unit, i.e., the last byte of the payload is also the last byte of
			the fragmented NAL unit.  When the following FU payload is not the
			last fragment of a fragmented NAL unit, the End bit is set to
			zero.

			R: 1 bit
			The Reserved bit MUST be equal to 0 and MUST be ignored by the
			receiver.

			Type: 5 bits
			The NAL unit payload type as defined in table 7-1 of [1].
		*/
		fuIndicator := packet[0]
		fuHeader := packet[1]
		isStart := fuHeader&0x80 != 0
		isEnd := fuHeader&0x40 != 0
		if isStart {
			s.fuStarted = true
			s.fuBuffer = []byte{fuIndicator&0xe0 | fuHeader&0x1f}
		}
		if s.fuStarted {
			s.fuBuffer = append(s.fuBuffer, packet[2:]...)
			if isEnd {
				s.fuStarted = false
				if err = s.handleH264Payload(timestamp, s.fuBuffer); err != nil {
					return
				}
			}
		}

	case naluType == 24: // STAP-A
		/*
			0                   1                   2                   3
			0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
			+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
			|                          RTP Header                           |
			+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
			|STAP-A NAL HDR |         NALU 1 Size           | NALU 1 HDR    |
			+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
			|                         NALU 1 Data                           |
			:                                                               :
			+               +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
			|               | NALU 2 Size                   | NALU 2 HDR    |
			+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
			|                         NALU 2 Data                           |
			:                                                               :
			|                               +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
			|                               :...OPTIONAL RTP padding        |
			+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+

			Figure 7.  An example of an RTP packet including an STAP-A
			containing two single-time aggregation units
		*/
		packet = packet[1:]
		for len(packet) >= 2 {
			size := int(packet[0])<<8 | int(packet[1])
			if size+2 > len(packet) {
				break
			}
			if err = s.handleH264Payload(timestamp, packet[2:size+2]); err != nil {
				return
			}
			packet = packet[size+2:]
		}
		return

	case naluType >= 6 && naluType <= 23: // other single NALU packet
	case naluType == 25: // STAB-B
	case naluType == 26: // MTAP-16
	case naluType == 27: // MTAP-24
	case naluType == 29: // FU-B

	default:
		err = fmt.Errorf("rtsp: unsupported H264 naluType=%d", naluType)
		return
	}

	return
}

func (s *Stream) handleRtpPacket(packet []byte) (err error) {
	if s.isCodecDataChange() {
		err = ErrCodecDataChange
		return
	}

	if s.client != nil && s.client.DebugRtp {
		fmt.Println("rtp: packet", s.CodecData.Type(), "len", len(packet))
		dumpsize := len(packet)
		if dumpsize > 32 {
			dumpsize = 32
		}
		fmt.Print(hex.Dump(packet[:dumpsize]))
	}

	/*
		0                   1                   2                   3
		0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
		+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		|V=2|P|X|  CC   |M|     PT      |       sequence number         |
		+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		|                           timestamp                           |
		+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		|           synchronization source (SSRC) identifier            |
		+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+
		|            contributing source (CSRC) identifiers             |
		|                             ....                              |
		+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
	*/
	if len(packet) < 8 {
		err = fmt.Errorf("rtp: packet too short")
		return
	}
	payloadOffset := 12 + int(packet[0]&0xf)*4
	if payloadOffset > len(packet) {
		err = fmt.Errorf("rtp: packet too short")
		return
	}
	timestamp := binary.BigEndian.Uint32(packet[4:8])
	payload := packet[payloadOffset:]

	/*
		PT 	Encoding Name 	Audio/Video (A/V) 	Clock Rate (Hz) 	Channels 	Reference
		0	PCMU	A	8000	1	[RFC3551]
		1	Reserved
		2	Reserved
		3	GSM	A	8000	1	[RFC3551]
		4	G723	A	8000	1	[Vineet_Kumar][RFC3551]
		5	DVI4	A	8000	1	[RFC3551]
		6	DVI4	A	16000	1	[RFC3551]
		7	LPC	A	8000	1	[RFC3551]
		8	PCMA	A	8000	1	[RFC3551]
		9	G722	A	8000	1	[RFC3551]
		10	L16	A	44100	2	[RFC3551]
		11	L16	A	44100	1	[RFC3551]
		12	QCELP	A	8000	1	[RFC3551]
		13	CN	A	8000	1	[RFC3389]
		14	MPA	A	90000		[RFC3551][RFC2250]
		15	G728	A	8000	1	[RFC3551]
		16	DVI4	A	11025	1	[Joseph_Di_Pol]
		17	DVI4	A	22050	1	[Joseph_Di_Pol]
		18	G729	A	8000	1	[RFC3551]
		19	Reserved	A
		20	Unassigned	A
		21	Unassigned	A
		22	Unassigned	A
		23	Unassigned	A
		24	Unassigned	V
		25	CelB	V	90000		[RFC2029]
		26	JPEG	V	90000		[RFC2435]
		27	Unassigned	V
		28	nv	V	90000		[RFC3551]
		29	Unassigned	V
		30	Unassigned	V
		31	H261	V	90000		[RFC4587]
		32	MPV	V	90000		[RFC2250]
		33	MP2T	AV	90000		[RFC2250]
		34	H263	V	90000		[Chunrong_Zhu]
		35-71	Unassigned	?
		72-76	Reserved for RTCP conflict avoidance				[RFC3551]
		77-95	Unassigned	?
		96-127	dynamic	?			[RFC3551]
	*/
	//payloadType := packet[1]&0x7f

	switch s.Sdp.Type {
	case av.H264:
		if err = s.handleH264Payload(timestamp, payload); err != nil {
			return
		}

	case av.AAC:
		if len(payload) < 4 {
			err = fmt.Errorf("rtp: aac packet too short")
			return
		}
		payload = payload[4:] // TODO: remove this hack
		s.gotPacket = true
		s.pkt.Data = payload
		s.timestamp = timestamp

	default:
		s.gotPacket = true
		s.pkt.Data = payload
		s.timestamp = timestamp
	}

	return
}

func (c *Client) Play() (err error) {
	req := Request{
		Method: "PLAY",
		Uri:    c.requestUri,
	}
	req.Header = append(req.Header, "Session: "+c.session)
	if err = c.WriteRequest(req); err != nil {
		return
	}
	if c.allCodecDataReady() {
		c.stage = stageCodecDataDone
	} else {
		c.stage = stageWaitCodecData
	}
	return
}

func (c *Client) Teardown() (err error) {
	req := Request{
		Method: "TEARDOWN",
		Uri:    c.requestUri,
	}
	req.Header = append(req.Header, "Session: "+c.session)
	if err = c.WriteRequest(req); err != nil {
		return
	}
	return
}

func (c *Client) Close() (err error) {
	return c.conn.Conn.Close()
}

func (c *Client) handleBlock(block []byte) (pkt av.Packet, ok bool, err error) {
	_, blockno, _ := c.parseBlockHeader(block)
	if blockno%2 != 0 {
		if c.DebugRtp {
			fmt.Println("rtsp: rtcp block len", len(block)-4)
		}
		return
	}

	i := blockno / 2
	if i >= len(c.streams) {
		err = fmt.Errorf("rtsp: block no=%d invalid", blockno)
		return
	}
	stream := c.streams[i]

	herr := stream.handleRtpPacket(block[4:])
	if herr != nil {
		if !c.SkipErrRtpBlock {
			err = herr
			return
		}
	}

	if stream.gotPacket {
		/*
			TODO: sync AV by rtcp NTP timestamp
			TODO: handle timestamp overflow
			https://tools.ietf.org/html/rfc3550
			A receiver can then synchronize presentation of the audio and video packets by relating
			their RTP timestamps using the timestamp pairs in RTCP SR packets.
		*/
		if stream.firstTimestamp == 0 {
			stream.firstTimestamp = stream.timestamp
		}
		stream.timestamp -= stream.firstTimestamp

		ok = true
		pkt = stream.pkt
		pkt.Time = time.Duration(stream.timestamp) * time.Second / time.Duration(stream.timeScale())
		pkt.Idx = int8(c.setupMap[i])

		if pkt.Time < stream.lastTime || pkt.Time-stream.lastTime > time.Minute*30 {
			err = fmt.Errorf("rtp: time invalid stream#%d time=%v lastTime=%v", pkt.Idx, pkt.Time, stream.lastTime)
			return
		}
		stream.lastTime = pkt.Time

		if c.DebugRtp {
			fmt.Println("rtp: pktout", pkt.Idx, pkt.Time, len(pkt.Data))
		}

		stream.pkt = av.Packet{}
		stream.gotPacket = false
	}

	return
}

func (c *Client) readPacket() (pkt av.Packet, err error) {
	if err = c.SendRtpKeepalive(); err != nil {
		return
	}

	for {
		var res Response
		for {
			if res, err = c.poll(); err != nil {
				return
			}
			if len(res.Block) > 0 {
				break
			}
		}

		var ok bool
		if pkt, ok, err = c.handleBlock(res.Block); err != nil {
			return
		}
		if ok {
			return
		}
	}

	return
}

func (c *Client) ReadPacket() (pkt av.Packet, err error) {
	if err = c.prepare(stageCodecDataDone); err != nil {
		return
	}
	return c.readPacket()
}

func Handler(h *avutil.RegisterHandler) {
	h.UrlDemuxer = func(uri string) (ok bool, demuxer av.DemuxCloser, err error) {
		if !strings.HasPrefix(uri, "rtsp://") {
			return
		}
		ok = true
		demuxer, err = Dial(uri)
		return
	}
}
