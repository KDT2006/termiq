package server

import (
	"encoding/gob"
	"fmt"
	"log"
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
}

func New(listenAddr string) *Server {
	return &Server{
		ListenAddr: listenAddr,
		Games:      make(map[string]*game.Game),
	}
}

func (s *Server) Start() error {
	protocol.Init() // ensure protocol types are registered with gob

	log.Printf("Starting server on %s", s.ListenAddr)

	ln, err := net.Listen("tcp", s.ListenAddr)
	if err != nil {
		return fmt.Errorf("failed to create listener: %w", err)
	}
	defer ln.Close()

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("failed to accept connection: %v", err)
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
		log.Printf("failed to decode message: %v", err)
		conn.Close()
		return
	}

	switch msg.Type {
	case protocol.JoinGame:
		s.handleGameJoin(conn, msg)
	case protocol.CreateGame:
		s.handleGameCreate(conn, msg)
	default:
		log.Printf("unknown message type: %s", msg.Type)
		conn.Close()
		return
	}
}

func (s *Server) handleGameJoin(conn net.Conn, msg protocol.Message) {
	defer conn.Close()

	encoder := gob.NewEncoder(conn)

	joinMessage, ok := msg.Payload.(protocol.JoinGamePayload)
	if !ok {
		log.Printf("invalid join game payload: %v", msg.Payload)
		s.respondWithError(encoder, "invalid join game payload")
		return
	}

	s.mu.Lock()
	gameCode := joinMessage.GameCode
	if _, ok := s.Games[gameCode]; !ok {
		log.Printf("game not found: %s", gameCode)
		s.mu.Unlock()
		s.respondWithError(encoder, "game not found")
		return
	}

	gameInstance := s.Games[gameCode]
	s.mu.Unlock()

	// Check if the game is in the lobby state
	state := gameInstance.GetState()
	if state != protocol.GameStateLobby {
		log.Printf("game %s is not in lobby state, current state: %s", gameCode, state)
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
		log.Printf("failed to send join response: %v", err)
		return
	}
}

func (s *Server) handleGameCreate(conn net.Conn, msg protocol.Message) {
	defer conn.Close()

	encoder := gob.NewEncoder(conn)

	createMessage, ok := msg.Payload.(protocol.CreateGamePayload)
	if !ok {
		log.Printf("invalid create game payload: %v", msg.Payload)
		s.respondWithError(encoder, "invalid create game payload")
		return
	}

	// Create and start a new game instance
	s.mu.Lock()
	gameCode := s.generateGameCode()       // Generate a unique game code
	gameAddr := s.generateRandomGameAddr() // Generate a random game address
	newGame := game.New(gameAddr, "", "", createMessage.PlayerName)
	s.Games[gameCode] = newGame
	s.mu.Unlock()

	go func() {
		if err := newGame.Start(); err != nil {
			log.Printf("failed to start game: %v", err)
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
		log.Printf("failed to send create game response: %v", err)
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

func (s *Server) generateRandomGameAddr() string {
	return fmt.Sprintf("localhost:%d", 4000+1+len(s.Games)) // Simple port allocation logic
}

func (s *Server) respondWithError(encoder *gob.Encoder, message string) {
	encoder.Encode(protocol.Message{
		Type: protocol.MatchmakerErrorMsg,
		Payload: protocol.MatchmakerError{
			Message: message,
		},
	})
}
