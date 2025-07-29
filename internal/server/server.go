package server

import (
	"errors"
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
	broadcast       chan Message
	state           GameState
	questions       []string
	choices         [][]string
	answers         []string
	currentQuestion int
	endCh           chan struct{}
	wg              sync.WaitGroup
	ln              net.Listener // needed for graceful shutdown
}

func New(listenAddr string) *Server {
	return &Server{
		ListenAddr: listenAddr,
		clients:    make(map[string]*Client),
		broadcast:  make(chan Message, 10),
		state:      GameStateLobby,
		endCh:      make(chan struct{}),
	}
}

func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.ListenAddr)
	if err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}
	s.ln = ln
	defer ln.Close()

	go s.handleBroadcasts()
	go s.gameLoop()

	log.Printf("Server is listening on %s", s.ListenAddr)

	for {
		select {
		case <-s.endCh:
			log.Println("Server shutting down...")
			s.closeAllClients()
			s.wg.Wait()
			return nil
		default:
			{
				conn, err := ln.Accept()
				if err != nil {
					if errors.Is(err, net.ErrClosed) {
						log.Println("Listener closed")
						return nil
					}

					log.Printf("failed to accept connection: %v", err)
					continue
				}

				log.Printf("accepted connection from %s", conn.RemoteAddr())
				go s.HandleConn(conn)
			}
		}
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
	s.wg.Add(1)
	defer s.wg.Done()

	for {
		select {
		case <-s.endCh:
			log.Println("Broadcast handler shutting down")
			return
		case msg := <-s.broadcast:
			{
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
	s.wg.Add(1)
	defer s.wg.Done()

	for {
		select {
		case <-s.endCh:
			log.Printf("read loop for client %s shutting down", client.conn.RemoteAddr())
			return
		default:
			{
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
	}
}

func (s *Server) writeLoop(client *Client) {
	s.wg.Add(1)
	defer s.wg.Done()

	for {
		select {
		case <-s.endCh:
			log.Printf("write loop for client %s shutting down", client.conn.RemoteAddr())
			return
		case msg, ok := <-client.outbound:
			{
				if !ok {
					return
				}
				_, err := client.conn.Write(msg)
				if err != nil {
					log.Printf("error writing to client %s: %v", client.conn.RemoteAddr(), err)
					return
				}
			}
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
	s.choices = [][]string{
		{"Paris", "London", "Berlin", "Madrid"},
		{"3", "4", "5", "6"},
		{"Harper Lee", "Mark Twain", "Ernest Hemingway", "F. Scott Fitzgerald"},
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
		userPrompt := fmt.Sprintf("Question %d: %s\nChoices: %v\n", i+1, question, s.choices[i])
		s.broadcast <- Message{
			Client:  nil, // broadcast to all clients
			Type:    MessageTypeChat,
			Payload: []byte(userPrompt),
		}

		time.Sleep(5 * time.Second)

		s.broadcast <- Message{
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
	s.broadcast <- Message{
		Client:  nil,
		Type:    MessageTypeLeaderboard,
		Payload: []byte(leaderboardString),
	}

	fmt.Println("Game finished, waiting for clients to receive final messages...")
	time.Sleep(5 * time.Second) // wait for clients to receive the final message
	close(s.endCh)              // signal server shutdown
	s.ln.Close()                // close listener to stop hanging in the accept loop
}

func (s *Server) handleMessage(msg Message) {
	switch msg.Type {
	case MessageTypeAnswer:
		if s.state != GameStateQuestion {
			log.Printf("Received answer from %s but game is not in question state", msg.Client.conn.RemoteAddr())
			return
		}

		if string(msg.Payload) == s.answers[s.currentQuestion] {
			s.updateScore(msg.Client, 10) // static points for now
		}
	default:
		log.Printf("Received unknown message type %d from %s", msg.Type, msg.Client.conn.RemoteAddr())
	}
}

func (s *Server) updateScore(client *Client, points int) {
	client.score += points
}

func (s *Server) closeAllClients() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, client := range s.clients {
		close(client.outbound)
		client.conn.Close()
	}
}
