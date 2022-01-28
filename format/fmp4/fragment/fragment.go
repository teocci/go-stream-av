// Package fragment
// Created by RTT.
// Author: teocci@yandex.com on 2021-Oct-27
package fragment

import (
	"time"

	"github.com/teocci/go-stream-av/av"
)

type Fragment struct {
	Bytes       []byte
	Length      int
	Independent bool
	Duration    time.Duration
}

type Fragmenter interface {
	av.PacketWriter
	Fragment() (Fragment, error)
	Duration() time.Duration
	TimeScale() uint32
	MovieHeader() (filename, contentType string, contents []byte)
	NewSegment()
}
