// Package av
// Defines basic interfaces and data structures of container demux/mux and audio encode/decode.
// Created by RTT.
// Author: teocci@yandex.com on 2021-Oct-27
package av

import (
	"fmt"
	"time"
)

// SampleFormat Audio sample format.
type SampleFormat uint8

const (
	U8   = SampleFormat(iota + 1) // 8-bit unsigned integer
	S16                           // signed 16-bit integer
	S32                           // signed 32-bit integer
	FLT                           // 32-bit float
	DBL                           // 64-bit float
	U8P                           // 8-bit unsigned integer in planar
	S16P                          // signed 16-bit integer in planar
	S32P                          // signed 32-bit integer in planar
	FLTP                          // 32-bit float in planar
	DBLP                          // 64-bit float in planar
	U32                           // unsigned 32-bit integer
)

func (sf SampleFormat) BytesPerSample() int {
	switch sf {
	case U8, U8P:
		return 1
	case S16, S16P:
		return 2
	case FLT, FLTP, S32, S32P, U32:
		return 4
	case DBL, DBLP:
		return 8
	default:
		return 0
	}
}

func (sf SampleFormat) String() string {
	switch sf {
	case U8:
		return "U8"
	case S16:
		return "S16"
	case S32:
		return "S32"
	case FLT:
		return "FLT"
	case DBL:
		return "DBL"
	case U8P:
		return "U8P"
	case S16P:
		return "S16P"
	case FLTP:
		return "FLTP"
	case DBLP:
		return "DBLP"
	case U32:
		return "U32"
	default:
		return "?"
	}
}

// IsPlanar checks if this sample format is in planar.
func (sf SampleFormat) IsPlanar() bool {
	switch sf {
	case S16P, S32P, FLTP, DBLP:
		return true
	default:
		return false
	}
}

// ChannelLayout represents an audio channel layout.
type ChannelLayout uint16

func (cl ChannelLayout) String() string {
	return fmt.Sprintf("%dch", cl.Count())
}

const (
	CH_FRONT_CENTER = ChannelLayout(1 << iota)
	CH_FRONT_LEFT
	CH_FRONT_RIGHT
	CH_BACK_CENTER
	CH_BACK_LEFT
	CH_BACK_RIGHT
	CH_SIDE_LEFT
	CH_SIDE_RIGHT
	CH_LOW_FREQ
	CH_NR

	CH_MONO     = ChannelLayout(CH_FRONT_CENTER)
	CH_STEREO   = ChannelLayout(CH_FRONT_LEFT | CH_FRONT_RIGHT)
	CH_2_1      = ChannelLayout(CH_STEREO | CH_BACK_CENTER)
	CH_2POINT1  = ChannelLayout(CH_STEREO | CH_LOW_FREQ)
	CH_SURROUND = ChannelLayout(CH_STEREO | CH_FRONT_CENTER)
	CH_3POINT1  = ChannelLayout(CH_SURROUND | CH_LOW_FREQ)
	// TODO: add all channel_layout in ffmpeg
)

func (cl ChannelLayout) Count() (n int) {
	for cl != 0 {
		n++
		cl = (cl - 1) & cl
	}
	return
}

// CodecType represents a Video/Audio codec type. can be H264/AAC/SPEEX/...
type CodecType uint32

var (
	H264       = MakeVideoCodecType(avCodecTypeMagic + 1)
	H265       = MakeVideoCodecType(avCodecTypeMagic + 2)
	JPEG       = MakeVideoCodecType(avCodecTypeMagic + 3)
	VP8        = MakeVideoCodecType(avCodecTypeMagic + 4)
	VP9        = MakeVideoCodecType(avCodecTypeMagic + 5)
	AV1        = MakeVideoCodecType(avCodecTypeMagic + 6)
	AAC        = MakeAudioCodecType(avCodecTypeMagic + 1)
	PCM_MULAW  = MakeAudioCodecType(avCodecTypeMagic + 2)
	PCM_ALAW   = MakeAudioCodecType(avCodecTypeMagic + 3)
	SPEEX      = MakeAudioCodecType(avCodecTypeMagic + 4)
	NELLYMOSER = MakeAudioCodecType(avCodecTypeMagic + 5)
	PCM        = MakeAudioCodecType(avCodecTypeMagic + 6)
	OPUS       = MakeAudioCodecType(avCodecTypeMagic + 7)
)

const codecTypeAudioBit = 0x1
const codecTypeOtherBits = 1

func (ct CodecType) String() string {
	switch ct {
	case H264:
		return "H264"
	case H265:
		return "H265"
	case JPEG:
		return "JPEG"
	case VP8:
		return "VP8"
	case VP9:
		return "VP9"
	case AV1:
		return "AV1"
	case AAC:
		return "AAC"
	case PCM_MULAW:
		return "PCM_MULAW"
	case PCM_ALAW:
		return "PCM_ALAW"
	case SPEEX:
		return "SPEEX"
	case NELLYMOSER:
		return "NELLYMOSER"
	case PCM:
		return "PCM"
	case OPUS:
		return "OPUS"
	}
	return ""
}

func (ct CodecType) IsAudio() bool {
	return ct&codecTypeAudioBit != 0
}

func (ct CodecType) IsVideo() bool {
	return ct&codecTypeAudioBit == 0
}

// MakeAudioCodecType creates a new audio codec type.
func MakeAudioCodecType(base uint32) (c CodecType) {
	c = CodecType(base)<<codecTypeOtherBits | CodecType(codecTypeAudioBit)
	return
}

