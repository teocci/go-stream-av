// Package mp4
// Created by RTT.
// Author: teocci@yandex.com on 2021-Oct-27
package mp4

import (
	"bufio"
	"fmt"
	"io"
	"time"

	"github.com/teocci/go-stream-av/av"
	"github.com/teocci/go-stream-av/codec/aacparser"
	"github.com/teocci/go-stream-av/codec/h264parser"
	"github.com/teocci/go-stream-av/format/mp4/mp4io"
	"github.com/teocci/go-stream-av/utils/bits/pio"
)

type Muxer struct {
	w       io.WriteSeeker
	bufw    *bufio.Writer
	wpos    int64
	streams []*Stream
}

func NewMuxer(w io.WriteSeeker) *Muxer {
	return &Muxer{
		w:    w,
		bufw: bufio.NewWriterSize(w, pio.RecommendBufioSize),
	}
}

func (m *Muxer) newStream(codec av.CodecData) (err error) {
	switch codec.Type() {
	case av.H264, av.H265, av.AAC:

	default:
		err = fmt.Errorf("mp4: codec type=%v is not supported", codec.Type())
		return
	}
	stream := &Stream{CodecData: codec}

	stream.sample = &mp4io.SampleTable{
		SampleDesc:   &mp4io.SampleDesc{},
		TimeToSample: &mp4io.TimeToSample{},
		SampleToChunk: &mp4io.SampleToChunk{
			Entries: []mp4io.SampleToChunkEntry{
				{
					FirstChunk:      1,
					SampleDescId:    1,
					SamplesPerChunk: 1,
				},
			},
		},
		SampleSize:  &mp4io.SampleSize{},
		ChunkOffset: &mp4io.ChunkOffset{},
	}

	stream.trackAtom = &mp4io.Track{
		Header: &mp4io.TrackHeader{
			TrackId:  int32(len(m.streams) + 1),
			Flags:    0x0003, // Track enabled | Track in movie
			Duration: 0,      // fill later
			Matrix:   [9]int32{0x10000, 0, 0, 0, 0x10000, 0, 0, 0, 0x40000000},
		},
		Media: &mp4io.Media{
			Header: &mp4io.MediaHeader{
				TimeScale: 0, // fill later
				Duration:  0, // fill later
				Language:  21956,
			},
			Info: &mp4io.MediaInfo{
				Sample: stream.sample,
				Data: &mp4io.DataInfo{
					Refer: &mp4io.DataRefer{
						Url: &mp4io.DataReferUrl{
							Flags: 0x000001, // Self reference
						},
					},
				},
			},
		},
	}

	switch codec.Type() {
	case av.H264:
		stream.sample.SyncSample = &mp4io.SyncSample{}
	case av.H265:
		stream.sample.SyncSample = &mp4io.SyncSample{}
	}

	stream.timeScale = 90000
	stream.muxer = m
	m.streams = append(m.streams, stream)

	return
}

func (s *Stream) fillTrackAtom() (err error) {
	s.trackAtom.Media.Header.TimeScale = int32(s.timeScale)
	s.trackAtom.Media.Header.Duration = int32(s.duration)
	if s.Type() == av.H264 {
		codec := s.CodecData.(h264parser.CodecData)
		width, height := codec.Width(), codec.Height()
		s.sample.SampleDesc.AVC1Desc = &mp4io.AVC1Desc{
			DataRefIdx:           1,
			HorizontalResolution: 72,
			VorizontalResolution: 72,
			Width:                int16(width),
			Height:               int16(height),
			FrameCount:           1,
			Depth:                24,
			ColorTableId:         -1,
			Conf:                 &mp4io.AVC1Conf{Data: codec.AVCDecoderConfRecordBytes()},
		}
		s.trackAtom.Media.Handler = &mp4io.HandlerRefer{
			SubType: [4]byte{'v', 'i', 'd', 'e'},
			Name:    []byte("Video Media Handler"),
		}
		s.trackAtom.Media.Info.Video = &mp4io.VideoMediaInfo{
			Flags: 0x000001,
		}
		s.trackAtom.Header.TrackWidth = float64(width)
		s.trackAtom.Header.TrackHeight = float64(height)
	} else if s.Type() == av.H265 {
		codec := s.CodecData.(h264parser.CodecData)
		width, height := codec.Width(), codec.Height()
		s.sample.SampleDesc.HV1Desc = &mp4io.HV1Desc{
			DataRefIdx:           1,
			HorizontalResolution: 72,
			VorizontalResolution: 72,
			Width:                int16(width),
			Height:               int16(height),
			FrameCount:           1,
			Depth:                24,
			ColorTableId:         -1,
			Conf:                 &mp4io.HV1Conf{Data: codec.AVCDecoderConfRecordBytes()},
		}
		s.trackAtom.Media.Handler = &mp4io.HandlerRefer{
			SubType: [4]byte{'v', 'i', 'd', 'e'},
			Name:    []byte("Video Media Handler"),
		}
		s.trackAtom.Media.Info.Video = &mp4io.VideoMediaInfo{
			Flags: 0x000001,
		}
		s.trackAtom.Header.TrackWidth = float64(width)
		s.trackAtom.Header.TrackHeight = float64(height)
	} else if s.Type() == av.AAC {
		codec := s.CodecData.(aacparser.CodecData)
		s.sample.SampleDesc.MP4ADesc = &mp4io.MP4ADesc{
			DataRefIdx:       1,
			NumberOfChannels: int16(codec.ChannelLayout().Count()),
			SampleSize:       int16(codec.SampleFormat().BytesPerSample()),
			SampleRate:       float64(codec.SampleRate()),
			Conf: &mp4io.ElemStreamDesc{
				DecConfig: codec.MPEG4AudioConfigBytes(),
			},
		}
		s.trackAtom.Header.Volume = 1
		s.trackAtom.Header.AlternateGroup = 1
		s.trackAtom.Media.Handler = &mp4io.HandlerRefer{
			SubType: [4]byte{'s', 'o', 'u', 'n'},
			Name:    []byte("Sound Handler"),
		}
		s.trackAtom.Media.Info.Sound = &mp4io.SoundMediaInfo{}

	} else {
		err = fmt.Errorf("mp4: codec type=%d invalid", s.Type())
	}

	return
}

