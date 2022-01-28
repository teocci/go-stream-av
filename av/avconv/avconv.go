// Package avconv
// Created by RTT.
// Author: teocci@yandex.com on 2021-Oct-27
package avconv

import (
	"fmt"
	"io"
	"time"

	"github.com/teocci/go-stream-av/av"
	"github.com/teocci/go-stream-av/av/avutil"
	"github.com/teocci/go-stream-av/av/pktque"
	"github.com/teocci/go-stream-av/av/transcode"
)

var Debug bool

type Option struct {
	Transcode bool
	Args      []string
}

type Options struct {
	OutputCodecTypes []av.CodecType
}

type Demuxer struct {
	transDemux *transcode.Demuxer
	streams    []av.CodecData
	Options
	Demuxer av.Demuxer
}

func (dm *Demuxer) Close() (err error) {
	if dm.transDemux != nil {
		return dm.transDemux.Close()
	}
	return
}

func (dm *Demuxer) Streams() (streams []av.CodecData, err error) {
	if err = dm.prepare(); err != nil {
		return
	}
	streams = dm.streams
	return
}

func (dm *Demuxer) ReadPacket() (pkt av.Packet, err error) {
	if err = dm.prepare(); err != nil {
		return
	}
	return dm.transDemux.ReadPacket()
}

func (dm *Demuxer) prepare() (err error) {
	if dm.transDemux != nil {
		return
	}

	/*
		var streams []av.CodecData
		if streams, err = dm.Demuxer.Streams(); err != nil {
			return
		}
	*/

	supports := dm.Options.OutputCodecTypes

	transOpts := transcode.Options{}
	transOpts.FindAudioDecoderEncoder = func(codec av.AudioCodecData, i int) (ok bool, dec av.AudioDecoder, enc av.AudioEncoder, err error) {
		if len(supports) == 0 {
			return
		}

		support := false
		for _, typ := range supports {
			if typ == codec.Type() {
				support = true
			}
		}

		if support {
			return
		}
		ok = true

		var enctype av.CodecType
		for _, typ := range supports {
			if typ.IsAudio() {
				if enc, _ = avutil.DefaultHandlers.NewAudioEncoder(typ); enc != nil {
					enctype = typ
					break
				}
			}
		}
		if enc == nil {
			err = fmt.Errorf("avconv: convert %s->%s failed", codec.Type(), enctype)
			return
		}

		// TODO: support per stream option
		// enc.SetSampleRate ...

		if dec, err = avutil.DefaultHandlers.NewAudioDecoder(codec); err != nil {
			err = fmt.Errorf("avconv: decode %s failed", codec.Type())
			return
		}

		return
	}

	dm.transDemux = &transcode.Demuxer{
		Options: transOpts,
		Demuxer: dm.Demuxer,
	}
	if dm.streams, err = dm.transDemux.Streams(); err != nil {
		return
	}

	return
}

func ConvertCmdline(args []string) (err error) {
	output := ""
	input := ""
	flagI := false
	flagV := false
	flagT := false
	flagRE := false
	duration := time.Duration(0)
	options := Options{}

	for _, arg := range args {
		switch arg {
		case "-i":
			flagI = true

		case "-v":
			flagV = true

		case "-t":
			flagT = true

		case "-re":
			flagRE = true

		default:
			switch {
			case flagI:
				flagI = false
				input = arg

			case flagT:
				flagT = false
				var f float64
				_, _ = fmt.Sscanf(arg, "%f", &f)
				duration = time.Duration(f * float64(time.Second))

			default:
				output = arg
			}
		}
	}

	if input == "" {
		err = fmt.Errorf("avconv: input file not specified")
		return
	}

	if output == "" {
		err = fmt.Errorf("avconv: output file not specified")
		return
	}

	var demuxer av.DemuxCloser
	var muxer av.MuxCloser

	if demuxer, err = avutil.Open(input); err != nil {
		return
	}
	defer demuxer.Close()

	var handler avutil.RegisterHandler
	if handler, muxer, err = avutil.DefaultHandlers.FindCreate(output); err != nil {
		return
	}
	defer muxer.Close()

	options.OutputCodecTypes = handler.CodecTypes

	convDemux := &Demuxer{
		Options: options,
		Demuxer: demuxer,
	}
	defer convDemux.Close()

	var streams []av.CodecData
	if streams, err = demuxer.Streams(); err != nil {
		return
	}

	var convStreams []av.CodecData
	if convStreams, err = convDemux.Streams(); err != nil {
		return
	}

	if flagV {
		for _, stream := range streams {
			fmt.Print(stream.Type(), " ")
		}
		fmt.Print("-> ")
		for _, stream := range convStreams {
			fmt.Print(stream.Type(), " ")
		}
		fmt.Println()
	}

	if err = muxer.WriteHeader(convStreams); err != nil {
		return
	}

	filters := pktque.Filters{}
	if flagRE {
		filters = append(filters, &pktque.Walltime{})
	}
	filterDemux := &pktque.FilterDemuxer{
		Demuxer: convDemux,
		Filter:  filters,
	}

	for {
		var pkt av.Packet
		if pkt, err = filterDemux.ReadPacket(); err != nil {
			if err == io.EOF {
				err = nil
				break
			}
			return
		}
		if flagV {
			fmt.Println(pkt.Idx, pkt.Time, len(pkt.Data), pkt.IsKeyFrame)
		}
		if duration != 0 && pkt.Time > duration {
			break
		}
		if err = muxer.WritePacket(pkt); err != nil {
			return
		}
	}

	if err = muxer.WriteTrailer(); err != nil {
		return
	}

	return
}
