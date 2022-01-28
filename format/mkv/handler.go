// Package mkv
// Created by RTT.
// Author: teocci@yandex.com on 2021-Oct-27
package mkv

import (
	"io"

	"github.com/teocci/go-stream-av/av"
	"github.com/teocci/go-stream-av/av/avutil"
)

var CodecTypes = []av.CodecType{av.H264, av.AAC}

func Handler(h *avutil.RegisterHandler) {
	h.Ext = ".mkv"

	h.Probe = func(b []byte) bool {
		return b[0] == 0x47 && b[188] == 0x47
	}

	h.ReaderDemuxer = func(r io.Reader) av.Demuxer {
		return NewDemuxer(r)
	}

	h.WriterMuxer = func(w io.Writer) av.Muxer {
		//return NewMuxer(w)
		return nil
	}

	h.CodecTypes = CodecTypes
}