func (m *Muxer) WriteHeader(streams []av.CodecData) (err error) {
	m.streams = []*Stream{}
	for _, stream := range streams {
		if err = m.newStream(stream); err != nil {
			return
		}
	}

	taghdr := make([]byte, 8)
	pio.PutU32BE(taghdr[4:], uint32(mp4io.MDAT))
	if _, err = m.w.Write(taghdr); err != nil {
		return
	}
	m.wpos += 8

	for _, stream := range m.streams {
		if stream.Type().IsVideo() {
			stream.sample.CompositionOffset = &mp4io.CompositionOffset{}
		}
	}
	return
}

func (m *Muxer) WritePacket(pkt av.Packet) (err error) {
	stream := m.streams[pkt.Idx]
	if stream.lastPacket != nil {
		if err = stream.writePacket(*stream.lastPacket, pkt.Time-stream.lastPacket.Time); err != nil {
			return
		}
	}
	stream.lastPacket = &pkt
	return
}

func (s *Stream) writePacket(pkt av.Packet, rawdur time.Duration) (err error) {
	if rawdur < 0 {
		err = fmt.Errorf("mp4: stream#%d time=%v < lasttime=%v", pkt.Idx, pkt.Time, s.lastPacket.Time)
		return
	}

	if _, err = s.muxer.bufw.Write(pkt.Data); err != nil {
		return
	}

	if pkt.IsKeyFrame && s.sample.SyncSample != nil {
		s.sample.SyncSample.Entries = append(s.sample.SyncSample.Entries, uint32(s.sampleIndex+1))
	}

	duration := uint32(s.timeToTs(rawdur))
	if s.sttsEntry == nil || duration != s.sttsEntry.Duration {
		s.sample.TimeToSample.Entries = append(s.sample.TimeToSample.Entries, mp4io.TimeToSampleEntry{Duration: duration})
		s.sttsEntry = &s.sample.TimeToSample.Entries[len(s.sample.TimeToSample.Entries)-1]
	}
	
	s.sttsEntry.Count++

	if s.sample.CompositionOffset != nil {
		offset := uint32(s.timeToTs(pkt.CompositionTime))
		if s.cttsEntry == nil || offset != s.cttsEntry.Offset {
			table := s.sample.CompositionOffset
			table.Entries = append(table.Entries, mp4io.CompositionOffsetEntry{Offset: offset})
			s.cttsEntry = &table.Entries[len(table.Entries)-1]
		}
		s.cttsEntry.Count++
	}

	s.duration += int64(duration)
	s.sampleIndex++
	s.sample.ChunkOffset.Entries = append(s.sample.ChunkOffset.Entries, uint32(s.muxer.wpos))
	s.sample.SampleSize.Entries = append(s.sample.SampleSize.Entries, uint32(len(pkt.Data)))

	s.muxer.wpos += int64(len(pkt.Data))
	return
}

func (m *Muxer) WriteTrailer() (err error) {
	for _, stream := range m.streams {
		if stream.lastPacket != nil {
			if err = stream.writePacket(*stream.lastPacket, 0); err != nil {
				return
			}
			stream.lastPacket = nil
		}
	}

	moov := &mp4io.Movie{}
	moov.Header = &mp4io.MovieHeader{
		PreferredRate:   1,
		PreferredVolume: 1,
		Matrix:          [9]int32{0x10000, 0, 0, 0, 0x10000, 0, 0, 0, 0x40000000},
		NextTrackId:     2,
	}

	maxDur := time.Duration(0)
	timeScale := int64(10000)
	for _, stream := range m.streams {
		if err = stream.fillTrackAtom(); err != nil {
			return
		}
		dur := stream.tsToTime(stream.duration)
		stream.trackAtom.Header.Duration = int32(timeToTs(dur, timeScale))
		if dur > maxDur {
			maxDur = dur
		}
		moov.Tracks = append(moov.Tracks, stream.trackAtom)
	}
	moov.Header.TimeScale = int32(timeScale)
	moov.Header.Duration = int32(timeToTs(maxDur, timeScale))

	if err = m.bufw.Flush(); err != nil {
		return
	}

	var mdatsize int64
	if mdatsize, err = m.w.Seek(0, 1); err != nil {
		return
	}
	if _, err = m.w.Seek(0, 0); err != nil {
		return
	}
	taghdr := make([]byte, 4)
	pio.PutU32BE(taghdr, uint32(mdatsize))
	if _, err = m.w.Write(taghdr); err != nil {
		return
	}

	if _, err = m.w.Seek(0, 2); err != nil {
		return
	}
	b := make([]byte, moov.Len())
	moov.Marshal(b)
	if _, err = m.w.Write(b); err != nil {
		return
	}

	return
}
