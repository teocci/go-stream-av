// Package rtmp
// Created by RTT.
// Author: teocci@yandex.com on 2021-Oct-27
package rtmp

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/teocci/go-stream-av/av"
	"github.com/teocci/go-stream-av/av/avutil"
	"github.com/teocci/go-stream-av/format/flv"
	"github.com/teocci/go-stream-av/format/flv/flvio"
	"github.com/teocci/go-stream-av/utils/bits/pio"
)

var Debug bool

func ParseURL(uri string) (u *url.URL, err error) {
	if u, err = url.Parse(uri); err != nil {
		return
	}
	if _, _, serr := net.SplitHostPort(u.Host); serr != nil {
		u.Host += ":1935"
	}
	return
}

func Dial(uri string) (conn *Conn, err error) {
	return DialTimeout(uri, 0)
}

func DialTimeout(uri string, timeout time.Duration) (conn *Conn, err error) {
	var u *url.URL
	if u, err = ParseURL(uri); err != nil {
		return
	}

	dailer := net.Dialer{Timeout: timeout}
	var netconn net.Conn
	if netconn, err = dailer.Dial("tcp", u.Host); err != nil {
		return
	}

	conn = NewConn(netconn)
	conn.URL = u
	return
}

type Server struct {
	Addr          string
	HandlePublish func(*Conn)
	HandlePlay    func(*Conn)
	HandleConn    func(*Conn)
}

func (s *Server) handleConn(conn *Conn) (err error) {
	if s.HandleConn != nil {
		s.HandleConn(conn)
	} else {
		if err = conn.prepare(stageCommandDone, 0); err != nil {
			return
		}
		if conn.playing {
			if s.HandlePlay != nil {
				s.HandlePlay(conn)
			}
		} else if conn.publishing {
			if s.HandlePublish != nil {
				s.HandlePublish(conn)
			}
		}
	}

	return
}

func (s *Server) ListenAndServe() (err error) {
	addr := s.Addr
	if addr == "" {
		addr = ":1935"
	}
	var tcpaddr *net.TCPAddr
	if tcpaddr, err = net.ResolveTCPAddr("tcp", addr); err != nil {
		err = fmt.Errorf("rtmp: ListenAndServe: %s", err)
		return
	}

	var listener *net.TCPListener
	if listener, err = net.ListenTCP("tcp", tcpaddr); err != nil {
		return
	}

	if Debug {
		fmt.Println("rtmp: server: listening on", addr)
	}

	for {
		var netconn net.Conn
		if netconn, err = listener.Accept(); err != nil {
			return
		}

		if Debug {
			fmt.Println("rtmp: server: accepted")
		}

		conn := NewConn(netconn)
		conn.isserver = true
		go func() {
			err := s.handleConn(conn)
			if Debug {
				fmt.Println("rtmp: server: client closed err:", err)
			}
		}()
	}
}

const (
	stageHandshakeDone = iota + 1
	stageCommandDone
	stageCodecDataDone
)

const (
	prepareReading = iota + 1
	prepareWriting
)

type Conn struct {
	chunkHeaderBuf      []byte
	chunkHeaderBufExt   []byte
	URL                 *url.URL
	OnPlayOrPublish     func(string, flvio.AMFMap) error
	prober              *flv.Prober
	streams             []av.CodecData
	txbytes             uint64
	rxbytes             uint64
	bufr                *bufio.Reader
	bufw                *bufio.Writer
	ackn                uint32
	writebuf            []byte
	readbuf             []byte
	netconn             net.Conn
	txrxcount           *txrxcount
	writeMaxChunkSize   int
	readMaxChunkSize    int
	readAckSize         uint32
	readcsmap           map[uint32]*chunkStream
	isserver            bool
	publishing, playing bool
	reading, writing    bool
	stage               int
	avmsgsid            uint32
	gotcommand          bool
	commandname         string
	commandtransid      float64
	commandobj          flvio.AMFMap
	commandparams       []interface{}
	gotmsg              bool
	timestamp           uint32
	msgdata             []byte
	msgtypeid           uint8
	datamsgvals         []interface{}
	avtag               flvio.Tag
	eventtype           uint16
}

type txrxcount struct {
	io.ReadWriter
	txbytes uint64
	rxbytes uint64
}

func (trc *txrxcount) Read(p []byte) (int, error) {
	n, err := trc.ReadWriter.Read(p)
	trc.rxbytes += uint64(n)
	return n, err
}

func (trc *txrxcount) Write(p []byte) (int, error) {
	n, err := trc.ReadWriter.Write(p)
	trc.txbytes += uint64(n)
	return n, err
}

func NewConn(netconn net.Conn) *Conn {
	conn := &Conn{}
	conn.prober = &flv.Prober{}
	conn.netconn = netconn
	conn.readcsmap = make(map[uint32]*chunkStream)
	conn.readMaxChunkSize = 128
	conn.writeMaxChunkSize = 128
	conn.bufr = bufio.NewReaderSize(netconn, pio.RecommendBufioSize)
	conn.bufw = bufio.NewWriterSize(netconn, pio.RecommendBufioSize)
	conn.txrxcount = &txrxcount{ReadWriter: netconn}
	conn.writebuf = make([]byte, 4096)
	conn.readbuf = make([]byte, 4096)
	conn.chunkHeaderBuf = make([]byte, 265)
	conn.chunkHeaderBufExt = make([]byte, 12+4+4)
	return conn
}

type chunkStream struct {
	timenow     uint32
	timedelta   uint32
	hastimeext  bool
	msgsid      uint32
	msgtypeid   uint8
	msgdatalen  uint32
	msgdataleft uint32
	msghdrtype  uint8
	msgdata     []byte
}

func (cs *chunkStream) Start() {
	cs.msgdataleft = cs.msgdatalen
	cs.msgdata = make([]byte, cs.msgdatalen)
}

