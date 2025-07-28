package server

import (
	"fmt"
	"log"
	"net"
)

type Server struct {
	ListenAddr string
}

func New(listenAddr string) *Server {
	return &Server{
		ListenAddr: listenAddr,
	}
}

func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.ListenAddr)
	if err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}
	defer ln.Close()

	log.Printf("Server is listening on %s", s.ListenAddr)

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("failed to accept connection: %v", err)
			continue
		}

		log.Printf("accepted connection from %s", conn.RemoteAddr())
		go s.HandleConn(conn)
	}
}

func (s *Server) HandleConn(conn net.Conn) {
	defer conn.Close()

	for {
		buf := make([]byte, 1024)
		n, err := conn.Read(buf)
		if err != nil {
			log.Printf("error reading from conn: %v", err)
			return
		}

		if n == 0 {
			log.Printf("EOF from %s", conn.RemoteAddr())
			return
		}

		log.Printf("received %d bytes from %s: %s", n, conn.RemoteAddr(), string(buf[:n]))
		_, err = conn.Write(buf[:n]) // Echo back
		if err != nil {
			return
		}
	}
}
