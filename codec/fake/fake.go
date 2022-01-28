// Package fake
// Created by RTT.
// Author: teocci@yandex.com on 2021-Oct-27
package fake

import (
	"github.com/teocci/go-stream-av/av"
)

type CodecData struct {
	CodecType_     av.CodecType
	SampleRate_    int
	SampleFormat_  av.SampleFormat
	ChannelLayout_ av.ChannelLayout
}

func (cd CodecData) Type() av.CodecType {
	return cd.CodecType_
}

func (cd CodecData) SampleFormat() av.SampleFormat {
	return cd.SampleFormat_
}

func (cd CodecData) ChannelLayout() av.ChannelLayout {
	return cd.ChannelLayout_
}

func (cd CodecData) SampleRate() int {
	return cd.SampleRate_
}