const (
	msgtypeidUserControl      = 4
	msgtypeidAck              = 3
	msgtypeidWindowAckSize    = 5
	msgtypeidSetPeerBandwidth = 6
	msgtypeidSetChunkSize     = 1
	msgtypeidCommandMsgAMF0   = 20
	msgtypeidCommandMsgAMF3   = 17
	msgtypeidDataMsgAMF0      = 18
	msgtypeidDataMsgAMF3      = 15
	msgtypeidVideoMsg         = 9
	msgtypeidAudioMsg         = 8
)

const (
	eventtypeStreamBegin      = 0
	eventtypeSetBufferLength  = 3
	eventtypeStreamIsRecorded = 4
)

func (c *Conn) NetConn() net.Conn {
	return c.netconn
}

func (c *Conn) TxBytes() uint64 {
	return c.txrxcount.txbytes
}

func (c *Conn) RxBytes() uint64 {
	return c.txrxcount.rxbytes
}

func (c *Conn) Close() (err error) {
	return c.netconn.Close()
}

func (c *Conn) pollCommand() (err error) {
	for {
		if err = c.pollMsg(); err != nil {
			return
		}
		if c.gotcommand {
			return
		}
	}
}

func (c *Conn) pollAVTag() (tag flvio.Tag, err error) {
	for {
		if err = c.pollMsg(); err != nil {
			return
		}
		switch c.msgtypeid {
		case msgtypeidVideoMsg, msgtypeidAudioMsg:
			tag = c.avtag
			return
		}
	}
}

func (c *Conn) pollMsg() (err error) {
	c.gotmsg = false
	c.gotcommand = false
	c.datamsgvals = nil
	c.avtag = flvio.Tag{}
	for {
		if err = c.readChunk(); err != nil {
			return
		}
		if c.gotmsg {
			return
		}
	}
}

func SplitPath(u *url.URL) (app, stream string) {
	pathsegs := strings.SplitN(u.RequestURI(), "/", 3)
	if len(pathsegs) > 1 {
		app = pathsegs[1]
	}
	if len(pathsegs) > 2 {
		stream = pathsegs[2]
	}
	return
}

func getTcUrl(u *url.URL) string {
	app, _ := SplitPath(u)
	nu := *u
	nu.Path = "/" + app
	return nu.String()
}

func createURL(tcurl, app, play string) (u *url.URL) {
	ps := strings.Split(app+"/"+play, "/")
	out := []string{""}
	for _, s := range ps {
		if len(s) > 0 {
			out = append(out, s)
		}
	}
	if len(out) < 2 {
		out = append(out, "")
	}
	path := strings.Join(out, "/")
	u, _ = url.ParseRequestURI(path)

	if tcurl != "" {
		tu, _ := url.Parse(tcurl)
		if tu != nil {
			u.Host = tu.Host
			u.Scheme = tu.Scheme
		}
	}
	return
}

var CodecTypes = flv.CodecTypes

func (c *Conn) writeBasicConf() (err error) {
	if err = c.writeSetChunkSize(65536); err != nil {
		return
	}
	if err = c.writeWindowAckSize(2500000); err != nil {
		return
	}
	if err = c.writeSetPeerBandwidth(10000000, 2); err != nil {
		return
	}
	return
}

func (c *Conn) readConnect() (err error) {
	var connectpath string
	if err = c.pollCommand(); err != nil {
		return
	}
	if c.commandname != "connect" {
		err = fmt.Errorf("rtmp: first command is not connect")
		return
	}
	if c.commandobj == nil {
		err = fmt.Errorf("rtmp: connect command params invalid")
		return
	}

	var ok bool
	var _app, _tcurl interface{}
	if _app, ok = c.commandobj["app"]; !ok {
		err = fmt.Errorf("rtmp: `connect` params missing `app`")
		return
	}
	connectpath, _ = _app.(string)

	var tcurl string
	if _tcurl, ok = c.commandobj["tcUrl"]; !ok {
		_tcurl, ok = c.commandobj["tcurl"]
	}
	if ok {
		tcurl, _ = _tcurl.(string)
	}
	connectparams := c.commandobj

	if err = c.writeBasicConf(); err != nil {
		return
	}

	if err = c.writeCommandMsg(3, 0, "_result", c.commandtransid,
		flvio.AMFMap{
			"fmtVer":       "FMS/3,0,1,123",
			"capabilities": 31,
			"mode":         1,
		},
		flvio.AMFMap{
			"level":          "status",
			"code":           "NetConnection.Connect.Success",
			"description":    "Connection succeeded.",
			"objectEncoding": 3,
		},
	); err != nil {
		return
	}

	if err = c.flushWrite(); err != nil {
		return
	}

	for {
		if err = c.pollMsg(); err != nil {
			return
		}
		if c.gotcommand {
			switch c.commandname {

			case "createStream":
				c.avmsgsid = uint32(1)
				if err = c.writeCommandMsg(3, 0, "_result", c.commandtransid, nil, c.avmsgsid); err != nil {
					return
				}
				if err = c.flushWrite(); err != nil {
					return
				}

			case "publish":
				if Debug {
					fmt.Println("rtmp: < publish")
				}

				if len(c.commandparams) < 1 {
					err = fmt.Errorf("rtmp: publish params invalid")
					return
				}
				publishpath, _ := c.commandparams[0].(string)

				var cberr error
				if c.OnPlayOrPublish != nil {
					cberr = c.OnPlayOrPublish(c.commandname, connectparams)
				}

				if err = c.writeCommandMsg(5, c.avmsgsid,
					"onStatus", c.commandtransid, nil,
					flvio.AMFMap{
						"level":       "status",
						"code":        "NetStream.Publish.Start",
						"description": "Start publishing",
					},
				); err != nil {
					return
				}
				if err = c.flushWrite(); err != nil {
					return
				}

				if cberr != nil {
					err = fmt.Errorf("rtmp: OnPlayOrPublish check failed")
					return
				}

				c.URL = createURL(tcurl, connectpath, publishpath)
				c.publishing = true
				c.reading = true
				c.stage++
				return

			case "play":
				if Debug {
					fmt.Println("rtmp: < play")
				}

				if len(c.commandparams) < 1 {
					err = fmt.Errorf("rtmp: command play params invalid")
					return
				}
				playpath, _ := c.commandparams[0].(string)

				if err = c.writeStreamBegin(c.avmsgsid); err != nil {
					return
				}

				if err = c.writeCommandMsg(5, c.avmsgsid,
					"onStatus", c.commandtransid, nil,
					flvio.AMFMap{
						"level":       "status",
						"code":        "NetStream.Play.Start",
						"description": "Start live",
					},
				); err != nil {
					return
				}

				if err = c.writeDataMsg(5, c.avmsgsid,
					"|RtmpSampleAccess", true, true,
				); err != nil {
					return
				}

				if err = c.flushWrite(); err != nil {
					return
				}

				c.URL = createURL(tcurl, connectpath, playpath)
				c.playing = true
				c.writing = true
				c.stage++
				return
			}

		}
	}

	return
}

