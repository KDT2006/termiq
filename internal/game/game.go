package game

import (
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"slices"
	"sync"
	"time"

	"github.com/KDT2006/termiq/internal/config"
	"github.com/KDT2006/termiq/internal/protocol"
	"github.com/google/uuid"
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
	HostID          uuid.UUID // ID of the host client
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

func New(listenAddr string, configPath, questionSet string, hostClientID uuid.UUID) *Game {
	protocol.Init() // ensure protocol types are registered with gob

	// Load questions
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
	}

	var set *config.QuestionSet
	if questionSet == "" {
		set, err = cfg.GetDefaultSet()
	} else {
		set, err = cfg.GetQuestionSet(questionSet)
	}
	if err != nil {
		slog.Error("failed to get question set", "error", err)
	}

	return &Game{
		ListenAddr: listenAddr,
		HostID:     hostClientID,
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

	slog.Info("Game is listening", "address", s.ListenAddr)

	for {
		select {
		case <-s.endCh:
			slog.Info("Game shutting down", "address", s.ListenAddr)
			s.closeAllClients()
			s.wg.Wait()
			slog.Info("Game shutdown complete", "address", s.ListenAddr)
			return nil
		default:
			{
				Conn, err := ln.Accept()
				if err != nil {
					if errors.Is(err, net.ErrClosed) {
						slog.Info("Listener closed", "address", s.ListenAddr)
						continue
					}

					slog.Error("failed to accept Connection", "address", s.ListenAddr, "error", err)
					continue
				}

				slog.Info("accepted Connection", "address", s.ListenAddr, "remote", Conn.RemoteAddr())
				go s.HandleConn(Conn)
			}
		}
	}
}

func (s *Game) HandleConn(Conn net.Conn) {
	ConnDecoder := gob.NewDecoder(Conn)
	var msg protocol.Message
	if err := ConnDecoder.Decode(&msg); err != nil {
		slog.Error("failed to decode message", "address", s.ListenAddr, "error", err)
		Conn.Close()
		return
	}

	var playerName string
	var playerID uuid.UUID

	if msg.Type == protocol.JoinGame {
		joinMsg, ok := msg.Payload.(protocol.JoinGamePayload)
		if !ok {
			slog.Error("invalid join game payload", "address", s.ListenAddr, "remote", Conn.RemoteAddr())
			Conn.Close()
			return
		}

		playerName = joinMsg.PlayerName
		playerID = joinMsg.PlayerID
	} else {
		slog.Error("expected join message but got", "type", msg.Type, "remote", Conn.RemoteAddr())
		Conn.Close()
		return
	}

	client := &Client{
		Conn:       Conn,
		Host:       playerID == s.HostID,
		Outbound:   make(chan protocol.Message, 10),
		Encoder:    gob.NewEncoder(Conn),
		Decoder:    ConnDecoder,
		PlayerName: playerName,
		PlayerID:   playerID,
	}

	// Check if client is the host
	if playerID == s.HostID {
		slog.Info("Host client connected", "address", s.ListenAddr, "remote", Conn.RemoteAddr())
		s.HostAddr = Conn.RemoteAddr().String()
	} else {
		slog.Info("Regular client connected", "address", s.ListenAddr, "remote", Conn.RemoteAddr())

		// unicast to host to notify about new client
		go func() {
			hostClient, ok := s.clients[s.HostAddr]
			if !ok {
				slog.Error("Host client yet to connect, cannot notify about new client", "address", s.ListenAddr, "remote", Conn.RemoteAddr())
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
			slog.Info("Broadcast handler shutting down", "address", s.ListenAddr)
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
						slog.Error("client too slow to receive message, unregistering", "address", s.ListenAddr, "remote", client.Conn.RemoteAddr())
						toUnregister[client] = struct{}{}
					}
				}
				s.mu.Unlock()

				for client := range toUnregister {
					s.unregisterClient(client)
					slog.Error("unregistered client due to write failure", "address", s.ListenAddr, "remote", client.Conn.RemoteAddr())
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
		slog.Info("unregistered client", "address", s.ListenAddr, "remote", client.Conn.RemoteAddr())
	}
}

func (s *Game) readLoop(client *Client) {
	s.wg.Add(1)
	defer s.wg.Done()

	for {
		select {
		case <-s.endCh:
			slog.Info("read loop shutting down", "address", s.ListenAddr, "remote", client.Conn.RemoteAddr())
			return
		default:
			{
				var msg protocol.Message
				err := client.Decoder.Decode(&msg)
				if err != nil {
					if errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) &&
						s.state == protocol.GameStateFinished {
						slog.Info("Connection closed by client", "address", s.ListenAddr, "remote", client.Conn.RemoteAddr())
						return
					}

					slog.Error("error decoding message from client", "address", s.ListenAddr, "remote", client.Conn.RemoteAddr(), "error", err)
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
			slog.Info("write loop shutting down", "address", s.ListenAddr, "remote", client.Conn.RemoteAddr())
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
						slog.Info("Connection closed by client", "address", s.ListenAddr, "remote", client.Conn.RemoteAddr())
						return
					}

					slog.Error("error writing to client", "address", s.ListenAddr, "remote", client.Conn.RemoteAddr(), "error", err)
					return
				}
			}
		}
	}
}

func (s *Game) gameLoop() {
	<-s.startCh

	s.state = protocol.GameStateLobby
	slog.Info("Waiting for minimum players to start the game...", "address", s.ListenAddr)

	for len(s.clients) < 2 {
		time.Sleep(time.Second)
	}

	slog.Info("Minimum players reached, starting the game...", "address", s.ListenAddr)
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

	slog.Info("Game finished, waiting for clients to receive final messages...", "address", s.ListenAddr)
	s.state = protocol.GameStateFinished
	s.broadcastGameState()

	time.Sleep(2 * time.Second) // wait for clients to receive the final message
	close(s.endCh)              // signal Game shutdown
	s.ln.Close()                // close listener to stop hanging in the accept loop
}

func (s *Game) handleMessage(msg protocol.Message, client *Client) {
	switch msg.Type {
	case protocol.SubmitAnswer:
		slog.Info("Received answer submission from client", "address", s.ListenAddr, "remote", client.Conn.RemoteAddr())
		if s.state != protocol.GameStateQuestion {
			slog.Info("Received answer from client but game is not in question state", "address", s.ListenAddr, "remote", client.Conn.RemoteAddr())
			return
		}

		answerPayload, ok := msg.Payload.(protocol.SubmitAnswerPayload)
		if !ok {
			slog.Error("Invalid payload type for SubmitAnswer", "address", s.ListenAddr, "remote", client.Conn.RemoteAddr())
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
			slog.Info("Received StartGame from non-host client", "address", s.ListenAddr, "remote", client.Conn.RemoteAddr())
			return
		}

		s.startCh <- struct{}{}
	default:
		slog.Error("Received unknown message type", "address", s.ListenAddr, "remote", client.Conn.RemoteAddr(), "type", msg.Type)
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
