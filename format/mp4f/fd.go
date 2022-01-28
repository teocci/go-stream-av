// Package mp4f
// Created by RTT.
// Author: teocci@yandex.com on 2021-Oct-27
package mp4f

import "github.com/teocci/go-stream-av/format/mp4/mp4io"

type FDummy struct {
	Data []byte
	Tag_ mp4io.Tag
	mp4io.AtomPos
}

func (self FDummy) Children() []mp4io.Atom {
	return nil
}

func (self FDummy) Tag() mp4io.Tag {
	return self.Tag_
}

func (self FDummy) Len() int {
	return len(self.Data)
}

func (self FDummy) Marshal(b []byte) int {
	copy(b, self.Data)
	return len(self.Data)
}

func (self FDummy) Unmarshal(b []byte, offset int) (n int, err error) {
	return
}