func (c *Conn) checkConnectResult() (ok bool, errmsg string) {
	if len(c.commandparams) < 1 {
		errmsg = "params length < 1"
		return
	}

	obj, _ := c.commandparams[0].(flvio.AMFMap)
	if obj == nil {
		errmsg = "params[0] not object"
		return
	}

	_code, _ := obj["code"]
	if _code == nil {
		errmsg = "code invalid"
		return
	}

	code, _ := _code.(string)
	if code != "NetConnection.Connect.Success" {
		errmsg = "code != NetConnection.Connect.Success"
		return
	}

	ok = true
	return
}

func (c *Conn) checkCreateStreamResult() (ok bool, avmsgsid uint32) {
	if len(c.commandparams) < 1 {
		return
	}

	ok = true
	_avmsgsid, _ := c.commandparams[0].(float64)
	avmsgsid = uint32(_avmsgsid)
	return
}

func (c *Conn) probe() (err error) {
	for !c.prober.Probed() {
		var tag flvio.Tag
		if tag, err = c.pollAVTag(); err != nil {
			return
		}
		if err = c.prober.PushTag(tag, int32(c.timestamp)); err != nil {
			return
		}
	}

	c.streams = c.prober.Streams
	c.stage++
	return
}

func (c *Conn) writeConnect(path string) (err error) {
	if err = c.writeBasicConf(); err != nil {
		return
	}

	if Debug {
		fmt.Printf("rtmp: > connect('%s') host=%s\n", path, c.URL.Host)
	}
	if err = c.writeCommandMsg(3, 0, "connect", 1,
		flvio.AMFMap{
			"app":           path,
			"flashVer":      "MAC 22,0,0,192",
			"tcUrl":         getTcUrl(c.URL),
			"fpad":          false,
			"capabilities":  15,
			"audioCodecs":   4071,
			"videoCodecs":   252,
			"videoFunction": 1,
		},
	); err != nil {
		return
	}

	if err = c.flushWrite(); err != nil {
		return
	}

	for {
		if err = c.pollMsg(); err != nil {
			return
		}
		if c.gotcommand {
			if c.commandname == "_result" {
				var ok bool
				var errmsg string
				if ok, errmsg = c.checkConnectResult(); !ok {
					err = fmt.Errorf("rtmp: command connect failed: %s", errmsg)
					return
				}
				if Debug {
					fmt.Printf("rtmp: < _result() of connect\n")
				}
				break
			}
		} else {
			if c.msgtypeid == msgtypeidWindowAckSize {
				if len(c.msgdata) == 4 {
					c.readAckSize = pio.U32BE(c.msgdata)
				}
				if err = c.writeWindowAckSize(0xffffffff); err != nil {
					return
				}
			}
		}
	}

	return
}

func (c *Conn) connectPublish() (err error) {
	connectpath, publishpath := SplitPath(c.URL)

	if err = c.writeConnect(connectpath); err != nil {
		return
	}

	transid := 2

	if Debug {
		fmt.Printf("rtmp: > createStream()\n")
	}
	if err = c.writeCommandMsg(3, 0, "createStream", transid, nil); err != nil {
		return
	}
	transid++

	if err = c.flushWrite(); err != nil {
		return
	}

	for {
		if err = c.pollMsg(); err != nil {
			return
		}
		if c.gotcommand {
			if c.commandname == "_result" {
				var ok bool
				if ok, c.avmsgsid = c.checkCreateStreamResult(); !ok {
					err = fmt.Errorf("rtmp: createStream command failed")
					return
				}
				break
			}
		}
	}

	if Debug {
		fmt.Printf("rtmp: > publish('%s')\n", publishpath)
	}
	if err = c.writeCommandMsg(8, c.avmsgsid, "publish", transid, nil, publishpath); err != nil {
		return
	}
	transid++

	if err = c.flushWrite(); err != nil {
		return
	}

	c.writing = true
	c.publishing = true
	c.stage++
	return
}

func (c *Conn) connectPlay() (err error) {
	connectpath, playpath := SplitPath(c.URL)

	if err = c.writeConnect(connectpath); err != nil {
		return
	}

	if Debug {
		fmt.Printf("rtmp: > createStream()\n")
	}
	if err = c.writeCommandMsg(3, 0, "createStream", 2, nil); err != nil {
		return
	}

	if err = c.writeSetBufferLength(0, 100); err != nil {
		return
	}

	if err = c.flushWrite(); err != nil {
		return
	}

	for {
		if err = c.pollMsg(); err != nil {
			return
		}
		if c.gotcommand {
			if c.commandname == "_result" {
				var ok bool
				if ok, c.avmsgsid = c.checkCreateStreamResult(); !ok {
					err = fmt.Errorf("rtmp: createStream command failed")
					return
				}
				break
			}
		}
	}

	if Debug {
		fmt.Printf("rtmp: > play('%s')\n", playpath)
	}
	if err = c.writeCommandMsg(8, c.avmsgsid, "play", 0, nil, playpath); err != nil {
		return
	}
	if err = c.flushWrite(); err != nil {
		return
	}

	c.reading = true
	c.playing = true
	c.stage++
	return
}

