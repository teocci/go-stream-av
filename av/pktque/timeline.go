// Package pktque
// Created by RTT.
// Author: teocci@yandex.com on 2021-Oct-27
package pktque

import (
	"time"
)

/*
pop                                   push

     seg                 seg        seg
  |--------|         |---------|   |---|
     20ms                40ms       5ms
----------------- time -------------------->
headtm                               tailtm
*/

type tlSeg struct {
	tm, dur time.Duration
}

type Timeline struct {
	segs []tlSeg
	headtm time.Duration
}

func (tl *Timeline) Push(tm time.Duration, dur time.Duration) {
	if len(tl.segs) > 0 {
		tail := tl.segs[len(tl.segs)-1]
		diff := tm-(tail.tm+tail.dur)
		if diff < 0 {
			tm -= diff
		}
	}
	tl.segs = append(tl.segs, tlSeg{tm, dur})
}

func (tl *Timeline) Pop(dur time.Duration) (tm time.Duration) {
	if len(tl.segs) == 0 {
		return tl.headtm
	}

	tm = tl.segs[0].tm
	for dur > 0 && len(tl.segs) > 0 {
		seg := &tl.segs[0]
		sub := dur
		if seg.dur < sub {
			sub = seg.dur
		}
		seg.dur -= sub
		dur -= sub
		seg.tm += sub
		tl.headtm += sub
		if seg.dur == 0 {
			copy(tl.segs[0:], tl.segs[1:])
			tl.segs = tl.segs[:len(tl.segs)-1]
		}
	}

	return
}

