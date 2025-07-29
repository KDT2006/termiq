package server

import "net"

type Client struct {
	conn     net.Conn
	outbound chan []byte
}
