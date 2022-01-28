// Package format
// Created by RTT.
// Author: teocci@yandex.com on 2021-Oct-27
package format

import (
	"github.com/teocci/go-stream-av/av/avutil"
	"github.com/teocci/go-stream-av/format/aac"
	"github.com/teocci/go-stream-av/format/flv"
	"github.com/teocci/go-stream-av/format/mp4"
	"github.com/teocci/go-stream-av/format/rtmp"
	"github.com/teocci/go-stream-av/format/rtsp"
	"github.com/teocci/go-stream-av/format/ts"
)

func RegisterAll() {
	avutil.DefaultHandlers.Add(mp4.Handler)
	avutil.DefaultHandlers.Add(ts.Handler)
	avutil.DefaultHandlers.Add(rtmp.Handler)
	avutil.DefaultHandlers.Add(rtsp.Handler)
	avutil.DefaultHandlers.Add(flv.Handler)
	avutil.DefaultHandlers.Add(aac.Handler)
}
