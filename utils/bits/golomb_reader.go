// Package bits
// Created by RTT.
// Author: teocci@yandex.com on 2021-Oct-27
package bits

import (
	"io"
)

type GolombBitReader struct {
	R    io.Reader
	buf  [1]byte
	left byte
}

func (gbr *GolombBitReader) ReadBit() (res uint, err error) {
	if gbr.left == 0 {
		if _, err = gbr.R.Read(gbr.buf[:]); err != nil {
			return
		}
		gbr.left = 8
	}
	gbr.left--
	res = uint(gbr.buf[0]>>gbr.left) & 1
	return
}

func (gbr *GolombBitReader) ReadBits(n int) (res uint, err error) {
	for i := 0; i < n; i++ {
		var bit uint
		if bit, err = gbr.ReadBit(); err != nil {
			return
		}
		res |= bit << uint(n-i-1)
	}
	return
}

func (gbr *GolombBitReader) ReadBits32(n uint) (r uint32, err error) {
	var t uint
	for i := uint(0); i < n; i++ {
		t, err = gbr.ReadBit()
		if err != nil {
			return
		}
		r = (r << 1) | uint32(t)
	}
	return
}

func (gbr *GolombBitReader) ReadBits64(n uint) (r uint64, err error) {
	var t uint
	for i := uint(0); i < n; i++ {
		t, err = gbr.ReadBit()
		if err != nil {
			return
		}
		r = (r << 1) | uint64(t)
	}
	return
}

func (gbr *GolombBitReader) ReadExponentialGolombCode() (res uint, err error) {
	i := 0
	for {
		var bit uint
		if bit, err = gbr.ReadBit(); err != nil {
			return
		}
		if !(bit == 0 && i < 32) {
			break
		}
		i++
	}
	if res, err = gbr.ReadBits(i); err != nil {
		return
	}
	res += (1 << uint(i)) - 1
	return
}

func (gbr *GolombBitReader) ReadSE() (res uint, err error) {
	if res, err = gbr.ReadExponentialGolombCode(); err != nil {
		return
	}
	if res&0x01 != 0 {
		res = (res + 1) / 2
	} else {
		res = -res / 2
	}
	return
}
