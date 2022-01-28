// Package nvr
// Created by RTT.
// Author: teocci@yandex.com on 2021-Oct-27
package nvr

import (
	"log"
	"os"
	"time"

	"github.com/teocci/go-stream-av/av"
)

type Muxer struct {
	name      string
	patch     string
	started   bool
	file      *os.File
	codec     []av.CodecData
	buffer    []*av.Packet
	bufferDur time.Duration
	seqDur    time.Duration
}

//NewMuxer func
func NewMuxer(codec []av.CodecData, name, patch string, seqDur time.Duration) *Muxer {
	return &Muxer{
		codec:  codec,
		name:   name,
		patch:  patch,
		seqDur: seqDur,
	}
}

//CodecUpdate func
func (m *Muxer) CodecUpdate(val []av.CodecData) {
	m.codec = val
}

//WritePacket func
func (m *Muxer) WritePacket(pkt *av.Packet) (err error) {
	if !m.started && pkt.IsKeyFrame {
		m.started = true
	}
	if m.started {
		if pkt.IsKeyFrame && m.bufferDur >= m.seqDur {
			log.Println("write to drive", len(m.buffer), m.bufferDur)
			m.buffer = nil
			m.bufferDur = 0
		}
		m.buffer = append(m.buffer, pkt)
		if pkt.Idx == 0 {
			m.bufferDur += pkt.Duration
		}
	}
	return nil
}

//Close func
func (m *Muxer) Close() {
	return
}
