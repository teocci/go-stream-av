// Package rtspv2
// Created by RTT.
// Author: teocci@yandex.com on 2021-Oct-27
package rtspv2

import (
	"fmt"
	"math/rand"
	"net"
	"net/url"
	"time"

	"github.com/teocci/go-stream-av/av"
)

const (
	StartCodePS        = 0x000001ba
	StartCodeSYS       = 0x000001bb
	StartCodeMAP       = 0x000001bc
	StartCodeVideo     = 0x000001e0
	StartCodeAudio     = 0x000001c0
	MEPGProgramEndCode = 0x000001b9
)

const (
	StreamIDVideo = 0xe0
	StreamIDAudio = 0xc0
)

const (
	UDPTransfer        int = 0
	TCPTransferActive  int = 1
	TCPTransferPassive int = 2
	LocalCache         int = 3
)

//
const (
	StreamTypeH264 = 0x1b
	StreamTypeH265 = 0x24
	StreamTypeAAC  = 0x90
)

type encPSPacket struct {
	crc32 uint64
}

type Conn struct {
	URL      *url.URL
	netConn  net.Conn
	readBuf  []byte
	writeBuf []byte
	playing  bool
	psEnc    *encPSPacket
	cseq     int
	ssrc     uint32
	protocol int
}

type Server struct {
	Addr           string
	HandleDescribe func(*Conn)
	HandleOptions  func(*Conn)
	HandleSetup    func(*Conn)
	HandlePlay     func(*Conn)
	HandleConn     func(*Conn)
}

func NewConn(netConn net.Conn) *Conn {
	conn := &Conn{}
	conn.netConn = netConn
	conn.writeBuf = make([]byte, 4096)
	conn.readBuf = make([]byte, 4096)
	conn.ssrc = rand.Uint32()
	conn.protocol = TCPTransferPassive

	return conn
}

func (c *Conn) Close() (err error) {
	return nil
}

func (c *Conn) WritePacket(pkt *av.Packet) (err error) {
	return nil
}

func (c *Conn) WriteHeader(codec []av.CodecData) (err error) {
	return nil
}

func (c *Conn) NetConn() net.Conn {
	return c.netConn
}

func (s *Server) ListenAndServe() (err error) {
	addr := s.Addr
	if addr == "" {
		addr = ":554"
	}
	var tcpAddr *net.TCPAddr
	if tcpAddr, err = net.ResolveTCPAddr("tcp", addr); err != nil {
		err = fmt.Errorf("rtsp: ListenAndServe: %s", err)
		return
	}

	var listener *net.TCPListener
	if listener, err = net.ListenTCP("tcp", tcpAddr); err != nil {
		return
	}

	if Debug {
		fmt.Println("rtsp: server: listening on", addr)
	}

	for {
		var netConn net.Conn
		if netConn, err = listener.Accept(); err != nil {
			return
		}

		if Debug {
			fmt.Println("rtsp: server: accepted")
		}
		conn := NewConn(netConn)
		go func() {
			err := s.handleConn(conn)
			if Debug {
				fmt.Println("rtsp: server: client closed err:", err)
			}
			//defer conn.Close()
		}()
	}
}

func (s *Server) handleConn(conn *Conn) (err error) {
	return
}

func (c *Conn) prepare() error {
	return nil
}

func timeToTs(tm time.Duration, timeScale int64) int64 {
	return int64(tm * time.Duration(timeScale) / time.Second)
}