func (c *Conn) ReadPacket() (pkt av.Packet, err error) {
	if err = c.prepare(stageCodecDataDone, prepareReading); err != nil {
		return
	}

	if !c.prober.Empty() {
		pkt = c.prober.PopPacket()
		return
	}

	for {
		var tag flvio.Tag
		if tag, err = c.pollAVTag(); err != nil {
			return
		}

		var ok bool
		if pkt, ok = c.prober.TagToPacket(tag, int32(c.timestamp)); ok {
			return
		}
	}

	return
}

func (c *Conn) Prepare() (err error) {
	return c.prepare(stageCommandDone, 0)
}

func (c *Conn) prepare(stage int, flags int) (err error) {
	for c.stage < stage {
		switch c.stage {
		case 0:
			if c.isserver {
				if err = c.handshakeServer(); err != nil {
					return
				}
			} else {
				if err = c.handshakeClient(); err != nil {
					return
				}
			}

		case stageHandshakeDone:
			if c.isserver {
				if err = c.readConnect(); err != nil {
					return
				}
			} else {
				if flags == prepareReading {
					if err = c.connectPlay(); err != nil {
						return
					}
				} else {
					if err = c.connectPublish(); err != nil {
						return
					}
				}
			}

		case stageCommandDone:
			if flags == prepareReading {
				if err = c.probe(); err != nil {
					return
				}
			} else {
				err = fmt.Errorf("rtmp: call WriteHeader() before WritePacket()")
				return
			}
		}
	}
	return
}

func (c *Conn) Streams() (streams []av.CodecData, err error) {
	if err = c.prepare(stageCodecDataDone, prepareReading); err != nil {
		return
	}
	streams = c.streams
	return
}

func (c *Conn) WritePacket(pkt av.Packet) (err error) {
	if err = c.prepare(stageCodecDataDone, prepareWriting); err != nil {
		return
	}

	stream := c.streams[pkt.Idx]
	tag, timestamp := flv.PacketToTag(pkt, stream)

	if Debug {
		fmt.Println("rtmp: WritePacket", pkt.Idx, pkt.Time, pkt.CompositionTime)
	}

	if err = c.writeAVTag(tag, int32(timestamp)); err != nil {
		return
	}
	return
}

func (c *Conn) WriteTrailer() (err error) {
	if err = c.flushWrite(); err != nil {
		return
	}
	return
}

func (c *Conn) WriteHeader(streams []av.CodecData) (err error) {
	if err = c.prepare(stageCommandDone, prepareWriting); err != nil {
		return
	}

	var metadata flvio.AMFMap
	if metadata, err = flv.NewMetadataByStreams(streams); err != nil {
		return
	}

	if err = c.writeDataMsg(5, c.avmsgsid, "onMetaData", metadata); err != nil {
		return
	}

	for _, stream := range streams {
		var ok bool
		var tag flvio.Tag
		if tag, ok, err = flv.CodecDataToTag(stream); err != nil {
			return
		}
		if ok {
			if err = c.writeAVTag(tag, 0); err != nil {
				return
			}
		}
	}

	c.streams = streams
	c.stage++
	return
}

func (c *Conn) tmpwbuf(n int) []byte {
	if len(c.writebuf) < n {
		c.writebuf = make([]byte, n)
	}
	return c.writebuf
}

func (c *Conn) writeSetChunkSize(size int) (err error) {
	c.writeMaxChunkSize = size
	b := c.tmpwbuf(chunkHeaderLength + 4)
	n := c.fillChunkHeader(b, 2, 0, msgtypeidSetChunkSize, 0, 4)
	pio.PutU32BE(b[n:], uint32(size))
	n += 4
	_, err = c.bufw.Write(b[:n])
	return
}

func (c *Conn) writeAck(seqnum uint32) (err error) {
	b := c.tmpwbuf(chunkHeaderLength + 4)
	n := c.fillChunkHeader(b, 2, 0, msgtypeidAck, 0, 4)
	pio.PutU32BE(b[n:], seqnum)
	n += 4
	_, err = c.bufw.Write(b[:n])
	return
}

func (c *Conn) writeWindowAckSize(size uint32) (err error) {
	b := c.tmpwbuf(chunkHeaderLength + 4)
	n := c.fillChunkHeader(b, 2, 0, msgtypeidWindowAckSize, 0, 4)
	pio.PutU32BE(b[n:], size)
	n += 4
	_, err = c.bufw.Write(b[:n])
	return
}

func (c *Conn) writeSetPeerBandwidth(acksize uint32, limittype uint8) (err error) {
	b := c.tmpwbuf(chunkHeaderLength + 5)
	n := c.fillChunkHeader(b, 2, 0, msgtypeidSetPeerBandwidth, 0, 5)
	pio.PutU32BE(b[n:], acksize)
	n += 4
	b[n] = limittype
	n++
	_, err = c.bufw.Write(b[:n])
	return
}

func (c *Conn) writeCommandMsg(csid, msgsid uint32, args ...interface{}) (err error) {
	return c.writeAMF0Msg(msgtypeidCommandMsgAMF0, csid, msgsid, args...)
}

func (c *Conn) writeDataMsg(csid, msgsid uint32, args ...interface{}) (err error) {
	return c.writeAMF0Msg(msgtypeidDataMsgAMF0, csid, msgsid, args...)
}

func (c *Conn) writeAMF0Msg(msgtypeid uint8, csid, msgsid uint32, args ...interface{}) (err error) {
	size := 0
	for _, arg := range args {
		size += flvio.LenAMF0Val(arg)
	}

	b := c.tmpwbuf(chunkHeaderLength + size)
	n := c.fillChunkHeader(b, csid, 0, msgtypeid, msgsid, size)
	for _, arg := range args {
		n += flvio.FillAMF0Val(b[n:], arg)
	}

	_, err = c.bufw.Write(b[:n])
	return
}
func (c *Conn) fillChunk3Header(b []byte, csid uint32, timestamp uint32) (n int) {
	b[n] = (byte(csid) & 0x3f) | 0xC0
	n++
	if timestamp >= 0xffffff {
		pio.PutU32BE(b[n:], timestamp)
		n += 4
	}
	return
}

