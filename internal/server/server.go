package server

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

type GameState byte

const (
	GameStateLobby GameState = iota
	GameStateQuestion
	GameStateFinished
)

type Server struct {
	ListenAddr      string
	clients         map[string]*Client
	mu              sync.Mutex
	broadcast       chan OutboundMessage
	state           GameState
	questions       []string
	answers         []string
	currentQuestion int
}

func New(listenAddr string) *Server {
	return &Server{
		ListenAddr: listenAddr,
		clients:    make(map[string]*Client),
		broadcast:  make(chan OutboundMessage, 10),
	}
}

func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.ListenAddr)
	if err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}
	defer ln.Close()

	go s.handleBroadcasts()
	go s.gameLoop()

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

		if msg.Client == nil {
			// broadcast to all clients
			toUnregister := make(map[*Client]struct{})
			s.mu.Lock()
			for _, client := range s.clients {
				select {
				case client.outbound <- msg.Payload:
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
		} else {
			msg.Client.outbound <- msg.Payload
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

		msg := Message{
			Client:  client,
			Type:    MessageTypeAnswer, // assumoing all messages are answers
			Payload: buf[:n],
		}
		go s.handleMessage(msg)
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

func (s *Server) gameLoop() {
	// TODO: read from config
	s.questions = []string{
		"What is the capital of France?",
		"What is 2 + 2?",
		"Who wrote 'To Kill a Mockingbird'?",
	}
	s.answers = []string{"Paris", "4", "Harper Lee"}

	s.state = GameStateLobby
	log.Println("Waiting for minimum players to start the game...")

	for len(s.clients) < 2 {
		time.Sleep(time.Second)
	}

	log.Println("Minimum players reached, starting the game...")
	s.state = GameStateQuestion

	for i, question := range s.questions {
		s.currentQuestion = i
		s.broadcast <- OutboundMessage{
			Client:  nil, // broadcast to all clients
			Type:    MessageTypeChat,
			Payload: []byte(fmt.Sprintf("Question %d: %s", i+1, question)),
		}

		time.Sleep(5 * time.Second)

		s.broadcast <- OutboundMessage{
			Client:  nil,
			Type:    MessageTypeChat,
			Payload: []byte(fmt.Sprintf("Answer %d: %s", i+1, s.answers[i])),
		}
	}

	s.state = GameStateFinished
	leaderboardString := "Leaderboard:"
	for _, client := range s.clients {
		leaderboardString += fmt.Sprintf("\n%s: %d points", client.conn.RemoteAddr(), client.score)
	}
	s.broadcast <- OutboundMessage{
		Client:  nil,
		Type:    MessageTypeLeaderboard,
		Payload: []byte(leaderboardString),
	}
}

func (s *Server) handleMessage(msg Message) {
	switch msg.Type {
	case MessageTypeAnswer:
		if s.state != GameStateQuestion {
			log.Printf("Received answer from %s but game is not in question state", msg.Client.conn.RemoteAddr())
			return
		}

		if string(msg.Payload) == s.answers[s.currentQuestion] {
			s.broadcast <- OutboundMessage{
				Client:  msg.Client,
				Type:    MessageTypeChat,
				Payload: []byte("You're correct!"),
			}
			s.updateScore(msg.Client, 10) // static points for now
		} else {
			s.broadcast <- OutboundMessage{
				Client:  msg.Client,
				Type:    MessageTypeChat,
				Payload: []byte("Wrong answer!"),
			}
		}
	default:
		log.Printf("Received unknown message type %d from %s", msg.Type, msg.Client.conn.RemoteAddr())
	}
}

func (s *Server) updateScore(client *Client, points int) {
	client.score += points
}
