package server

import (
	"encoding/gob"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/KDT2006/termiq/internal/protocol"
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
	broadcast       chan protocol.Message
	state           protocol.GameState
	questions       []string
	choices         [][]string
	answers         []int // indices of correct answers
	currentQuestion int
	endCh           chan struct{}
	wg              sync.WaitGroup
	ln              net.Listener // needed for graceful shutdown
}

func New(listenAddr string) *Server {
	protocol.Init() // ensure protocol types are registered with gob

	return &Server{
		ListenAddr: listenAddr,
		clients:    make(map[string]*Client),
		broadcast:  make(chan protocol.Message, 10),
		state:      protocol.GameStateLobby,
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
		outbound: make(chan protocol.Message, 10),
		encoder:  gob.NewEncoder(conn),
		decoder:  gob.NewDecoder(conn),
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
				// broadcast to all clients
				toUnregister := make(map[*Client]struct{})
				s.mu.Lock()
				for _, client := range s.clients {
					select {
					case client.outbound <- msg:
					default:
						// client too slow to receive, unregister
						log.Printf("client %s too slow to receive message, unregistering", client.conn.RemoteAddr())
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
				var msg protocol.Message
				err := client.decoder.Decode(&msg)
				if err != nil {
					log.Printf("error decoding message from client %s: %v", client.conn.RemoteAddr(), err)
					return
				}

				go s.handleMessage(msg, client)
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
				err := client.encoder.Encode(msg)
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
	s.answers = []int{0, 1, 0}

	s.state = protocol.GameStateLobby
	log.Println("Waiting for minimum players to start the game...")

	for len(s.clients) < 2 {
		time.Sleep(time.Second)
	}

	log.Println("Minimum players reached, starting the game...")
	s.state = protocol.GameStateQuestion
	s.broadcast <- protocol.Message{
		Type: protocol.GameStateUpdate,
		Payload: protocol.GameStatePayload{
			State:       s.state,
			PlayerCount: len(s.clients),
		},
	}

	for i, question := range s.questions {
		s.currentQuestion = i
		s.broadcast <- protocol.Message{
			Type: protocol.QuestionMessage,
			Payload: protocol.QuestionPayload{
				QuestionID: i,
				Question:   question,
				Choices:    s.choices[i],
				TimeLimit:  5, // static time limit for now
			},
		}

		// broadcast timer updates
		go func() {
			for timeLeft := 5; timeLeft > 0; timeLeft-- {
				s.broadcast <- protocol.Message{
					Type: protocol.TimerUpdate,
					Payload: protocol.TimerPayload{
						QuestionID: s.currentQuestion,
						TimeLeft:   timeLeft,
					},
				}
				time.Sleep(time.Second)
			}
		}()

		time.Sleep(5 * time.Second)
	}

	s.state = protocol.GameStateFinished

	leaderboardString := "Leaderboard:"
	for _, client := range s.clients {
		leaderboardString += fmt.Sprintf("\n%s: %d points", client.conn.RemoteAddr(), client.score)
	}

	rankings := make([]protocol.PlayerRank, 0, len(s.clients))
	for id, client := range s.clients {
		rankings = append(rankings, protocol.PlayerRank{
			PlayerName: id,
			Score:      client.score,
			Rank:       0, // TODO: calculate rank based on score
		})
	}
	s.broadcast <- protocol.Message{
		Type: protocol.LeaderboardMsg,
		Payload: protocol.LeaderboardPayload{
			Rankings: rankings,
		},
	}

	fmt.Println("Game finished, waiting for clients to receive final messages...")
	time.Sleep(5 * time.Second) // wait for clients to receive the final message
	close(s.endCh)              // signal server shutdown
	s.ln.Close()                // close listener to stop hanging in the accept loop
}

func (s *Server) handleMessage(msg protocol.Message, client *Client) {
	switch msg.Type {
	case protocol.SubmitAnswer:
		fmt.Println("Received answer submission")
		if s.state != protocol.GameStateQuestion {
			log.Printf("Received answer from %s but game is not in question state", client.conn.RemoteAddr())
			return
		}

		answerPayload, ok := msg.Payload.(protocol.SubmitAnswerPayload)
		if !ok {
			log.Printf("Invalid payload type for SubmitAnswer from %s", client.conn.RemoteAddr())
			return
		}

		if answerPayload.Answer == s.answers[s.currentQuestion] {
			s.updateScore(client, 10) // static points for now
		}
	default:
		log.Printf("Received unknown message type %s from %s", msg.Type, client.conn.RemoteAddr())
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
