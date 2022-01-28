// Package mp4
// Created by RTT.
// Author: teocci@yandex.com on 2021-Oct-27
package mp4

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/teocci/go-stream-av/av"
	"github.com/teocci/go-stream-av/codec/aacparser"
	"github.com/teocci/go-stream-av/codec/h264parser"
	"github.com/teocci/go-stream-av/format/mp4/mp4io"
)

type Demuxer struct {
	r         io.ReadSeeker
	streams   []*Stream
	movieAtom *mp4io.Movie
}

func NewDemuxer(r io.ReadSeeker) *Demuxer {
	return &Demuxer{
		r: r,
	}
}

func (self *Demuxer) Streams() (streams []av.CodecData, err error) {
	if err = self.probe(); err != nil {
		return
	}
	for _, stream := range self.streams {
		streams = append(streams, stream.CodecData)
	}
	return
}

func (self *Demuxer) readat(pos int64, b []byte) (err error) {
	if _, err = self.r.Seek(pos, 0); err != nil {
		return
	}
	if _, err = io.ReadFull(self.r, b); err != nil {
		return
	}
	return
}

func (self *Demuxer) probe() (err error) {
	if self.movieAtom != nil {
		return
	}

	var moov *mp4io.Movie
	var atoms []mp4io.Atom

	if atoms, err = mp4io.ReadFileAtoms(self.r); err != nil {
		return
	}
	if _, err = self.r.Seek(0, 0); err != nil {
		return
	}

	for _, atom := range atoms {
		if atom.Tag() == mp4io.MOOV {
			moov = atom.(*mp4io.Movie)
		}
	}

	if moov == nil {
		err = fmt.Errorf("mp4: 'moov' atom not found")
		return
	}

	self.streams = []*Stream{}
	for i, atrack := range moov.Tracks {
		stream := &Stream{
			trackAtom: atrack,
			demuxer:   self,
			idx:       i,
		}
		if atrack.Media != nil && atrack.Media.Info != nil && atrack.Media.Info.Sample != nil {
			stream.sample = atrack.Media.Info.Sample
			stream.timeScale = int64(atrack.Media.Header.TimeScale)
		} else {
			err = fmt.Errorf("mp4: sample table not found")
			return
		}

		if avc1 := atrack.GetAVC1Conf(); avc1 != nil {
			if stream.CodecData, err = h264parser.NewCodecDataFromAVCDecoderConfRecord(avc1.Data); err != nil {
				return
			}
			self.streams = append(self.streams, stream)
		} else if esds := atrack.GetElemStreamDesc(); esds != nil {
			if stream.CodecData, err = aacparser.NewCodecDataFromMPEG4AudioConfigBytes(esds.DecConfig); err != nil {
				return
			}
			self.streams = append(self.streams, stream)
		}
	}

	self.movieAtom = moov
	return
}

func (s *Stream) setSampleIndex(index int) (err error) {
	found := false
	start := 0
	s.chunkGroupIndex = 0

	for s.chunkIndex = range s.sample.ChunkOffset.Entries {
		if s.chunkGroupIndex+1 < len(s.sample.SampleToChunk.Entries) &&
			uint32(s.chunkIndex+1) == s.sample.SampleToChunk.Entries[s.chunkGroupIndex+1].FirstChunk {
			s.chunkGroupIndex++
		}
		n := int(s.sample.SampleToChunk.Entries[s.chunkGroupIndex].SamplesPerChunk)
		if index >= start && index < start+n {
			found = true
			s.sampleIndexInChunk = index - start
			break
		}
		start += n
	}
	if !found {
		err = fmt.Errorf("mp4: stream[%d]: cannot locate sample index in chunk", s.idx)
		return
	}

	if s.sample.SampleSize.SampleSize != 0 {
		s.sampleOffsetInChunk = int64(s.sampleIndexInChunk) * int64(s.sample.SampleSize.SampleSize)
	} else {
		if index >= len(s.sample.SampleSize.Entries) {
			err = fmt.Errorf("mp4: stream[%d]: sample index out of range", s.idx)
			return
		}
		s.sampleOffsetInChunk = int64(0)
		for i := index - s.sampleIndexInChunk; i < index; i++ {
			s.sampleOffsetInChunk += int64(s.sample.SampleSize.Entries[i])
		}
	}

	s.dts = int64(0)
	start = 0
	found = false
	s.sttsEntryIndex = 0
	for s.sttsEntryIndex < len(s.sample.TimeToSample.Entries) {
		entry := s.sample.TimeToSample.Entries[s.sttsEntryIndex]
		n := int(entry.Count)
		if index >= start && index < start+n {
			s.sampleIndexInSttsEntry = index - start
			s.dts += int64(index-start) * int64(entry.Duration)
			found = true
			break
		}
		start += n
		s.dts += int64(n) * int64(entry.Duration)
		s.sttsEntryIndex++
	}
	if !found {
		err = fmt.Errorf("mp4: stream[%d]: cannot locate sample index in stts entry", s.idx)
		return
	}

	if s.sample.CompositionOffset != nil && len(s.sample.CompositionOffset.Entries) > 0 {
		start = 0
		found = false
		s.cttsEntryIndex = 0
		for s.cttsEntryIndex < len(s.sample.CompositionOffset.Entries) {
			n := int(s.sample.CompositionOffset.Entries[s.cttsEntryIndex].Count)
			if index >= start && index < start+n {
				s.sampleIndexInCttsEntry = index - start
				found = true
				break
			}
			start += n
			s.cttsEntryIndex++
		}
		if !found {
			err = fmt.Errorf("mp4: stream[%d]: cannot locate sample index in ctts entry", s.idx)
			return
		}
	}

	if s.sample.SyncSample != nil {
		s.syncSampleIndex = 0
		for s.syncSampleIndex < len(s.sample.SyncSample.Entries)-1 {
			if s.sample.SyncSample.Entries[s.syncSampleIndex+1]-1 > uint32(index) {
				break
			}
			s.syncSampleIndex++
		}
	}

	if false {
		fmt.Printf("mp4: stream[%d]: setSampleIndex chunkGroupIndex=%d chunkIndex=%d sampleOffsetInChunk=%d\n",
			s.idx, s.chunkGroupIndex, s.chunkIndex, s.sampleOffsetInChunk)
	}

	s.sampleIndex = index
	return
}

