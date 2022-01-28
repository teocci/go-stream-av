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
	rdata := []byte{0xf3, 0xb3, 0x45, 0x60}
	rbuf := bytes.NewReader(rdata[:])
	r := &Reader{R: rbuf}
	var u32 uint
	if u32, _ = r.ReadBits(4); u32 != 0xf {
		t.Logf("%d\n", u32)
		t.FailNow()
	}
	if u32, _ = r.ReadBits(4); u32 != 0x3 {
		t.Logf("%d\n", u32)
		t.FailNow()
	}
	if u32, _ = r.ReadBits(2); u32 != 0x2 {
		t.Logf("%d\n", u32)
		t.FailNow()
	}
	if u32, _ = r.ReadBits(2); u32 != 0x3 {
		t.Logf("%d\n", u32)
		t.FailNow()
	}
	b := make([]byte, 2)
	if _, _ = r.Read(b); b[0] != 0x34 || b[1] != 0x56 {
		t.FailNow()
	}

	wbuf := &bytes.Buffer{}
	w := &Writer{W: wbuf}
	_ = w.WriteBits(0xf, 4)
	_ = w.WriteBits(0x3, 4)
	_ = w.WriteBits(0x2, 2)
	_ = w.WriteBits(0x3, 2)
	n, _ := w.Write([]byte{0x34, 0x56})
	if n != 2 {
		t.FailNow()
	}
	_ = w.FlushBits()
	wdata := wbuf.Bytes()
	if wdata[0] != 0xf3 || wdata[1] != 0xb3 || wdata[2] != 0x45 || wdata[3] != 0x60 {
		t.FailNow()
	}

	b = make([]byte, 8)
	pio.PutU32BE(b, 0x11223344)
	if b[0] != 0x11 || b[1] != 0x22 || b[2] != 0x33 || b[3] != 0x44 {
		t.FailNow()
	}
}
