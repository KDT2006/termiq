package server

import (
	"encoding/gob"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"sync"
	"time"

	"github.com/KDT2006/termiq/internal/game"
	"github.com/KDT2006/termiq/internal/protocol"
)

// Server represents the game server that manages multiple games
// and acts as a matchmaker for clients.
type Server struct {
	ListenAddr string
	Games      map[string]*game.Game
	mu         sync.Mutex

	ports   map[int]struct{} // list of ports used for games
	portsMu sync.Mutex
}

func New(listenAddr string) *Server {
	return &Server{
		ListenAddr: listenAddr,
		Games:      make(map[string]*game.Game),
		ports:      make(map[int]struct{}),
	}
}

func (s *Server) Start() error {
	protocol.Init()    // ensure protocol types are registered with gob
	go s.cleanupLoop() // Start the cleanup job

	slog.Info("Starting server", "address", s.ListenAddr)

	ln, err := net.Listen("tcp", s.ListenAddr)
	if err != nil {
		return fmt.Errorf("failed to create listener: %w", err)
	}
	defer ln.Close()

	for {
		conn, err := ln.Accept()
		if err != nil {
			slog.Error("failed to accept connection", "address", s.ListenAddr, "error", err)
			continue
		}

		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	// Wait for the join message from the client
	var msg protocol.Message
	decoder := gob.NewDecoder(conn)
	if err := decoder.Decode(&msg); err != nil {
		slog.Error("failed to decode message", "address", s.ListenAddr, "error", err)
		conn.Close()
		return
	}

	switch msg.Type {
	case protocol.JoinGame:
		s.handleGameJoin(conn, msg)
	case protocol.CreateGame:
		s.handleGameCreate(conn, msg)
	default:
		slog.Error("unknown message type", "address", s.ListenAddr, "type", msg.Type)
		conn.Close()
		return
	}
}

func (s *Server) handleGameJoin(conn net.Conn, msg protocol.Message) {
	defer conn.Close()

	encoder := gob.NewEncoder(conn)

	joinMessage, ok := msg.Payload.(protocol.JoinGamePayload)
	if !ok {
		slog.Error("invalid join game payload", "address", s.ListenAddr, "payload", msg.Payload)
		s.respondWithError(encoder, "invalid join game payload")
		return
	}

	s.mu.Lock()
	gameCode := joinMessage.GameCode
	if _, ok := s.Games[gameCode]; !ok {
		slog.Error("game not found", "address", s.ListenAddr, "gameCode", gameCode)
		s.mu.Unlock()
		s.respondWithError(encoder, "game not found")
		return
	}

	gameInstance := s.Games[gameCode]
	s.mu.Unlock()

	// Check if the game is in the lobby state
	state := gameInstance.GetState()
	if state != protocol.GameStateLobby {
		slog.Error("game is not in lobby state", "address", s.ListenAddr, "gameCode", gameCode, "currentState", state)
		s.respondWithError(encoder, "game is not in lobby state")
		return
	}

	// Return the game url for the client to connect
	response := protocol.Message{
		Type: protocol.JoinGameResponseMsg,
		Payload: protocol.JoinGameResponse{
			ServerURL: gameInstance.ListenAddr,
		},
	}

	if err := encoder.Encode(response); err != nil {
		slog.Error("failed to send join response", "address", s.ListenAddr, "error", err)
		return
	}
}

func (s *Server) handleGameCreate(conn net.Conn, msg protocol.Message) {
	defer conn.Close()

	encoder := gob.NewEncoder(conn)

	createMessage, ok := msg.Payload.(protocol.CreateGamePayload)
	if !ok {
		slog.Error("invalid create game payload", "address", s.ListenAddr, "payload", msg.Payload)
		s.respondWithError(encoder, "invalid create game payload")
		return
	}

	// Create and start a new game instance
	s.mu.Lock()
	gameCode := s.generateGameCode()            // Generate a unique game code
	gameAddr, err := s.generateRandomGameAddr() // Generate a random game address
	if err != nil {
		slog.Error("failed to generate game address", "address", s.ListenAddr, "error", err)
		s.respondWithError(encoder, "failed to generate game address")
		s.mu.Unlock()
		return
	}

	newGame := game.New(gameAddr, createMessage.Config, createMessage.CurrentSet, createMessage.PlayerID)
	s.Games[gameCode] = newGame
	s.mu.Unlock()

	go func() {
		if err := newGame.Start(); err != nil {
			slog.Error("failed to start game", "address", s.ListenAddr, "error", err)
			s.respondWithError(encoder, "failed to start game")
			return
		}
	}()

	time.Sleep(1 * time.Second) // Give the game some time to start

	response := protocol.Message{
		Type: protocol.CreateGameResponseMsg,
		Payload: protocol.CreateGameResponse{
			ServerURL: newGame.ListenAddr,
			GameCode:  gameCode,
		},
	}
	if err := encoder.Encode(response); err != nil {
		slog.Error("failed to send create game response", "address", s.ListenAddr, "error", err)
		return
	}
}

func (s *Server) generateGameCode() string {
	charset := "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

	code := [6]byte{}
	for i := range len(code) {
		code[i] = charset[rand.IntN(len(charset))]
	}

	return string(code[:])
}

func (s *Server) generateRandomGameAddr() (string, error) {
	s.portsMu.Lock()
	defer s.portsMu.Unlock()

	port := s.generateRandomPort()
	if port == 0 {
		return "", fmt.Errorf("no available ports")
	} else {
		return fmt.Sprintf("localhost:%d", port), nil
	}
}

func (s *Server) generateRandomPort() int {
	base := 4001
	maxPorts := 1000    // Total number of ports in the range
	maxAttempts := 1000 // Max attempts to find a free port

	if len(s.ports) >= maxPorts {
		slog.Error("maximum number of ports reached", "address", s.ListenAddr)
		return 0 // return 0 to indicate no ports available
	}

	for range maxAttempts {
		offset := rand.IntN(maxPorts)
		port := base + offset

		if _, ok := s.ports[port]; !ok {
			s.ports[port] = struct{}{}
			return port
		}
	}

	slog.Error("Failed to find a free port after attempts", "address", s.ListenAddr, "attempts", maxAttempts)
	return 0
}

func (s *Server) respondWithError(encoder *gob.Encoder, message string) {
	encoder.Encode(protocol.Message{
		Type: protocol.MatchmakerErrorMsg,
		Payload: protocol.MatchmakerError{
			Message: message,
		},
	})
}

func (s *Server) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		s.mu.Lock()
		for code, game := range s.Games {
			if game.GetState() == protocol.GameStateFinished {
				slog.Info("Cleaning up finished game", "gameAddr", game.ListenAddr, "gameCode", code)
				delete(s.Games, code)

				port, ok := s.getGamePort(game.ListenAddr)
				if !ok {
					slog.Error("Failed to find port for finished game", "gameAddr", game.ListenAddr, "gameCode", code)
					continue
				}
				delete(s.ports, port)
			} else {
				slog.Debug("Game still active, skipping cleanup", "address", s.ListenAddr, "gameCode", code)
			}
		}
		s.mu.Unlock()
	}
}

func (s *Server) getGamePort(gameAddr string) (int, bool) {
	s.portsMu.Lock()
	defer s.portsMu.Unlock()

	for port := range s.ports {
		if fmt.Sprintf("localhost:%d", port) == gameAddr {
			return port, true
		}
	}

	return 0, false
}
