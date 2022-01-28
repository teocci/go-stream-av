// Package mp4io
// Created by RTT.
// Author: teocci@yandex.com on 2021-Oct-27
package mp4io

import (
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"

	"github.com/teocci/go-stream-av/utils/bits/pio"
)

type ParseError struct {
	Debug  string
	Offset int
	prev   *ParseError
}

func (pe *ParseError) Error() string {
	var s []string
	for p := pe; p != nil; p = p.prev {
		s = append(s, fmt.Sprintf("%s:%d", p.Debug, p.Offset))
	}
	return "mp4io: parse error: " + strings.Join(s, ",")
}

func parseErr(debug string, offset int, prev error) (err error) {
	_prev, _ := prev.(*ParseError)
	return &ParseError{Debug: debug, Offset: offset, prev: _prev}
}

func GetTime32(b []byte) (t time.Time) {
	sec := pio.U32BE(b)
	t = time.Date(1904, time.January, 1, 0, 0, 0, 0, time.UTC)
	t = t.Add(time.Second * time.Duration(sec))
	return
}

func PutTime32(b []byte, t time.Time) {
	dur := t.Sub(time.Date(1904, time.January, 1, 0, 0, 0, 0, time.UTC))
	sec := uint32(dur / time.Second)
	pio.PutU32BE(b, sec)
}

func GetTime64(b []byte) (t time.Time) {
	sec := pio.U64BE(b)
	t = time.Date(1904, time.January, 1, 0, 0, 0, 0, time.UTC)
	t = t.Add(time.Second * time.Duration(sec))
	return
}

func PutTime64(b []byte, t time.Time) {
	dur := t.Sub(time.Date(1904, time.January, 1, 0, 0, 0, 0, time.UTC))
	sec := uint64(dur / time.Second)
	pio.PutU64BE(b, sec)
}

func PutFixed16(b []byte, f float64) {
	intpart, fracpart := math.Modf(f)
	b[0] = uint8(intpart)
	b[1] = uint8(fracpart * 256.0)
}

func GetFixed16(b []byte) float64 {
	return float64(b[0]) + float64(b[1])/256.0
}

func PutFixed32(b []byte, f float64) {
	intpart, fracpart := math.Modf(f)
	pio.PutU16BE(b[0:2], uint16(intpart))
	pio.PutU16BE(b[2:4], uint16(fracpart*65536.0))
}

func GetFixed32(b []byte) float64 {
	return float64(pio.U16BE(b[0:2])) + float64(pio.U16BE(b[2:4]))/65536.0
}

type Tag uint32

func (t Tag) String() string {
	var b [4]byte
	pio.PutU32BE(b[:], uint32(t))
	for i := 0; i < 4; i++ {
		if b[i] == 0 {
			b[i] = ' '
		}
	}
	return string(b[:])
}

type Atom interface {
	Pos() (int, int)
	Tag() Tag
	Marshal([]byte) int
	Unmarshal([]byte, int) (int, error)
	Len() int
	Children() []Atom
}

type AtomPos struct {
	Offset int
	Size   int
}

func (ap AtomPos) Pos() (int, int) {
	return ap.Offset, ap.Size
}

func (ap *AtomPos) setPos(offset int, size int) {
	ap.Offset, ap.Size = offset, size
}

type Dummy struct {
	Data []byte
	Tag_ Tag
	AtomPos
}

func (d Dummy) Children() []Atom {
	return nil
}

func (d Dummy) Tag() Tag {
	return d.Tag_
}

func (d Dummy) Len() int {
	return len(d.Data)
}

func (d Dummy) Marshal(b []byte) int {
	copy(b, d.Data)
	return len(d.Data)
}

func (d *Dummy) Unmarshal(b []byte, offset int) (n int, err error) {
	(&d.AtomPos).setPos(offset, len(b))
	d.Data = b
	n = len(b)
	return
}

func StringToTag(tag string) Tag {
	var b [4]byte
	copy(b[:], []byte(tag))
	return Tag(pio.U32BE(b[:]))
}

func FindChildrenByName(root Atom, tag string) Atom {
	return FindChildren(root, StringToTag(tag))
}

func FindChildren(root Atom, tag Tag) Atom {
	if root.Tag() == tag {
		return root
	}
	for _, child := range root.Children() {
		if r := FindChildren(child, tag); r != nil {
			return r
		}
	}
	return nil
}

const (
	TFHD_BASE_DATA_OFFSET     = 0x01
	TFHD_STSD_ID              = 0x02
	TFHD_DEFAULT_DURATION     = 0x08
	TFHD_DEFAULT_SIZE         = 0x10
	TFHD_DEFAULT_FLAGS        = 0x20
	TFHD_DURATION_IS_EMPTY    = 0x010000
	TFHD_DEFAULT_BASE_IS_MOOF = 0x020000
)

