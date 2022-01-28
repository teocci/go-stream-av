// Package ts
// Created by RTT.
// Author: teocci@yandex.com on 2021-Oct-27
package ts

import (
	"time"

	"github.com/teocci/go-stream-av/av"
	"github.com/teocci/go-stream-av/format/ts/tsio"
)

type Stream struct {
	av.CodecData

	demuxer *Demuxer
	muxer   *Muxer

	pid        uint16
	streamId   uint8
	streamType uint8

	tsw *tsio.TSWriter
	idx int

	iskeyframe bool
	pts, dts   time.Duration
	data       []byte
	datalen    int
}
