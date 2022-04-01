// Package bits
// Created by RTT.
// Author: teocci@yandex.com on 2021-Oct-27
package bits

import (
	"bytes"
	"github.com/teocci/go-stream-av/utils/bits/pio"
	"testing"
)

func TestBits(t *testing.T) {
	rData := []byte{0xf3, 0xb3, 0x45, 0x60}
	rBuf := bytes.NewReader(rData[:])
	r := &Reader{R: rBuf}
	var u32 uint
	if u32, _ = r.ReadBits(4); u32 != 0xf {
		t.FailNow()
	}
	if u32, _ = r.ReadBits(4); u32 != 0x3 {
		t.FailNow()
	}
	if u32, _ = r.ReadBits(2); u32 != 0x2 {
		t.FailNow()
	}
	if u32, _ = r.ReadBits(2); u32 != 0x3 {
		t.FailNow()
	}
	b := make([]byte, 2)
	if _, _ = r.Read(b); b[0] != 0x34 || b[1] != 0x56 {
		t.FailNow()
	}

	wBuf := &bytes.Buffer{}
	w := &Writer{W: wBuf}
	_ = w.WriteBits(0xf, 4)
	_ = w.WriteBits(0x3, 4)
	_ = w.WriteBits(0x2, 2)
	_ = w.WriteBits(0x3, 2)
	n, _ := w.Write([]byte{0x34, 0x56})
	if n != 2 {
		t.FailNow()
	}
	_ = w.FlushBits()
	wData := wBuf.Bytes()
	if wData[0] != 0xf3 || wData[1] != 0xb3 || wData[2] != 0x45 || wData[3] != 0x60 {
		t.FailNow()
	}

	b = make([]byte, 8)
	pio.PutU32BE(b, 0x11223344)
	if b[0] != 0x11 || b[1] != 0x22 || b[2] != 0x33 || b[3] != 0x44 {
		t.FailNow()
	}
}