func (c *Conn) fillChunk0Header(b []byte, csid uint32, timestamp uint32, msgtypeid uint8, msgsid uint32, msgdatalen int) (n int) {

	//  0                   1                   2                   3
	//  0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
	// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
	// |                   timestamp                   |message length |
	// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
	// |     message length (cont)     |message type id| msg stream id |
	// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
	// |           message stream id (cont)            |
	// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
	//
	//       Figure 9 Chunk Message Header – Type 0

	b[n] = byte(csid) & 0x3f
	n++
	if timestamp < 0xffffff {
		pio.PutU24BE(b[n:], uint32(timestamp))
	} else {
		pio.PutU24BE(b[n:], uint32(0xffffff))
	}
	n += 3
	pio.PutU24BE(b[n:], uint32(msgdatalen))
	n += 3
	b[n] = msgtypeid
	n++
	pio.PutU32LE(b[n:], msgsid)
	n += 4

	if timestamp >= 0xffffff {
		pio.PutU32BE(b[n:], timestamp)
		n += 4
	}
	if Debug {
		fmt.Printf("rtmp: write chunk msgdatalen=%d msgsid=%d\n", msgdatalen, msgsid)
	}

	return
}
func (c *Conn) weiteAVTagtoChunk(csid uint32, timestamp uint32, msgtypeid uint8, msgsid uint32, msgdatalen int, tag flvio.Tag) (n int, err error) {
	pos := 0
	sn := 0
	last := c.writeMaxChunkSize
	hdrlen := tag.FillHeader(c.chunkHeaderBufExt)
	tag.Data = append(c.chunkHeaderBufExt[:hdrlen], tag.Data...)
	msgdatalen = len(tag.Data)
	end := msgdatalen
	for msgdatalen+hdrlen > 0 {
		if pos == 0 {
			n = c.fillChunk0Header(c.chunkHeaderBuf, csid, timestamp, msgtypeid, msgsid, msgdatalen)
			n, err = c.bufw.Write(c.chunkHeaderBuf[:n])
			if err != nil {
				return
			}
		} else {
			n := c.fillChunk3Header(c.chunkHeaderBuf, csid, timestamp)
			_, err = c.bufw.Write(c.chunkHeaderBuf[:n])
		}
		if msgdatalen > c.writeMaxChunkSize {
			if sn, err = c.bufw.Write(tag.Data[pos:last]); err != nil {
				return
			}
			pos += sn
			last += sn
			msgdatalen -= sn
			continue
		}
		if sn, err = c.bufw.Write(tag.Data[pos:end]); err != nil {
			return
		}
		pos += sn
		msgdatalen -= sn
		return
	}
	return
}

func (c *Conn) writeAVTag(tag flvio.Tag, ts int32) (err error) {
	var msgtypeid uint8
	var csid uint32
	var data []byte

	switch tag.Type {
	case flvio.TAG_AUDIO:
		msgtypeid = msgtypeidAudioMsg
		csid = 6
		data = tag.Data

	case flvio.TAG_VIDEO:
		msgtypeid = msgtypeidVideoMsg
		csid = 7
		data = tag.Data
	}
	_, err = c.weiteAVTagtoChunk(csid, uint32(ts), msgtypeid, c.avmsgsid, len(data), tag)
	return err
}

func (c *Conn) writeStreamBegin(msgsid uint32) (err error) {
	b := c.tmpwbuf(chunkHeaderLength + 6)
	n := c.fillChunkHeader(b, 2, 0, msgtypeidUserControl, 0, 6)
	pio.PutU16BE(b[n:], eventtypeStreamBegin)
	n += 2
	pio.PutU32BE(b[n:], msgsid)
	n += 4
	_, err = c.bufw.Write(b[:n])
	return
}

func (c *Conn) writeSetBufferLength(msgsid uint32, timestamp uint32) (err error) {
	b := c.tmpwbuf(chunkHeaderLength + 10)
	n := c.fillChunkHeader(b, 2, 0, msgtypeidUserControl, 0, 10)
	pio.PutU16BE(b[n:], eventtypeSetBufferLength)
	n += 2
	pio.PutU32BE(b[n:], msgsid)
	n += 4
	pio.PutU32BE(b[n:], timestamp)
	n += 4
	_, err = c.bufw.Write(b[:n])
	return
}

const chunkHeaderLength = 12
const FlvTimestampMax = 0xFFFFFF

func (self *Conn) fillChunkHeader(b []byte, csid uint32, timestamp int32, msgtypeid uint8, msgsid uint32, msgdatalen int) (n int) {
	//  0                   1                   2                   3
	//  0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
	// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
	// |                   timestamp                   |message length |
	// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
	// |     message length (cont)     |message type id| msg stream id |
	// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
	// |           message stream id (cont)            |
	// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
	//
	//       Figure 9 Chunk Message Header – Type 0

	b[n] = byte(csid) & 0x3f
	n++
	if uint32(timestamp) <= FlvTimestampMax {
		pio.PutU24BE(b[n:], uint32(timestamp))
	} else {
		pio.PutU24BE(b[n:], FlvTimestampMax)
	}
	n += 3
	pio.PutU24BE(b[n:], uint32(msgdatalen))
	n += 3
	b[n] = msgtypeid
	n++
	pio.PutU32LE(b[n:], msgsid)
	n += 4
	if uint32(timestamp) > FlvTimestampMax {
		pio.PutU32BE(b[n:], uint32(timestamp))
		n += 4
	}

	if Debug {
		fmt.Printf("rtmp: write chunk msgdatalen=%d msgsid=%d\n", msgdatalen, msgsid)
	}

	return
}

func (c *Conn) flushWrite() (err error) {
	if err = c.bufw.Flush(); err != nil {
		return
	}
	return
}

