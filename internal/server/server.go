package server

import (
	"fmt"
	"log"
	"net"
	"sync"
)

type Server struct {
	ListenAddr string
	clients    map[string]*Client
	mu         sync.Mutex
	broadcast  chan []byte
}

func New(listenAddr string) *Server {
	return &Server{
		ListenAddr: listenAddr,
		clients:    make(map[string]*Client),
		broadcast:  make(chan []byte, 10),
	}
}

func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.ListenAddr)
	if err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}
	defer ln.Close()

	go s.handleBroadcasts()

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
	client := &Client{
		conn:     conn,
		outbound: make(chan []byte, 10),
	}

	s.registerClient(client)
	defer s.unregisterClient(client)

	go s.writeLoop(client)
	s.readLoop(client)
}

func (s *Server) handleBroadcasts() {

	for {
		msg := <-s.broadcast
		toUnregister := make(map[*Client]struct{})
		s.mu.Lock()
		for _, client := range s.clients {
			select {
			case client.outbound <- msg:
			default:
				// client too slow to receive, unregister
				toUnregister[client] = struct{}{}
			}
		}
		s.mu.Unlock()

		for client := range toUnregister {
			s.unregisterClient(client)
			log.Printf("unregistered client %s due to write failure", client.conn.RemoteAddr())
		}
	}

}

func (s *Server) registerClient(client *Client) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.clients[client.conn.RemoteAddr().String()] = client
}

func (s *Server) unregisterClient(client *Client) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.clients[client.conn.RemoteAddr().String()]; ok {
		close(client.outbound)
		delete(s.clients, client.conn.RemoteAddr().String())
		client.conn.Close()
		log.Printf("unregistered client %s", client.conn.RemoteAddr())
	}
}

func (s *Server) readLoop(client *Client) {
	for {
		buf := make([]byte, 1024)
		n, err := client.conn.Read(buf)
		if err != nil {
			log.Printf("error reading from client %s: %v", client.conn.RemoteAddr(), err)
			return
		}

		s.broadcast <- buf[:n]
	}
}

func (s *Server) writeLoop(client *Client) {
	for msg := range client.outbound {
		_, err := client.conn.Write(msg)
		if err != nil {
			log.Printf("error writing to client %s: %v", client.conn.RemoteAddr(), err)
			return
		}
	}
}
