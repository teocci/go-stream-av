// Package mp4
// Created by RTT.
// Author: teocci@yandex.com on 2021-Oct-27
package mp4

import (
	"time"

	"github.com/teocci/go-stream-av/av"
	"github.com/teocci/go-stream-av/format/mp4/mp4io"
)

type Stream struct {
	av.CodecData

	trackAtom *mp4io.Track
	idx       int

	lastPacket *av.Packet

	timeScale int64
	duration  int64

	muxer   *Muxer
	demuxer *Demuxer

	sample              *mp4io.SampleTable
	sampleIndex         int
	sampleOffsetInChunk int64
	syncSampleIndex     int

	dts                    int64
	sttsEntryIndex         int
	sampleIndexInSttsEntry int

	cttsEntryIndex         int
	sampleIndexInCttsEntry int

	chunkGroupIndex    int
	chunkIndex         int
	sampleIndexInChunk int

	sttsEntry *mp4io.TimeToSampleEntry
	cttsEntry *mp4io.CompositionOffsetEntry
}

func timeToTs(tm time.Duration, timeScale int64) int64 {
	return int64(tm * time.Duration(timeScale) / time.Second)
}

func tsToTime(ts int64, timeScale int64) time.Duration {
	return time.Duration(ts) * time.Second / time.Duration(timeScale)
}

func (s *Stream) timeToTs(tm time.Duration) int64 {
	return int64(tm * time.Duration(s.timeScale) / time.Second)
}

func (s *Stream) tsToTime(ts int64) time.Duration {
	return time.Duration(ts) * time.Second / time.Duration(s.timeScale)
}
