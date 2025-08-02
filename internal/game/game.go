package game

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

// Game represents a single game instance. It manages clients, game state, and communication.
type Game struct {
	ListenAddr      string
	HostName        string // Name of the host client(TODO: use UUID instead)
	HostAddr        string
	clients         map[string]*Client
	mu              sync.Mutex
	broadcast       chan protocol.Message
	state           protocol.GameState
	config          *config.Config
	currentSet      *config.QuestionSet
	currentQuestion int
	startCh         chan struct{}
	endCh           chan struct{}
	wg              sync.WaitGroup
	ln              net.Listener // needed for graceful shutdown
}

func New(listenAddr string, configPath, questionSet, hostName string) *Game {
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

	return &Game{
		ListenAddr: listenAddr,
		HostName:   hostName,
		clients:    make(map[string]*Client),
		config:     cfg,
		currentSet: set,
		broadcast:  make(chan protocol.Message, 10),
		state:      protocol.GameStateLobby,
		startCh:    make(chan struct{}),
		endCh:      make(chan struct{}),
	}
}

func (s *Game) Start() error {
	ln, err := net.Listen("tcp", s.ListenAddr)
	if err != nil {
		return fmt.Errorf("failed to start Game: %w", err)
	}
	s.ln = ln
	defer ln.Close()

	go s.handleBroadcasts()
	go s.gameLoop()

	log.Printf("Game is listening on %s", s.ListenAddr)

	for {
		select {
		case <-s.endCh:
			log.Println("Game shutting down...")
			s.closeAllClients()
			s.wg.Wait()
			log.Printf("Game %s shutdown complete", s.ListenAddr)
			return nil
		default:
			{
				Conn, err := ln.Accept()
				if err != nil {
					if errors.Is(err, net.ErrClosed) {
						log.Println("Listener closed")
						continue
					}

					log.Printf("failed to accept Connection: %v", err)
					continue
				}

				log.Printf("accepted Connection from %s", Conn.RemoteAddr())
				go s.HandleConn(Conn)
			}
		}
	}
}

func (s *Game) HandleConn(Conn net.Conn) {
	ConnDecoder := gob.NewDecoder(Conn)
	var msg protocol.Message
	if err := ConnDecoder.Decode(&msg); err != nil {
		log.Printf("failed to decode message: %v", err)
		Conn.Close()
		return
	}

	var playerName string

	if msg.Type == protocol.JoinGame {
		joinMsg, ok := msg.Payload.(protocol.JoinGamePayload)
		if !ok {
			log.Printf("invalid join game payload from %s", Conn.RemoteAddr())
			Conn.Close()
			return
		}

		playerName = joinMsg.PlayerName
	} else {
		log.Printf("expected join message but got %s from %s", msg.Type, Conn.RemoteAddr())
		Conn.Close()
		return
	}

	client := &Client{
		Conn:       Conn,
		Host:       playerName == s.HostName,
		Outbound:   make(chan protocol.Message, 10),
		Encoder:    gob.NewEncoder(Conn),
		Decoder:    ConnDecoder,
		PlayerName: playerName,
	}

	// Check if client is the host
	if playerName == s.HostName {
		log.Printf("Host client connected: %s", Conn.RemoteAddr())
		s.HostAddr = Conn.RemoteAddr().String()
	} else {
		log.Printf("Regular client connected: %s", Conn.RemoteAddr())

		// unicast to host to notify about new client
		go func() {
			hostClient, ok := s.clients[s.HostAddr]
			if !ok {
				log.Printf("Host client yet to connect, cannot notify about new client %s", Conn.RemoteAddr())
				Conn.Close()
				return
			}

			s.mu.Lock()
			defer s.mu.Unlock()
			hostClient.Outbound <- protocol.Message{
				Type: protocol.NewClientMsg,
				Payload: protocol.NewClientPayload{
					PlayerName:  playerName,
					PlayerCount: len(s.clients),
				},
			}
		}()
	}

	s.registerClient(client)
	defer s.unregisterClient(client)

	go s.writeLoop(client)
	s.readLoop(client)
}

func (s *Game) handleBroadcasts() {
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
					case client.Outbound <- msg:
					default:
						// client too slow to receive, unregister
						log.Printf("client %s too slow to receive message, unregistering", client.Conn.RemoteAddr())
						toUnregister[client] = struct{}{}
					}
				}
				s.mu.Unlock()

				for client := range toUnregister {
					s.unregisterClient(client)
					log.Printf("unregistered client %s due to write failure", client.Conn.RemoteAddr())
				}
			}
		}
	}

}

func (s *Game) registerClient(client *Client) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.clients[client.Conn.RemoteAddr().String()] = client
}