const (
	TRUN_DATA_OFFSET        = 0x01
	TRUN_FIRST_SAMPLE_FLAGS = 0x04
	TRUN_SAMPLE_DURATION    = 0x100
	TRUN_SAMPLE_SIZE        = 0x200
	TRUN_SAMPLE_FLAGS       = 0x400
	TRUN_SAMPLE_CTS         = 0x800
)

const (
	MP4ESDescrTag          = 3
	MP4DecConfigDescrTag   = 4
	MP4DecSpecificDescrTag = 5
)

type ElemStreamDesc struct {
	DecConfig []byte
	TrackId   uint16
	AtomPos
}

func (esd ElemStreamDesc) Children() []Atom {
	return nil
}

func (esd ElemStreamDesc) fillLength(b []byte, length int) (n int) {
	for i := 3; i > 0; i-- {
		b[n] = uint8(length>>uint(7*i))&0x7f | 0x80
		n++
	}
	b[n] = uint8(length & 0x7f)
	n++
	return
}

func (esd ElemStreamDesc) lenDescHdr() (n int) {
	return 5
}

func (esd ElemStreamDesc) fillDescHdr(b []byte, tag uint8, datalen int) (n int) {
	b[n] = tag
	n++
	n += esd.fillLength(b[n:], datalen)
	return
}

func (esd ElemStreamDesc) lenESDescHdr() (n int) {
	return esd.lenDescHdr() + 3
}

func (esd ElemStreamDesc) fillESDescHdr(b []byte, datalen int) (n int) {
	n += esd.fillDescHdr(b[n:], MP4ESDescrTag, datalen)
	pio.PutU16BE(b[n:], esd.TrackId)
	n += 2
	b[n] = 0 // flags
	n++
	return
}

func (esd ElemStreamDesc) lenDecConfigDescHdr() (n int) {
	return esd.lenDescHdr() + 2 + 3 + 4 + 4 + esd.lenDescHdr()
}

func (esd ElemStreamDesc) fillDecConfigDescHdr(b []byte, datalen int) (n int) {
	n += esd.fillDescHdr(b[n:], MP4DecConfigDescrTag, datalen)
	b[n] = 0x40 // objectid
	n++
	b[n] = 0x15 // streamtype
	n++
	// buffer size db
	pio.PutU24BE(b[n:], 0)
	n += 3
	// max bitrage
	pio.PutU32BE(b[n:], uint32(200000))
	n += 4
	// avg bitrage
	pio.PutU32BE(b[n:], uint32(0))
	n += 4
	n += esd.fillDescHdr(b[n:], MP4DecSpecificDescrTag, datalen-n)
	return
}

func (esd ElemStreamDesc) Len() (n int) {
	return 8 + 4 + esd.lenESDescHdr() + esd.lenDecConfigDescHdr() + len(esd.DecConfig) + esd.lenDescHdr() + 1
}

// Version(4)
// ESDesc(
//   MP4ESDescrTag
//   ESID(2)
//   ESFlags(1)
//   DecConfigDesc(
//     MP4DecConfigDescrTag
//     objectId streamType bufSize avgBitrate
//     DecSpecificDesc(
//       MP4DecSpecificDescrTag
//       decConfig
//     )
//   )
//   ?Desc(lenDescHdr+1)
// )

func (esd ElemStreamDesc) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(ESDS))
	n += 8
	pio.PutU32BE(b[n:], 0) // Version
	n += 4
	datalen := esd.Len()
	n += esd.fillESDescHdr(b[n:], datalen-n-esd.lenESDescHdr())
	n += esd.fillDecConfigDescHdr(b[n:], datalen-n-esd.lenDescHdr()-1)
	copy(b[n:], esd.DecConfig)
	n += len(esd.DecConfig)
	n += esd.fillDescHdr(b[n:], 0x06, datalen-n-esd.lenDescHdr())
	b[n] = 0x02
	n++
	pio.PutU32BE(b[0:], uint32(n))
	return
}

func (esd *ElemStreamDesc) Unmarshal(b []byte, offset int) (n int, err error) {
	if len(b) < n+12 {
		err = parseErr("hdr", offset+n, err)
		return
	}
	(&esd.AtomPos).setPos(offset, len(b))
	n += 8
	n += 4
	return esd.parseDesc(b[n:], offset+n)
}

