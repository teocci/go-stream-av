// Package mp4
// Created by RTT.
// Author: teocci@yandex.com on 2021-Oct-27
package mp4

import (
	"io"

	"github.com/teocci/go-stream-av/av"
	"github.com/teocci/go-stream-av/av/avutil"
)

var CodecTypes = []av.CodecType{av.H264, av.AAC}

func Handler(h *avutil.RegisterHandler) {
	h.Ext = ".mp4"

	h.Probe = func(b []byte) bool {
		switch string(b[4:8]) {
		case "moov", "ftyp", "free", "mdat", "moof":
			return true
		}
		return false
	}

	h.ReaderDemuxer = func(r io.Reader) av.Demuxer {
		return NewDemuxer(r.(io.ReadSeeker))
	}

	h.WriterMuxer = func(w io.Writer) av.Muxer {
		return NewMuxer(w.(io.WriteSeeker))
	}

	h.CodecTypes = CodecTypes
}