func (c *Conn) readChunk() (err error) {
	b := c.readbuf
	n := 0
	if _, err = io.ReadFull(c.bufr, b[:1]); err != nil {
		return
	}
	header := b[0]
	n += 1

	var msghdrtype uint8
	var csid uint32

	msghdrtype = header >> 6

	csid = uint32(header) & 0x3f
	switch csid {
	default: // Chunk basic header 1
	case 0: // Chunk basic header 2
		if _, err = io.ReadFull(c.bufr, b[:1]); err != nil {
			return
		}
		n += 1
		csid = uint32(b[0]) + 64
	case 1: // Chunk basic header 3
		if _, err = io.ReadFull(c.bufr, b[:2]); err != nil {
			return
		}
		n += 2
		csid = uint32(pio.U16BE(b)) + 64
	}

	cs := c.readcsmap[csid]
	if cs == nil {
		cs = &chunkStream{}
		c.readcsmap[csid] = cs
	}

	var timestamp uint32

	switch msghdrtype {
	case 0:
		//  0                   1                   2                   3
		//  0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
		// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		// |                   timestamp                   |message length |
		// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		// |     message length (cont)     |message type id| msg stream id |
		// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		// |           message stream id (cont)            |
		// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		//
		//       Figure 9 Chunk Message Header – Type 0
		if cs.msgdataleft != 0 {
			err = fmt.Errorf("rtmp: chunk msgdataleft=%d invalid", cs.msgdataleft)
			return
		}
		h := b[:11]
		if _, err = io.ReadFull(c.bufr, h); err != nil {
			return
		}
		n += len(h)
		timestamp = pio.U24BE(h[0:3])
		cs.msghdrtype = msghdrtype
		cs.msgdatalen = pio.U24BE(h[3:6])
		cs.msgtypeid = h[6]
		cs.msgsid = pio.U32LE(h[7:11])
		if timestamp == 0xffffff {
			if _, err = io.ReadFull(c.bufr, b[:4]); err != nil {
				return
			}
			n += 4
			timestamp = pio.U32BE(b)
			cs.hastimeext = true
		} else {
			cs.hastimeext = false
		}
		cs.timenow = timestamp
		cs.Start()

	case 1:
		//  0                   1                   2                   3
		//  0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
		// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		// |                timestamp delta                |message length |
		// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		// |     message length (cont)     |message type id|
		// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		//
		//       Figure 10 Chunk Message Header – Type 1
		if cs.msgdataleft != 0 {
			err = fmt.Errorf("rtmp: chunk msgdataleft=%d invalid", cs.msgdataleft)
			return
		}
		h := b[:7]
		if _, err = io.ReadFull(c.bufr, h); err != nil {
			return
		}
		n += len(h)
		timestamp = pio.U24BE(h[0:3])
		cs.msghdrtype = msghdrtype
		cs.msgdatalen = pio.U24BE(h[3:6])
		cs.msgtypeid = h[6]
		if timestamp == 0xffffff {
			if _, err = io.ReadFull(c.bufr, b[:4]); err != nil {
				return
			}
			n += 4
			timestamp = pio.U32BE(b)
			cs.hastimeext = true
		} else {
			cs.hastimeext = false
		}
		cs.timedelta = timestamp
		cs.timenow += timestamp
		cs.Start()

	case 2:
		//  0                   1                   2
		//  0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3
		// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		// |                timestamp delta                |
		// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		//
		//       Figure 11 Chunk Message Header – Type 2
		if cs.msgdataleft != 0 {
			err = fmt.Errorf("rtmp: chunk msgdataleft=%d invalid", cs.msgdataleft)
			return
		}
		h := b[:3]
		if _, err = io.ReadFull(c.bufr, h); err != nil {
			return
		}
		n += len(h)
		cs.msghdrtype = msghdrtype
		timestamp = pio.U24BE(h[0:3])
		if timestamp == 0xffffff {
			if _, err = io.ReadFull(c.bufr, b[:4]); err != nil {
				return
			}
			n += 4
			timestamp = pio.U32BE(b)
			cs.hastimeext = true
		} else {
			cs.hastimeext = false
		}
		cs.timedelta = timestamp
		cs.timenow += timestamp
		cs.Start()

	case 3:
		if cs.msgdataleft == 0 {
			switch cs.msghdrtype {
			case 0:
				if cs.hastimeext {
					if _, err = io.ReadFull(c.bufr, b[:4]); err != nil {
						return
					}
					n += 4
					timestamp = pio.U32BE(b)
					cs.timenow = timestamp
				}
			case 1, 2:
				if cs.hastimeext {
					if _, err = io.ReadFull(c.bufr, b[:4]); err != nil {
						return
					}
					n += 4
					timestamp = pio.U32BE(b)
				} else {
					timestamp = cs.timedelta
				}
				cs.timenow += timestamp
			}
			cs.Start()
		}

	default:
		err = fmt.Errorf("rtmp: invalid chunk msg header type=%d", msghdrtype)
		return
	}

	size := int(cs.msgdataleft)
	if size > c.readMaxChunkSize {
		size = c.readMaxChunkSize
	}
	off := cs.msgdatalen - cs.msgdataleft
	buf := cs.msgdata[off : int(off)+size]
	if _, err = io.ReadFull(c.bufr, buf); err != nil {
		return
	}
	n += len(buf)
	cs.msgdataleft -= uint32(size)

	if Debug {
		fmt.Printf("rtmp: chunk msgsid=%d msgtypeid=%d msghdrtype=%d len=%d left=%d\n",
			cs.msgsid, cs.msgtypeid, cs.msghdrtype, cs.msgdatalen, cs.msgdataleft)
	}

	if cs.msgdataleft == 0 {
		if Debug {
			fmt.Println("rtmp: chunk data")
			fmt.Print(hex.Dump(cs.msgdata))
		}

		if err = c.handleMsg(cs.timenow, cs.msgsid, cs.msgtypeid, cs.msgdata); err != nil {
			return
		}
	}

	c.ackn += uint32(n)
	if c.readAckSize != 0 && c.ackn > c.readAckSize {
		if err = c.writeAck(c.ackn); err != nil {
			return
		}
		c.ackn = 0
	}

	return
}