func (s *Game) unregisterClient(client *Client) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.clients[client.Conn.RemoteAddr().String()]; ok {
		close(client.Outbound)
		delete(s.clients, client.Conn.RemoteAddr().String())
		client.Conn.Close()
		log.Printf("unregistered client %s", client.Conn.RemoteAddr())
	}
}

func (s *Game) readLoop(client *Client) {
	s.wg.Add(1)
	defer s.wg.Done()

	for {
		select {
		case <-s.endCh:
			log.Printf("read loop for client %s shutting down", client.Conn.RemoteAddr())
			return
		default:
			{
				var msg protocol.Message
				err := client.Decoder.Decode(&msg)
				if err != nil {
					if errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) &&
						s.state == protocol.GameStateFinished {
						log.Printf("Connection closed by client %s", client.Conn.RemoteAddr())
						return
					}

					log.Printf("error decoding message from client %s: %v", client.Conn.RemoteAddr(), err)
					return
				}

				go s.handleMessage(msg, client)
			}
		}
	}
}

func (s *Game) writeLoop(client *Client) {
	s.wg.Add(1)
	defer s.wg.Done()

	for {
		select {
		case <-s.endCh:
			log.Printf("write loop for client %s shutting down", client.Conn.RemoteAddr())
			return
		case msg, ok := <-client.Outbound:
			{
				if !ok {
					return
				}
				err := client.Encoder.Encode(msg)
				if err != nil {
					if errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) &&
						s.state == protocol.GameStateFinished {
						log.Printf("Connection closed by client %s", client.Conn.RemoteAddr())
						return
					}

					log.Printf("error writing to client %s: %v", client.Conn.RemoteAddr(), err)
					return
				}
			}
		}
	}
}

func (s *Game) gameLoop() {
	<-s.startCh

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
	close(s.endCh)              // signal Game shutdown
	s.ln.Close()                // close listener to stop hanging in the accept loop
}

func (s *Game) handleMessage(msg protocol.Message, client *Client) {
	switch msg.Type {
	case protocol.SubmitAnswer:
		log.Printf("Received answer submission from client %s\n", client.Conn.RemoteAddr())
		if s.state != protocol.GameStateQuestion {
			log.Printf("Received answer from %s but game is not in question state", client.Conn.RemoteAddr())
			return
		}

		answerPayload, ok := msg.Payload.(protocol.SubmitAnswerPayload)
		if !ok {
			log.Printf("Invalid payload type for SubmitAnswer from %s", client.Conn.RemoteAddr())
			return
		}

		var correct bool
		if answerPayload.Answer == s.currentSet.Questions[s.currentQuestion].Answer {
			s.updateScore(client, s.currentSet.Questions[s.currentQuestion].Points)
			correct = true
		}

		// unicast score update to client
		client.Outbound <- protocol.Message{
			Type: protocol.ScoreUpdate,
			Payload: protocol.ScorePayload{
				PlayerName: client.PlayerName,
				Score:      client.Score,
				Correct:    correct,
			},
		}
	case protocol.StartGame:
		if !client.Host {
			log.Printf("Received StartGame from non-host client %s", client.Conn.RemoteAddr())
			return
		}

		s.startCh <- struct{}{}
	default:
		log.Printf("Received unknown message type %s from %s", msg.Type, client.Conn.RemoteAddr())
	}
}

func (s *Game) updateScore(client *Client, points int) {
	client.Score += points
}

func (s *Game) RankClients() []protocol.PlayerRank {
	rankings := make([]protocol.PlayerRank, 0, len(s.clients))
	sortedClients := make([]*Client, 0, len(s.clients)) // sorted slice of clients by score

	// sort clients by score
	for _, client := range s.clients {
		sortedClients = append(sortedClients, client)
	}
	slices.SortFunc(sortedClients, func(a, b *Client) int {
		if a.Score == b.Score {
			return 0
		}

		if a.Score > b.Score {
			return -1
		}

		return 1
	})

	// assign ranks based on sorted order
	for rank, client := range sortedClients {
		rankings = append(rankings, protocol.PlayerRank{
			PlayerName: client.PlayerName,
			Score:      client.Score,
			Rank:       rank + 1,
		})
	}

	return rankings
}

func (s *Game) broadcastGameState() {
	s.broadcast <- protocol.Message{
		Type: protocol.GameStateUpdate,
		Payload: protocol.GameStatePayload{
			State:       s.state,
			PlayerCount: len(s.clients),
		},
	}
}

func (s *Game) closeAllClients() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, client := range s.clients {
		close(client.Outbound)
		client.Conn.Close()
	}
}

func (s *Game) GetState() protocol.GameState {
	return s.state
}
