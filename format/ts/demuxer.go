// Package ts
// Created by RTT.
// Author: teocci@yandex.com on 2021-Oct-27
package ts

import (
	"bufio"
	"fmt"
	"io"
	"time"

	"github.com/teocci/go-stream-av/av"
	"github.com/teocci/go-stream-av/codec/aacparser"
	"github.com/teocci/go-stream-av/codec/h264parser"
	"github.com/teocci/go-stream-av/format/ts/tsio"
	"github.com/teocci/go-stream-av/utils/bits/pio"
)

type Demuxer struct {
	r *bufio.Reader

	packets []av.Packet

	pat     *tsio.PAT
	pmt     *tsio.PMT
	streams []*Stream
	tsHDR   []byte

	stage int
}

func NewDemuxer(r io.Reader) *Demuxer {
	return &Demuxer{
		tsHDR: make([]byte, 188),
		r:     bufio.NewReaderSize(r, pio.RecommendBufioSize),
	}
}

func (d *Demuxer) Streams() (streams []av.CodecData, err error) {
	if err = d.probe(); err != nil {
		return
	}
	for _, stream := range d.streams {
		streams = append(streams, stream.CodecData)
	}
	return
}

func (d *Demuxer) probe() (err error) {
	if d.stage == 0 {
		for {
			if d.pmt != nil {
				n := 0
				for _, stream := range d.streams {
					if stream.CodecData != nil {
						n++
					}
				}
				if n == len(d.streams) {
					break
				}
			}
			if err = d.poll(); err != nil {
				return
			}
		}
		d.stage++
	}
	return
}

func (d *Demuxer) ReadPacket() (pkt av.Packet, err error) {
	if err = d.probe(); err != nil {
		return
	}

	for len(d.packets) == 0 {
		if err = d.poll(); err != nil {
			return
		}
	}

	pkt = d.packets[0]
	d.packets = d.packets[1:]
	return
}

func (d *Demuxer) poll() (err error) {
	if err = d.readTSPacket(); err == io.EOF {
		var n int
		if n, err = d.payloadEnd(); err != nil {
			return
		}
		if n == 0 {
			err = io.EOF
		}
	}
	return
}

func (d *Demuxer) initPMT(payload []byte) (err error) {
	var psiHDRLen int
	var dataLen int
	if _, _, psiHDRLen, dataLen, err = tsio.ParsePSI(payload); err != nil {
		return
	}
	d.pmt = &tsio.PMT{}
	if _, err = d.pmt.Unmarshal(payload[psiHDRLen : psiHDRLen+dataLen]); err != nil {
		return
	}

	d.streams = []*Stream{}
	for i, info := range d.pmt.ElementaryStreamInfos {
		stream := &Stream{}
		stream.idx = i
		stream.demuxer = d
		stream.pid = info.ElementaryPID
		stream.streamType = info.StreamType
		switch info.StreamType {
		case tsio.ElementaryStreamTypeH264:
			d.streams = append(d.streams, stream)
		case tsio.ElementaryStreamTypeAdtsAAC:
			d.streams = append(d.streams, stream)
		}
	}
	return
}

func (d *Demuxer) payloadEnd() (n int, err error) {
	for _, stream := range d.streams {
		var i int
		if i, err = stream.payloadEnd(); err != nil {
			return
		}
		n += i
	}
	return
}

func (d *Demuxer) readTSPacket() (err error) {
	var HDRLen int
	var pid uint16
	var start bool
	var isKeyFrame bool

	if _, err = io.ReadFull(d.r, d.tsHDR); err != nil {
		return
	}

	if pid, start, isKeyFrame, HDRLen, err = tsio.ParseTSHeader(d.tsHDR); err != nil {
		return
	}
	payload := d.tsHDR[HDRLen:]

	if d.pat == nil {
		if pid == 0 {
			var psiHDRLen int
			var dataLen int
			if _, _, psiHDRLen, dataLen, err = tsio.ParsePSI(payload); err != nil {
				return
			}
			d.pat = &tsio.PAT{}
			if _, err = d.pat.Unmarshal(payload[psiHDRLen : psiHDRLen+dataLen]); err != nil {
				return
			}
		}
	} else if d.pmt == nil {
		for _, entry := range d.pat.Entries {
			if entry.ProgramMapPID == pid {
				if err = d.initPMT(payload); err != nil {
					return
				}
				break
			}
		}
	} else {
		for _, stream := range d.streams {
			if pid == stream.pid {
				if stream.streamType == tsio.ElementaryStreamTypeAdtsAAC {
					isKeyFrame = false
				}
				if err = stream.handleTSPacket(start, isKeyFrame, payload); err != nil {
					return
				}
				break
			}
		}
	}

	return
}