func (s *Stream) isSampleValid() bool {
	if s.chunkIndex >= len(s.sample.ChunkOffset.Entries) {
		return false
	}
	if s.chunkGroupIndex >= len(s.sample.SampleToChunk.Entries) {
		return false
	}
	if s.sttsEntryIndex >= len(s.sample.TimeToSample.Entries) {
		return false
	}
	if s.sample.CompositionOffset != nil && len(s.sample.CompositionOffset.Entries) > 0 {
		if s.cttsEntryIndex >= len(s.sample.CompositionOffset.Entries) {
			return false
		}
	}
	if s.sample.SyncSample != nil {
		if s.syncSampleIndex >= len(s.sample.SyncSample.Entries) {
			return false
		}
	}
	if s.sample.SampleSize.SampleSize != 0 {
		if s.sampleIndex >= len(s.sample.SampleSize.Entries) {
			return false
		}
	}
	return true
}

func (s *Stream) incSampleIndex() (duration int64) {
	if false {
		fmt.Printf("incSampleIndex sampleIndex=%d sampleOffsetInChunk=%d sampleIndexInChunk=%d chunkGroupIndex=%d chunkIndex=%d\n",
			s.sampleIndex, s.sampleOffsetInChunk, s.sampleIndexInChunk, s.chunkGroupIndex, s.chunkIndex)
	}

	s.sampleIndexInChunk++
	if uint32(s.sampleIndexInChunk) == s.sample.SampleToChunk.Entries[s.chunkGroupIndex].SamplesPerChunk {
		s.chunkIndex++
		s.sampleIndexInChunk = 0
		s.sampleOffsetInChunk = int64(0)
	} else {
		if s.sample.SampleSize.SampleSize != 0 {
			s.sampleOffsetInChunk += int64(s.sample.SampleSize.SampleSize)
		} else {
			s.sampleOffsetInChunk += int64(s.sample.SampleSize.Entries[s.sampleIndex])
		}
	}

	if s.chunkGroupIndex+1 < len(s.sample.SampleToChunk.Entries) &&
		uint32(s.chunkIndex+1) == s.sample.SampleToChunk.Entries[s.chunkGroupIndex+1].FirstChunk {
		s.chunkGroupIndex++
	}

	sttsEntry := s.sample.TimeToSample.Entries[s.sttsEntryIndex]
	duration = int64(sttsEntry.Duration)
	s.sampleIndexInSttsEntry++
	s.dts += duration
	if uint32(s.sampleIndexInSttsEntry) == sttsEntry.Count {
		s.sampleIndexInSttsEntry = 0
		s.sttsEntryIndex++
	}

	if s.sample.CompositionOffset != nil && len(s.sample.CompositionOffset.Entries) > 0 {
		s.sampleIndexInCttsEntry++
		if uint32(s.sampleIndexInCttsEntry) == s.sample.CompositionOffset.Entries[s.cttsEntryIndex].Count {
			s.sampleIndexInCttsEntry = 0
			s.cttsEntryIndex++
		}
	}

	if s.sample.SyncSample != nil {
		entries := s.sample.SyncSample.Entries
		if s.syncSampleIndex+1 < len(entries) && entries[s.syncSampleIndex+1]-1 == uint32(s.sampleIndex+1) {
			s.syncSampleIndex++
		}
	}

	s.sampleIndex++
	return
}

