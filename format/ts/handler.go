// Package ts
// Created by RTT.
// Author: teocci@yandex.com on 2021-Oct-27
package ts

import (
	"io"

	"github.com/teocci/go-stream-av/av"
	"github.com/teocci/go-stream-av/av/avutil"
)

func Handler(h *avutil.RegisterHandler) {
	h.Ext = ".ts"

	h.Probe = func(b []byte) bool {
		return b[0] == 0x47 && b[188] == 0x47
	}

	h.ReaderDemuxer = func(r io.Reader) av.Demuxer {
		return NewDemuxer(r)
	}

	h.WriterMuxer = func(w io.Writer) av.Muxer {
		return NewMuxer(w)
	}

	h.CodecTypes = CodecTypes
}