// MakeVideoCodecType creates a new video codec type.
func MakeVideoCodecType(base uint32) (c CodecType) {
	c = CodecType(base) << codecTypeOtherBits
	return
}

const avCodecTypeMagic = 233333

// CodecData is some important bytes for initializing audio/video decoder,
// can be converted to VideoCodecData or AudioCodecData using:
//
//     codecdata.(AudioCodecData) or codecdata.(VideoCodecData)
//
// for H264, CodecData is AVCDecoderConfigure bytes, includes SPS/PPS.
type CodecData interface {
	Type() CodecType // Video/Audio codec type
}

type VideoCodecData interface {
	CodecData
	Width() int  // Video width
	Height() int // Video height
}

type AudioCodecData interface {
	CodecData
	SampleFormat() SampleFormat                   // audio sample format
	SampleRate() int                              // audio sample rate
	ChannelLayout() ChannelLayout                 // audio channel layout
	PacketDuration([]byte) (time.Duration, error) // get audio compressed packet duration
}

type PacketWriter interface {
	WritePacket(Packet) error
}

type PacketReader interface {
	ReadPacket() (Packet, error)
}

// Muxer describes the steps of writing compressed audio/video packets into container formats like MP4/FLV/MPEG-TS.
//
// Container formats, rtmp.Conn, and transcode.Muxer implements Muxer interface.
type Muxer interface {
	WriteHeader([]CodecData) error // write the file header
	PacketWriter                   // write compressed audio/video packets
	WriteTrailer() error           // finish writing file, this func can be called only once
}

// MuxCloser is a Muxer with Close() method
type MuxCloser interface {
	Muxer
	Close() error
}

// Demuxer can read compressed audio/video packets from container formats like MP4/FLV/MPEG-TS.
type Demuxer interface {
	PacketReader                   // read compressed audio/video packets
	Streams() ([]CodecData, error) // reads the file header, contains video/audio meta information
}

// DemuxCloser is a Demuxer with Close() method
type DemuxCloser interface {
	Demuxer
	Close() error
}

// Packet stores compressed audio/video data.
type Packet struct {
	IsKeyFrame      bool          // video packet is key frame
	Idx             int8          // stream index in container format
	CompositionTime time.Duration // packet presentation time minus decode time for H264 B-Frame
	Time            time.Duration // packet decode time
	Duration        time.Duration //packet duration
	Data            []byte        // packet data
}

// AudioFrame represents a raw audio frame.
type AudioFrame struct {
	SampleFormat  SampleFormat  // audio sample format, e.g: S16,FLTP,...
	ChannelLayout ChannelLayout // audio channel layout, e.g: CH_MONO,CH_STEREO,...
	SampleCount   int           // sample count in this frame
	SampleRate    int           // sample rate
	Data          [][]byte      // data array for planar format len(Data) > 1
}

func (af AudioFrame) Duration() time.Duration {
	return time.Second * time.Duration(af.SampleCount) / time.Duration(af.SampleRate)
}

// HasSameFormat checks if an audio frame has same audio format.
func (af AudioFrame) HasSameFormat(other AudioFrame) bool {
	if af.SampleRate != other.SampleRate {
		return false
	}
	if af.ChannelLayout != other.ChannelLayout {
		return false
	}
	if af.SampleFormat != other.SampleFormat {
		return false
	}
	return true
}

// Slice splits sample audio sample from this frame.
func (af AudioFrame) Slice(start int, end int) (out AudioFrame) {
	if start > end {
		panic(fmt.Sprintf("av: AudioFrame split failed start=%d end=%d invalid", start, end))
	}
	out = af
	out.Data = append([][]byte(nil), out.Data...)
	out.SampleCount = end - start
	size := af.SampleFormat.BytesPerSample()
	for i := range out.Data {
		out.Data[i] = out.Data[i][start*size : end*size]
	}
	return
}

// Concat two audio frames.
func (af AudioFrame) Concat(in AudioFrame) (out AudioFrame) {
	out = af
	out.Data = append([][]byte(nil), out.Data...)
	out.SampleCount += in.SampleCount
	for i := range out.Data {
		out.Data[i] = append(out.Data[i], in.Data[i]...)
	}
	return
}

// AudioEncoder can encode raw audio frame into compressed audio packets.
// cgo/ffmpeg implements AudioEncoder, using ffmpeg.NewAudioEncoder to create it.
type AudioEncoder interface {
	CodecData() (AudioCodecData, error)   // encoder's codec data can put into container
	Encode(AudioFrame) ([][]byte, error)  // encode raw audio frame into compressed packet(s)
	Close()                               // close encoder, free cgo contexts
	SetSampleRate(int) error              // set encoder sample rate
	SetChannelLayout(ChannelLayout) error // set encoder channel layout
	SetSampleFormat(SampleFormat) error   // set encoder sample format
	SetBitrate(int) error                 // set encoder bitrate
	SetOption(string, interface{}) error  // encoder setopt, in ffmpeg is av_opt_set_dict()
	GetOption(string, interface{}) error  // encoder getopt
}

// AudioDecoder can decode compressed audio packets into raw audio frame.
// use ffmpeg.NewAudioDecoder to create it.
type AudioDecoder interface {
	Decode([]byte) (bool, AudioFrame, error) // decode one compressed audio packet
	Close()                                  // close decode, free cgo contexts
}

// AudioResampler can convert raw audio frames in different sample rate/format/channel layout.
type AudioResampler interface {
	Resample(AudioFrame) (AudioFrame, error) // convert raw audio frames
}