func (c *Conn) handleCommandMsgAMF0(b []byte) (n int, err error) {
	var name, transid, obj interface{}
	var size int

	if name, size, err = flvio.ParseAMF0Val(b[n:]); err != nil {
		return
	}
	n += size
	if transid, size, err = flvio.ParseAMF0Val(b[n:]); err != nil {
		return
	}
	n += size
	if obj, size, err = flvio.ParseAMF0Val(b[n:]); err != nil {
		return
	}
	n += size

	var ok bool
	if c.commandname, ok = name.(string); !ok {
		err = fmt.Errorf("rtmp: CommandMsgAMF0 command is not string")
		return
	}
	c.commandtransid, _ = transid.(float64)
	c.commandobj, _ = obj.(flvio.AMFMap)
	c.commandparams = []interface{}{}

	for n < len(b) {
		if obj, size, err = flvio.ParseAMF0Val(b[n:]); err != nil {
			return
		}
		n += size
		c.commandparams = append(c.commandparams, obj)
	}
	if n < len(b) {
		err = fmt.Errorf("rtmp: CommandMsgAMF0 left bytes=%d", len(b)-n)
		return
	}

	c.gotcommand = true
	return
}

func (c *Conn) handleMsg(timestamp uint32, msgsid uint32, msgtypeid uint8, msgdata []byte) (err error) {
	c.msgdata = msgdata
	c.msgtypeid = msgtypeid
	c.timestamp = timestamp

	switch msgtypeid {
	case msgtypeidCommandMsgAMF0:
		if _, err = c.handleCommandMsgAMF0(msgdata); err != nil {
			return
		}

	case msgtypeidCommandMsgAMF3:
		if len(msgdata) < 1 {
			err = fmt.Errorf("rtmp: short packet of CommandMsgAMF3")
			return
		}
		// skip first byte
		if _, err = c.handleCommandMsgAMF0(msgdata[1:]); err != nil {
			return
		}

	case msgtypeidUserControl:
		if len(msgdata) < 2 {
			err = fmt.Errorf("rtmp: short packet of UserControl")
			return
		}
		c.eventtype = pio.U16BE(msgdata)

	case msgtypeidDataMsgAMF0:
		b := msgdata
		n := 0
		for n < len(b) {
			var obj interface{}
			var size int
			if obj, size, err = flvio.ParseAMF0Val(b[n:]); err != nil {
				return
			}
			n += size
			c.datamsgvals = append(c.datamsgvals, obj)
		}
		if n < len(b) {
			err = fmt.Errorf("rtmp: DataMsgAMF0 left bytes=%d", len(b)-n)
			return
		}

	case msgtypeidVideoMsg:
		if len(msgdata) == 0 {
			return
		}
		tag := flvio.Tag{Type: flvio.TAG_VIDEO}
		var n int
		if n, err = (&tag).ParseHeader(msgdata); err != nil {
			return
		}
		if !(tag.FrameType == flvio.FRAME_INTER || tag.FrameType == flvio.FRAME_KEY) {
			return
		}
		tag.Data = msgdata[n:]
		c.avtag = tag

	case msgtypeidAudioMsg:
		if len(msgdata) == 0 {
			return
		}
		tag := flvio.Tag{Type: flvio.TAG_AUDIO}
		var n int
		if n, err = (&tag).ParseHeader(msgdata); err != nil {
			return
		}
		tag.Data = msgdata[n:]
		c.avtag = tag

	case msgtypeidSetChunkSize:
		if len(msgdata) < 4 {
			err = fmt.Errorf("rtmp: short packet of SetChunkSize")
			return
		}
		c.readMaxChunkSize = int(pio.U32BE(msgdata))
		return
	}

	c.gotmsg = true
	return
}

var (
	hsClientFullKey = []byte{
		'G', 'e', 'n', 'u', 'i', 'n', 'e', ' ', 'A', 'd', 'o', 'b', 'e', ' ',
		'F', 'l', 'a', 's', 'h', ' ', 'P', 'l', 'a', 'y', 'e', 'r', ' ',
		'0', '0', '1',
		0xF0, 0xEE, 0xC2, 0x4A, 0x80, 0x68, 0xBE, 0xE8, 0x2E, 0x00, 0xD0, 0xD1,
		0x02, 0x9E, 0x7E, 0x57, 0x6E, 0xEC, 0x5D, 0x2D, 0x29, 0x80, 0x6F, 0xAB,
		0x93, 0xB8, 0xE6, 0x36, 0xCF, 0xEB, 0x31, 0xAE,
	}
	hsServerFullKey = []byte{
		'G', 'e', 'n', 'u', 'i', 'n', 'e', ' ', 'A', 'd', 'o', 'b', 'e', ' ',
		'F', 'l', 'a', 's', 'h', ' ', 'M', 'e', 'd', 'i', 'a', ' ',
		'S', 'e', 'r', 'v', 'e', 'r', ' ',
		'0', '0', '1',
		0xF0, 0xEE, 0xC2, 0x4A, 0x80, 0x68, 0xBE, 0xE8, 0x2E, 0x00, 0xD0, 0xD1,
		0x02, 0x9E, 0x7E, 0x57, 0x6E, 0xEC, 0x5D, 0x2D, 0x29, 0x80, 0x6F, 0xAB,
		0x93, 0xB8, 0xE6, 0x36, 0xCF, 0xEB, 0x31, 0xAE,
	}
	hsClientPartialKey = hsClientFullKey[:30]
	hsServerPartialKey = hsServerFullKey[:36]
)

func hsMakeDigest(key []byte, src []byte, gap int) (dst []byte) {
	h := hmac.New(sha256.New, key)
	if gap <= 0 {
		h.Write(src)
	} else {
		h.Write(src[:gap])
		h.Write(src[gap+32:])
	}
	return h.Sum(nil)
}

func hsCalcDigestPos(p []byte, base int) (pos int) {
	for i := 0; i < 4; i++ {
		pos += int(p[base+i])
	}
	pos = (pos % 728) + base + 4
	return
}

func hsFindDigest(p []byte, key []byte, base int) int {
	gap := hsCalcDigestPos(p, base)
	digest := hsMakeDigest(key, p, gap)
	if bytes.Compare(p[gap:gap+32], digest) != 0 {
		return -1
	}
	return gap
}