func (s *Stream) addPacket(payload []byte, timedelta time.Duration) {
	dts := s.dts
	pts := s.pts

	if dts == 0 {
		dts = pts
	}

	dur := time.Duration(0)

	if s.pt > 0 {
		dur = dts + timedelta - s.pt
	}

	s.pt = dts + timedelta

	demuxer := s.demuxer
	pkt := av.Packet{
		Idx:        int8(s.idx),
		IsKeyFrame: s.isKeyFrame,
		Time:       dts + timedelta,
		Data:       payload,
		Duration:   dur,
	}
	if pts != dts {
		pkt.CompositionTime = pts - dts
	}
	demuxer.packets = append(demuxer.packets, pkt)
}

func (s *Stream) payloadEnd() (n int, err error) {
	payload := s.data
	if payload == nil {
		return
	}
	if s.dataLen != 0 && len(payload) != s.dataLen {
		err = fmt.Errorf("ts: packet size mismatch size=%d correct=%d", len(payload), s.dataLen)
		return
	}
	s.data = nil

	switch s.streamType {
	case tsio.ElementaryStreamTypeAdtsAAC:
		var config aacparser.MPEG4AudioConfig

		delta := time.Duration(0)
		for len(payload) > 0 {
			var hdrlen, framelen, samples int
			if config, hdrlen, framelen, samples, err = aacparser.ParseADTSHeader(payload); err != nil {
				return
			}
			if s.CodecData == nil {
				if s.CodecData, err = aacparser.NewCodecDataFromMPEG4AudioConfig(config); err != nil {
					return
				}
			}
			s.addPacket(payload[hdrlen:framelen], delta)
			n++
			delta += time.Duration(samples) * time.Second / time.Duration(config.SampleRate)
			payload = payload[framelen:]
		}

	case tsio.ElementaryStreamTypeH264:
		nalus, _ := h264parser.SplitNALUs(payload)
		var sps, pps []byte
		for _, nalu := range nalus {
			if len(nalu) > 0 {
				nalType := nalu[0] & 0x1f
				switch {
				case nalType == 7:
					sps = nalu
				case nalType == 8:
					pps = nalu
				case h264parser.IsDataNALU(nalu):
					// raw nalu to avcc
					b := make([]byte, 4+len(nalu))
					pio.PutU32BE(b[0:4], uint32(len(nalu)))
					copy(b[4:], nalu)
					s.addPacket(b, time.Duration(0))
					n++
				}
			}
		}

		if s.CodecData == nil && len(sps) > 0 && len(pps) > 0 {
			if s.CodecData, err = h264parser.NewCodecDataFromSPSAndPPS(sps, pps); err != nil {
				return
			}
		}
	}

	return
}

func (s *Stream) handleTSPacket(start bool, isKeyFrame bool, payload []byte) (err error) {
	if start {
		if _, err = s.payloadEnd(); err != nil {
			return
		}
		var hdrLen int
		if hdrLen, _, s.dataLen, s.pts, s.dts, err = tsio.ParsePESHeader(payload); err != nil {
			return
		}
		s.isKeyFrame = isKeyFrame
		if s.dataLen == 0 {
			s.data = make([]byte, 0, 4096)
		} else {
			s.data = make([]byte, 0, s.dataLen)
		}
		s.data = append(s.data, payload[hdrLen:]...)
	} else {
		s.data = append(s.data, payload...)
	}
	return
}