func (s *Stream) sampleCount() int {
	if s.sample.SampleSize.SampleSize == 0 {
		chunkGroupIndex := 0
		count := 0
		for chunkIndex := range s.sample.ChunkOffset.Entries {
			n := int(s.sample.SampleToChunk.Entries[chunkGroupIndex].SamplesPerChunk)
			count += n
			if chunkGroupIndex+1 < len(s.sample.SampleToChunk.Entries) &&
				uint32(chunkIndex+1) == s.sample.SampleToChunk.Entries[chunkGroupIndex+1].FirstChunk {
				chunkGroupIndex++
			}
		}
		return count
	} else {
		return len(s.sample.SampleSize.Entries)
	}
}

func (self *Demuxer) ReadPacket() (pkt av.Packet, err error) {
	if err = self.probe(); err != nil {
		return
	}
	if len(self.streams) == 0 {
		err = errors.New("mp4: no streams available while trying to read a packet")
		return
	}

	var chosen *Stream
	var chosenidx int
	for i, stream := range self.streams {
		if chosen == nil || stream.tsToTime(stream.dts) < chosen.tsToTime(chosen.dts) {
			chosen = stream
			chosenidx = i
		}
	}
	if false {
		fmt.Printf("ReadPacket: chosen index=%v time=%v\n", chosen.idx, chosen.tsToTime(chosen.dts))
	}
	tm := chosen.tsToTime(chosen.dts)
	if pkt, err = chosen.readPacket(); err != nil {
		return
	}
	pkt.Time = tm
	pkt.Idx = int8(chosenidx)
	return
}

func (self *Demuxer) CurrentTime() (tm time.Duration) {
	if len(self.streams) > 0 {
		stream := self.streams[0]
		tm = stream.tsToTime(stream.dts)
	}
	return
}

func (self *Demuxer) SeekToTime(tm time.Duration) (err error) {
	for _, stream := range self.streams {
		if stream.Type().IsVideo() {
			if err = stream.seekToTime(tm); err != nil {
				return
			}
			tm = stream.tsToTime(stream.dts)
			break
		}
	}

	for _, stream := range self.streams {
		if !stream.Type().IsVideo() {
			if err = stream.seekToTime(tm); err != nil {
				return
			}
		}
	}

	return
}

func (s *Stream) readPacket() (pkt av.Packet, err error) {
	if !s.isSampleValid() {
		err = io.EOF
		return
	}
	//fmt.Println("readPacket", s.sampleIndex)

	chunkOffset := s.sample.ChunkOffset.Entries[s.chunkIndex]
	sampleSize := uint32(0)
	if s.sample.SampleSize.SampleSize != 0 {
		sampleSize = s.sample.SampleSize.SampleSize
	} else {
		sampleSize = s.sample.SampleSize.Entries[s.sampleIndex]
	}

	sampleOffset := int64(chunkOffset) + s.sampleOffsetInChunk
	pkt.Data = make([]byte, sampleSize)
	if err = s.demuxer.readat(sampleOffset, pkt.Data); err != nil {
		return
	}

	if s.sample.SyncSample != nil {
		if s.sample.SyncSample.Entries[s.syncSampleIndex]-1 == uint32(s.sampleIndex) {
			pkt.IsKeyFrame = true
		}
	}

	//println("pts/dts", s.ptsEntryIndex, s.dtsEntryIndex)
	if s.sample.CompositionOffset != nil && len(s.sample.CompositionOffset.Entries) > 0 {
		cts := int64(s.sample.CompositionOffset.Entries[s.cttsEntryIndex].Offset)
		pkt.CompositionTime = s.tsToTime(cts)
	}

	s.incSampleIndex()

	return
}

func (s *Stream) seekToTime(tm time.Duration) (err error) {
	index := s.timeToSampleIndex(tm)
	if err = s.setSampleIndex(index); err != nil {
		return
	}
	if false {
		fmt.Printf("stream[%d]: seekToTime index=%v time=%v cur=%v\n", s.idx, index, tm, s.tsToTime(s.dts))
	}
	return
}

func (s *Stream) timeToSampleIndex(tm time.Duration) int {
	targetTs := s.timeToTs(tm)
	targetIndex := 0

	startTs := int64(0)
	endTs := int64(0)
	startIndex := 0
	endIndex := 0
	found := false
	for _, entry := range s.sample.TimeToSample.Entries {
		endTs = startTs + int64(entry.Count*entry.Duration)
		endIndex = startIndex + int(entry.Count)
		if targetTs >= startTs && targetTs < endTs {
			targetIndex = startIndex + int((targetTs-startTs)/int64(entry.Duration))
			found = true
		}
		startTs = endTs
		startIndex = endIndex
	}
	if !found {
		if targetTs < 0 {
			targetIndex = 0
		} else {
			targetIndex = endIndex - 1
		}
	}

	if s.sample.SyncSample != nil {
		entries := s.sample.SyncSample.Entries
		for i := len(entries) - 1; i >= 0; i-- {
			if entries[i]-1 < uint32(targetIndex) {
				targetIndex = int(entries[i] - 1)
				break
			}
		}
	}

	return targetIndex
}