func hsParse1(p []byte, peerkey []byte, key []byte) (ok bool, digest []byte) {
	var pos int
	if pos = hsFindDigest(p, peerkey, 772); pos == -1 {
		if pos = hsFindDigest(p, peerkey, 8); pos == -1 {
			return
		}
	}
	ok = true
	digest = hsMakeDigest(key, p[pos:pos+32], -1)
	return
}

func hsCreate01(p []byte, time uint32, ver uint32, key []byte) {
	p[0] = 3
	p1 := p[1:]
	rand.Read(p1[8:])
	pio.PutU32BE(p1[0:4], time)
	pio.PutU32BE(p1[4:8], ver)
	gap := hsCalcDigestPos(p1, 8)
	digest := hsMakeDigest(key, p1, gap)
	copy(p1[gap:], digest)
}

func hsCreate2(p []byte, key []byte) {
	rand.Read(p)
	gap := len(p) - 32
	digest := hsMakeDigest(key, p, gap)
	copy(p[gap:], digest)
}

func (c *Conn) handshakeClient() (err error) {
	var random [(1 + 1536*2) * 2]byte

	C0C1C2 := random[:1536*2+1]
	C0 := C0C1C2[:1]
	C0C1 := C0C1C2[:1536+1]
	C2 := C0C1C2[1536+1:]
	S0S1S2 := random[1536*2+1:]
	S1 := S0S1S2[1 : 1536+1]
	C0[0] = 3

	if _, err = c.bufw.Write(C0C1); err != nil {
		return
	}
	if err = c.bufw.Flush(); err != nil {
		return
	}

	if _, err = io.ReadFull(c.bufr, S0S1S2); err != nil {
		return
	}

	if Debug {
		fmt.Println("rtmp: handshakeClient: server version", S1[4], S1[5], S1[6], S1[7])
	}

	if ver := pio.U32BE(S1[4:8]); ver != 0 {
		C2 = S1
	} else {
		C2 = S1
	}

	if _, err = c.bufw.Write(C2); err != nil {
		return
	}

	c.stage++
	return
}

func (c *Conn) handshakeServer() (err error) {
	var random [(1 + 1536*2) * 2]byte

	C0C1C2 := random[:1536*2+1]
	C0 := C0C1C2[:1]
	C1 := C0C1C2[1 : 1536+1]
	C0C1 := C0C1C2[:1536+1]
	C2 := C0C1C2[1536+1:]
	S0S1S2 := random[1536*2+1:]
	S0 := S0S1S2[:1]
	S1 := S0S1S2[1 : 1536+1]
	S0S1 := S0S1S2[:1536+1]
	S2 := S0S1S2[1536+1:]

	if _, err = io.ReadFull(c.bufr, C0C1); err != nil {
		return
	}
	if C0[0] != 3 {
		err = fmt.Errorf("rtmp: handshake version=%d invalid", C0[0])
		return
	}

	S0[0] = 3

	clitime := pio.U32BE(C1[0:4])
	srvtime := clitime
	srvver := uint32(0x0d0e0a0d)
	cliver := pio.U32BE(C1[4:8])

	if cliver != 0 {
		var ok bool
		var digest []byte
		if ok, digest = hsParse1(C1, hsClientPartialKey, hsServerFullKey); !ok {
			err = fmt.Errorf("rtmp: handshake server: C1 invalid")
			return
		}
		hsCreate01(S0S1, srvtime, srvver, hsServerPartialKey)
		hsCreate2(S2, digest)
	} else {
		copy(S1, C1)
		copy(S2, C2)
	}

	if _, err = c.bufw.Write(S0S1S2); err != nil {
		return
	}
	if err = c.bufw.Flush(); err != nil {
		return
	}

	if _, err = io.ReadFull(c.bufr, C2); err != nil {
		return
	}

	c.stage++
	return
}

type closeConn struct {
	*Conn
	waitclose chan bool
}

func (self closeConn) Close() error {
	self.waitclose <- true
	return nil
}

func Handler(h *avutil.RegisterHandler) {
	h.UrlDemuxer = func(uri string) (ok bool, demuxer av.DemuxCloser, err error) {
		if !strings.HasPrefix(uri, "rtmp://") {
			return
		}
		ok = true
		demuxer, err = Dial(uri)
		return
	}

	h.UrlMuxer = func(uri string) (ok bool, muxer av.MuxCloser, err error) {
		if !strings.HasPrefix(uri, "rtmp://") {
			return
		}
		ok = true
		muxer, err = Dial(uri)
		return
	}

	h.ServerMuxer = func(uri string) (ok bool, muxer av.MuxCloser, err error) {
		if !strings.HasPrefix(uri, "rtmp://") {
			return
		}
		ok = true

		var u *url.URL
		if u, err = ParseURL(uri); err != nil {
			return
		}
		server := &Server{
			Addr: u.Host,
		}

		waitstart := make(chan error)
		waitconn := make(chan *Conn)
		waitclose := make(chan bool)

		server.HandlePlay = func(conn *Conn) {
			waitconn <- conn
			<-waitclose
		}

		go func() {
			waitstart <- server.ListenAndServe()
		}()

		select {
		case err = <-waitstart:
			if err != nil {
				return
			}

		case conn := <-waitconn:
			muxer = closeConn{Conn: conn, waitclose: waitclose}
			return
		}

		return
	}

	h.ServerDemuxer = func(uri string) (ok bool, demuxer av.DemuxCloser, err error) {
		if !strings.HasPrefix(uri, "rtmp://") {
			return
		}
		ok = true

		var u *url.URL
		if u, err = ParseURL(uri); err != nil {
			return
		}
		server := &Server{
			Addr: u.Host,
		}

		waitstart := make(chan error)
		waitconn := make(chan *Conn)
		waitclose := make(chan bool)

		server.HandlePublish = func(conn *Conn) {
			waitconn <- conn
			<-waitclose
		}

		go func() {
			waitstart <- server.ListenAndServe()
		}()

		select {
		case err = <-waitstart:
			if err != nil {
				return
			}

		case conn := <-waitconn:
			demuxer = closeConn{Conn: conn, waitclose: waitclose}
			return
		}

		return
	}

	h.CodecTypes = CodecTypes
}