func (esd *ElemStreamDesc) parseDesc(b []byte, offset int) (n int, err error) {
	var hdrlen int
	var datalen int
	var tag uint8
	if hdrlen, tag, datalen, err = esd.parseDescHdr(b, offset); err != nil {
		return
	}
	n += hdrlen

	if len(b) < n+datalen {
		err = parseErr("datalen", offset+n, err)
		return
	}

	switch tag {
	case MP4ESDescrTag:
		if len(b) < n+3 {
			err = parseErr("MP4ESDescrTag", offset+n, err)
			return
		}
		if _, err = esd.parseDesc(b[n+3:], offset+n+3); err != nil {
			return
		}

	case MP4DecConfigDescrTag:
		const size = 2 + 3 + 4 + 4
		if len(b) < n+size {
			err = parseErr("MP4DecSpecificDescrTag", offset+n, err)
			return
		}
		if _, err = esd.parseDesc(b[n+size:], offset+n+size); err != nil {
			return
		}

	case MP4DecSpecificDescrTag:
		esd.DecConfig = b[n:]
	}

	n += datalen
	return
}

func (esd *ElemStreamDesc) parseLength(b []byte, offset int) (n int, length int, err error) {
	for n < 4 {
		if len(b) < n+1 {
			err = parseErr("len", offset+n, err)
			return
		}
		c := b[n]
		n++
		length = (length << 7) | (int(c) & 0x7f)
		if c&0x80 == 0 {
			break
		}
	}
	return
}

func (esd *ElemStreamDesc) parseDescHdr(b []byte, offset int) (n int, tag uint8, datalen int, err error) {
	if len(b) < n+1 {
		err = parseErr("tag", offset+n, err)
		return
	}
	tag = b[n]
	n++
	var lenlen int
	if lenlen, datalen, err = esd.parseLength(b[n:], offset+n); err != nil {
		return
	}
	n += lenlen
	return
}

func ReadFileAtoms(r io.ReadSeeker) (atoms []Atom, err error) {
	for {
		offset, _ := r.Seek(0, 1)
		taghdr := make([]byte, 8)
		if _, err = io.ReadFull(r, taghdr); err != nil {
			if err == io.EOF {
				err = nil
			}
			return
		}
		size := pio.U32BE(taghdr[0:])
		tag := Tag(pio.U32BE(taghdr[4:]))

		var atom Atom
		switch tag {
		case MOOV:
			atom = &Movie{}
		case MOOF:
			atom = &MovieFrag{}
		}

		if atom != nil {
			b := make([]byte, int(size))
			if _, err = io.ReadFull(r, b[8:]); err != nil {
				return
			}
			copy(b, taghdr)
			if _, err = atom.Unmarshal(b, int(offset)); err != nil {
				return
			}
			atoms = append(atoms, atom)
		} else {
			dummy := &Dummy{Tag_: tag}
			dummy.setPos(int(offset), int(size))
			if _, err = r.Seek(int64(size)-8, 1); err != nil {
				return
			}
			atoms = append(atoms, dummy)
		}
	}
	return
}

func printatom(out io.Writer, root Atom, depth int) {
	offset, size := root.Pos()

	type stringintf interface {
		String() string
	}

	fmt.Fprintf(out,
		"%s%s offset=%d size=%d",
		strings.Repeat(" ", depth*2), root.Tag(), offset, size,
	)
	if str, ok := root.(stringintf); ok {
		fmt.Fprint(out, " ", str.String())
	}
	fmt.Fprintln(out)

	children := root.Children()
	for _, child := range children {
		printatom(out, child, depth+1)
	}
}

func FprintAtom(out io.Writer, root Atom) {
	printatom(out, root, 0)
}

func PrintAtom(root Atom) {
	FprintAtom(os.Stdout, root)
}

func (mh MovieHeader) String() string {
	return fmt.Sprintf("dur=%d", mh.Duration)
}

func (ts TimeToSample) String() string {
	return fmt.Sprintf("entries=%d", len(ts.Entries))
}

func (sc SampleToChunk) String() string {
	return fmt.Sprintf("entries=%d", len(sc.Entries))
}

func (s SampleSize) String() string {
	return fmt.Sprintf("entries=%d", len(s.Entries))
}

func (ss SyncSample) String() string {
	return fmt.Sprintf("entries=%d", len(ss.Entries))
}

func (co CompositionOffset) String() string {
	return fmt.Sprintf("entries=%d", len(co.Entries))
}

func (co ChunkOffset) String() string {
	return fmt.Sprintf("entries=%d", len(co.Entries))
}

func (tfr TrackFragRun) String() string {
	return fmt.Sprintf("dataoffset=%d", tfr.DataOffset)
}

func (tfh TrackFragHeader) String() string {
	return fmt.Sprintf("basedataoffset=%d", tfh.BaseDataOffset)
}

func (esd ElemStreamDesc) String() string {
	return fmt.Sprintf("configlen=%d", len(esd.DecConfig))
}

func (t *Track) GetAVC1Conf() (conf *AVC1Conf) {
	atom := FindChildren(t, AVCC)
	conf, _ = atom.(*AVC1Conf)
	return
}

func (t *Track) GetElemStreamDesc() (esds *ElemStreamDesc) {
	atom := FindChildren(t, ESDS)
	esds, _ = atom.(*ElemStreamDesc)
	return
}
