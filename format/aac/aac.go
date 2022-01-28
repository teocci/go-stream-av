// Package aac
// Created by RTT.
// Author: teocci@yandex.com on 2021-Oct-27
package aac

import (
	"bufio"
	"fmt"
	"io"
	"time"

	"github.com/teocci/go-stream-av/av"
	"github.com/teocci/go-stream-av/av/avutil"
	"github.com/teocci/go-stream-av/codec/aacparser"
)

type Muxer struct {
	w       io.Writer
	config  aacparser.MPEG4AudioConfig
	adtshdr []byte
}

func NewMuxer(w io.Writer) *Muxer {
	return &Muxer{
		adtshdr: make([]byte, aacparser.ADTSHeaderLength),
		w:       w,
	}
}

func (m *Muxer) WriteHeader(streams []av.CodecData) (err error) {
	if len(streams) > 1 || streams[0].Type() != av.AAC {
		err = fmt.Errorf("aac: must be only one aac stream")
		return
	}
	m.config = streams[0].(aacparser.CodecData).Config
	if m.config.ObjectType > aacparser.AOT_AAC_LTP {
		err = fmt.Errorf("aac: AOT %d is not allowed in ADTS", m.config.ObjectType)
	}
	return
}

func (m *Muxer) WritePacket(pkt av.Packet) (err error) {
	aacparser.FillADTSHeader(m.adtshdr, m.config, 1024, len(pkt.Data))
	if _, err = m.w.Write(m.adtshdr); err != nil {
		return
	}
	if _, err = m.w.Write(pkt.Data); err != nil {
		return
	}
	return
}

func (m *Muxer) WriteTrailer() (err error) {
	return
}

type Demuxer struct {
	r         *bufio.Reader
	config    aacparser.MPEG4AudioConfig
	codecdata av.CodecData
	ts        time.Duration
}

func NewDemuxer(r io.Reader) *Demuxer {
	return &Demuxer{
		r: bufio.NewReader(r),
	}
}

func (d *Demuxer) Streams() (streams []av.CodecData, err error) {
	if d.codecdata == nil {
		var adtshdr []byte
		var config aacparser.MPEG4AudioConfig
		if adtshdr, err = d.r.Peek(9); err != nil {
			return
		}
		if config, _, _, _, err = aacparser.ParseADTSHeader(adtshdr); err != nil {
			return
		}
		if d.codecdata, err = aacparser.NewCodecDataFromMPEG4AudioConfig(config); err != nil {
			return
		}
	}
	streams = []av.CodecData{d.codecdata}
	return
}

func (d *Demuxer) ReadPacket() (pkt av.Packet, err error) {
	var adtshdr []byte
	var config aacparser.MPEG4AudioConfig
	var hdrlen, framelen, samples int
	if adtshdr, err = d.r.Peek(9); err != nil {
		return
	}
	if config, hdrlen, framelen, samples, err = aacparser.ParseADTSHeader(adtshdr); err != nil {
		return
	}

	pkt.Data = make([]byte, framelen)
	if _, err = io.ReadFull(d.r, pkt.Data); err != nil {
		return
	}
	pkt.Data = pkt.Data[hdrlen:]

	pkt.Time = d.ts
	d.ts += time.Duration(samples) * time.Second / time.Duration(config.SampleRate)
	return
}

func Handler(h *avutil.RegisterHandler) {
	h.Ext = ".aac"

	h.ReaderDemuxer = func(r io.Reader) av.Demuxer {
		return NewDemuxer(r)
	}

	h.WriterMuxer = func(w io.Writer) av.Muxer {
		return NewMuxer(w)
	}

	h.Probe = func(b []byte) bool {
		_, _, _, _, err := aacparser.ParseADTSHeader(b)
		return err == nil
	}

	h.CodecTypes = []av.CodecType{av.AAC}
}
