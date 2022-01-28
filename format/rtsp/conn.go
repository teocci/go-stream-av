// Package rtsp
// Created by RTT.
// Author: teocci@yandex.com on 2021-Oct-27
package rtsp

import (
	"net"
	"time"
)

type connWithTimeout struct {
	Timeout time.Duration
	net.Conn
}

func (c connWithTimeout) Read(p []byte) (n int, err error) {
	if c.Timeout > 0 {
		_ = c.Conn.SetReadDeadline(time.Now().Add(c.Timeout))
	}
	return c.Conn.Read(p)
}

func (c connWithTimeout) Write(p []byte) (n int, err error) {
	if c.Timeout > 0 {
		_ = c.Conn.SetWriteDeadline(time.Now().Add(c.Timeout))
	}
	return c.Conn.Write(p)
}
