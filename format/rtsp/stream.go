// Package rtsp
// Created by RTT.
// Author: teocci@yandex.com on 2021-Oct-27
package rtsp

import (
	"time"

	"github.com/teocci/go-stream-av/av"
	"github.com/teocci/go-stream-av/format/rtsp/sdp"
)

type Stream struct {
	av.CodecData
	Sdp    sdp.Media
	client *Client

	// h264
	fuStarted  bool
	fuBuffer   []byte
	sps        []byte
	pps        []byte
	spsChanged bool
	ppsChanged bool

	gotPacket      bool
	pkt            av.Packet
	timestamp      uint32
	firstTimestamp uint32

	lastTime time.Duration
}
