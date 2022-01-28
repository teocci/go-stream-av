// Package mkv
// Created by RTT.
// Author: teocci@yandex.com on 2021-Oct-27
package mkv

import (
	"time"

	"github.com/teocci/go-stream-av/av"
)

type Stream struct {
	av.CodecData

	demuxer *Demuxer

	pid        uint16
	streamId   uint8
	streamType uint8

	idx int

	iskeyframe bool
	pts, dts   time.Duration
	data       []byte
	datalen    int
}
