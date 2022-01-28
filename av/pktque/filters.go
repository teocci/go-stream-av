// Package pktque
// Provides packet Filter interface and structures used by other components.
// Created by RTT.
// Author: teocci@yandex.com on 2021-Oct-27
package pktque

import (
	"time"

	"github.com/teocci/go-stream-av/av"
)

type Filter interface {
	// ModifyPacket changes packet time or drop packet
	ModifyPacket(pkt *av.Packet, streams []av.CodecData, videoidx int, audioidx int) (drop bool, err error)
}

// Filters type combines multiple Filters into one, ModifyPacket will be called in order.
type Filters []Filter

func (f Filters) ModifyPacket(pkt *av.Packet, streams []av.CodecData, videoidx int, audioidx int) (drop bool, err error) {
	for _, filter := range f {
		if drop, err = filter.ModifyPacket(pkt, streams, videoidx, audioidx); err != nil {
			return
		}
		if drop {
			return
		}
	}
	return
}

// FilterDemuxer wraps origin Demuxer and Filter into a new Demuxer, when read this Demuxer filters will be called.
type FilterDemuxer struct {
	av.Demuxer
	Filter   Filter
	streams  []av.CodecData
	videoidx int
	audioidx int
}

func (fd FilterDemuxer) ReadPacket() (pkt av.Packet, err error) {
	if fd.streams == nil {
		if fd.streams, err = fd.Demuxer.Streams(); err != nil {
			return
		}
		for i, stream := range fd.streams {
			if stream.Type().IsVideo() {
				fd.videoidx = i
			} else if stream.Type().IsAudio() {
				fd.audioidx = i
			}
		}
	}

	for {
		if pkt, err = fd.Demuxer.ReadPacket(); err != nil {
			return
		}
		var drop bool
		if drop, err = fd.Filter.ModifyPacket(&pkt, fd.streams, fd.videoidx, fd.audioidx); err != nil {
			return
		}
		if !drop {
			break
		}
	}

	return
}

// WaitKeyFrame drops packets until first video key frame arrived.
type WaitKeyFrame struct {
	ok bool
}

func (wkf *WaitKeyFrame) ModifyPacket(pkt *av.Packet, streams []av.CodecData, videoidx int, audioidx int) (drop bool, err error) {
	if !wkf.ok && pkt.Idx == int8(videoidx) && pkt.IsKeyFrame {
		wkf.ok = true
	}
	drop = !wkf.ok
	return
}

// FixTime fixes incorrect packet timestamps.
type FixTime struct {
	zerobase      time.Duration
	incrbase      time.Duration
	lasttime      time.Duration
	StartFromZero bool // make timestamp start from zero
	MakeIncrement bool // force timestamp increment
}

func (ft *FixTime) ModifyPacket(pkt *av.Packet, streams []av.CodecData, videoidx int, audioidx int) (drop bool, err error) {
	if ft.StartFromZero {
		if ft.zerobase == 0 {
			ft.zerobase = pkt.Time
		}
		pkt.Time -= ft.zerobase
	}

	if ft.MakeIncrement {
		pkt.Time -= ft.incrbase
		if ft.lasttime == 0 {
			ft.lasttime = pkt.Time
		}
		if pkt.Time < ft.lasttime || pkt.Time > ft.lasttime+time.Millisecond*500 {
			ft.incrbase += pkt.Time - ft.lasttime
			pkt.Time = ft.lasttime
		}
		ft.lasttime = pkt.Time
	}

	return
}

// AVSync drops incorrect packets to make A/V sync.
type AVSync struct {
	MaxTimeDiff time.Duration
	time        []time.Duration
}

func (avs *AVSync) ModifyPacket(pkt *av.Packet, streams []av.CodecData, videoidx int, audioidx int) (drop bool, err error) {
	if avs.time == nil {
		avs.time = make([]time.Duration, len(streams))
		if avs.MaxTimeDiff == 0 {
			avs.MaxTimeDiff = time.Millisecond * 500
		}
	}

	start, end, correctable, correctTime := avs.check(int(pkt.Idx))
	if pkt.Time >= start && pkt.Time < end {
		avs.time[pkt.Idx] = pkt.Time
	} else {
		if correctable {
			pkt.Time = correctTime
			for i := range avs.time {
				avs.time[i] = correctTime
			}
		} else {
			drop = true
		}
	}
	return
}

func (avs *AVSync) check(i int) (start time.Duration, end time.Duration, correctable bool, correctTime time.Duration) {
	minIdx := -1
	maxIdx := -1
	for j := range avs.time {
		if minIdx == -1 || avs.time[j] < avs.time[minIdx] {
			minIdx = j
		}
		if maxIdx == -1 || avs.time[j] > avs.time[maxIdx] {
			maxIdx = j
		}
	}
	allTheSame := avs.time[minIdx] == avs.time[maxIdx]

	if i == maxIdx {
		if allTheSame {
			correctable = true
		} else {
			correctable = false
		}
	} else {
		correctable = true
	}

	start = avs.time[minIdx]
	end = start + avs.MaxTimeDiff
	correctTime = start + time.Millisecond*40
	return
}

// Walltime makes packets reading speed as same as walltime, effect like ffmpeg -re option.
type Walltime struct {
	firsttime time.Time
}

func (wt *Walltime) ModifyPacket(pkt *av.Packet, streams []av.CodecData, videoidx int, audioidx int) (drop bool, err error) {
	if pkt.Idx == 0 {
		if wt.firsttime.IsZero() {
			wt.firsttime = time.Now()
		}
		packetTime := wt.firsttime.Add(pkt.Time)
		delta := packetTime.Sub(time.Now())
		if delta > 0 {
			time.Sleep(delta)
		}
	}
	return
}
