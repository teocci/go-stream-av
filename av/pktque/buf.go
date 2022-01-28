package pktque

import (
	"github.com/teocci/go-stream-av/av"
)

type Buf struct {
	Head, Tail BufPos
	pkts       []av.Packet
	Size       int
	Count      int
}

func NewBuf() *Buf {
	return &Buf{
		pkts: make([]av.Packet, 64),
	}
}

func (b *Buf) Pop() av.Packet {
	if b.Count == 0 {
		panic("pktque.Buf: Pop() when count == 0")
	}

	i := int(b.Head) & (len(b.pkts) - 1)
	pkt := b.pkts[i]
	b.pkts[i] = av.Packet{}
	b.Size -= len(pkt.Data)
	b.Head++
	b.Count--

	return pkt
}

func (b *Buf) grow() {
	newPackets := make([]av.Packet, len(b.pkts)*2)
	for i := b.Head; i.LT(b.Tail); i++ {
		newPackets[int(i)&(len(newPackets)-1)] = b.pkts[int(i)&(len(b.pkts)-1)]
	}
	b.pkts = newPackets
}

func (b *Buf) Push(pkt av.Packet) {
	if b.Count == len(b.pkts) {
		b.grow()
	}
	b.pkts[int(b.Tail)&(len(b.pkts)-1)] = pkt
	b.Tail++
	b.Count++
	b.Size += len(pkt.Data)
}

func (b *Buf) Get(pos BufPos) av.Packet {
	return b.pkts[int(pos)&(len(b.pkts)-1)]
}

func (b *Buf) IsValidPos(pos BufPos) bool {
	return pos.GE(b.Head) && pos.LT(b.Tail)
}

type BufPos int

func (bp BufPos) LT(pos BufPos) bool {
	return bp-pos < 0
}

func (bp BufPos) GE(pos BufPos) bool {
	return bp-pos >= 0
}

func (bp BufPos) GT(pos BufPos) bool {
	return bp-pos > 0
}
