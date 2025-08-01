package server

import (
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"slices"
	"sync"
	"time"

	"github.com/KDT2006/termiq/internal/config"
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
	config          *config.Config
	currentSet      *config.QuestionSet
	currentQuestion int
	endCh           chan struct{}
	wg              sync.WaitGroup
	ln              net.Listener // needed for graceful shutdown
}

func New(listenAddr string, configPath string, questionSet string) *Server {
	protocol.Init() // ensure protocol types are registered with gob

	// Load questions
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	var set *config.QuestionSet
	if questionSet == "" {
		set, err = cfg.GetDefaultSet()
	} else {
		set, err = cfg.GetQuestionSet(questionSet)
	}
	if err != nil {
		log.Fatalf("failed to get question set: %v", err)
	}

	return &Server{
		ListenAddr: listenAddr,
		clients:    make(map[string]*Client),
		config:     cfg,
		currentSet: set,
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
	connDecoder := gob.NewDecoder(conn)
	var msg protocol.Message
	if err := connDecoder.Decode(&msg); err != nil {
		log.Printf("failed to decode message: %v", err)
		conn.Close()
		return
	}

	var playerName string

	if msg.Type == protocol.JoinGame {
		joinMsg, ok := msg.Payload.(protocol.JoinGamePayload)
		if !ok {
			log.Printf("invalid join game payload from %s", conn.RemoteAddr())
			conn.Close()
			return
		}

		playerName = joinMsg.PlayerName
	} else {
		log.Printf("expected join message but got %s from %s", msg.Type, conn.RemoteAddr())
		conn.Close()
		return
	}

	client := &Client{
		conn:       conn,
		outbound:   make(chan protocol.Message, 10),
		encoder:    gob.NewEncoder(conn),
		decoder:    connDecoder,
		playerName: playerName,
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
					if errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) &&
						s.state == protocol.GameStateFinished {
						log.Printf("connection closed by client %s", client.conn.RemoteAddr())
						return
					}

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
					if errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) &&
						s.state == protocol.GameStateFinished {
						log.Printf("connection closed by client %s", client.conn.RemoteAddr())
						return
					}

					log.Printf("error writing to client %s: %v", client.conn.RemoteAddr(), err)
					return
				}
			}
		}
	}
}

func (s *Server) gameLoop() {
	s.state = protocol.GameStateLobby
	log.Println("Waiting for minimum players to start the game...")

	for len(s.clients) < 2 {
		time.Sleep(time.Second)
	}

	log.Println("Minimum players reached, starting the game...")
	s.state = protocol.GameStateQuestion
	s.broadcastGameState()

	for i, q := range s.currentSet.Questions {
		timeLimit := s.currentSet.TimeLimit

		s.currentQuestion = i
		s.broadcast <- protocol.Message{
			Type: protocol.QuestionMessage,
			Payload: protocol.QuestionPayload{
				QuestionID: i,
				Question:   q.Text,
				Choices:    q.Choices,
				TimeLimit:  timeLimit,
			},
		}

		// broadcast timer updates
		go func() {
			for timeLeft := timeLimit; timeLeft > 0; timeLeft-- {
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

		time.Sleep(time.Duration(timeLimit) * time.Second)
	}

	s.state = protocol.GameStateScores
	s.broadcastGameState()

	rankings := s.RankClients()
	s.broadcast <- protocol.Message{
		Type: protocol.LeaderboardMsg,
		Payload: protocol.LeaderboardPayload{
			Rankings: rankings,
		},
	}
	time.Sleep(2 * time.Second) // give clients time to process scores

	fmt.Println("Game finished, waiting for clients to receive final messages...")
	s.state = protocol.GameStateFinished
	s.broadcastGameState()

	time.Sleep(2 * time.Second) // wait for clients to receive the final message
	close(s.endCh)              // signal server shutdown
	s.ln.Close()                // close listener to stop hanging in the accept loop
}

func (s *Server) handleMessage(msg protocol.Message, client *Client) {
	switch msg.Type {
	case protocol.SubmitAnswer:
		fmt.Printf("Received answer submission from client %s\n", client.conn.RemoteAddr())
		if s.state != protocol.GameStateQuestion {
			log.Printf("Received answer from %s but game is not in question state", client.conn.RemoteAddr())
			return
		}

		answerPayload, ok := msg.Payload.(protocol.SubmitAnswerPayload)
		if !ok {
			log.Printf("Invalid payload type for SubmitAnswer from %s", client.conn.RemoteAddr())
			return
		}

		if answerPayload.Answer == s.currentSet.Questions[s.currentQuestion].Answer {
			s.updateScore(client, s.currentSet.Questions[s.currentQuestion].Points)
		}
	default:
		log.Printf("Received unknown message type %s from %s", msg.Type, client.conn.RemoteAddr())
	}
}

func (s *Server) updateScore(client *Client, points int) {
	client.score += points
}

func (s *Server) RankClients() []protocol.PlayerRank {
	rankings := make([]protocol.PlayerRank, 0, len(s.clients))
	sortedClients := make([]*Client, 0, len(s.clients)) // sorted slice of clients by score

	// sort clients by score
	for _, client := range s.clients {
		sortedClients = append(sortedClients, client)
	}
	slices.SortFunc(sortedClients, func(a, b *Client) int {
		if a.score == b.score {
			return 0
		}

		if a.score > b.score {
			return -1
		}

		return 1
	})

	// assign ranks based on sorted order
	for rank, client := range sortedClients {
		rankings = append(rankings, protocol.PlayerRank{
			PlayerName: client.playerName,
			Score:      client.score,
			Rank:       rank + 1,
		})
	}

	return rankings
}

func (s *Server) broadcastGameState() {
	s.broadcast <- protocol.Message{
		Type: protocol.GameStateUpdate,
		Payload: protocol.GameStatePayload{
			State:       s.state,
			PlayerCount: len(s.clients),
		},
	}
}

func (s *Server) closeAllClients() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, client := range s.clients {
		close(client.outbound)
		client.conn.Close()
	}
}
