package protocol

import (
	"encoding/gob"

	"github.com/KDT2006/termiq/internal/config"
	"github.com/google/uuid"
)

type MessageType string
type GameState string

func Init() {
	gob.Register(Message{})
	gob.Register(GameStatePayload{})
	gob.Register(QuestionPayload{})
	gob.Register(TimerPayload{})
	gob.Register(ScorePayload{})
	gob.Register(LeaderboardPayload{})
	gob.Register(PlayerRank{})
	gob.Register(JoinGamePayload{})
	gob.Register(SubmitAnswerPayload{})
	gob.Register(CreateGamePayload{})
	gob.Register(config.Config{})
	gob.Register(config.QuestionSet{})
	gob.Register(config.Question{})
	gob.Register(JoinGameResponse{})
	gob.Register(CreateGameResponse{})
	gob.Register(NewClientPayload{})
	gob.Register(StartGamePayload{})
	gob.Register(MatchmakerError{})
}

const (
	// Game -> Client
	GameStateUpdate MessageType = "game_state"
	QuestionMessage MessageType = "question"
	TimerUpdate     MessageType = "timer"
	ScoreUpdate     MessageType = "score"
	LeaderboardMsg  MessageType = "leaderboard"
	GameOverMsg     MessageType = "game_over"

	// Game -> Host
	NewClientMsg MessageType = "new_client"

	// Client -> Game
	StartGame    MessageType = "start_game"
	SubmitAnswer MessageType = "answer"

	// Client -> Matchmaker
	JoinGame   MessageType = "join"
	CreateGame MessageType = "create_game"

	// Matchmaker -> Client
	JoinGameResponseMsg   MessageType = "join_response"
	CreateGameResponseMsg MessageType = "create_response"
	MatchmakerErrorMsg    MessageType = "error"
)

const (
	GameStateLobby    GameState = "lobby"
	GameStateQuestion GameState = "question"
	GameStateScores   GameState = "scores"
	GameStateFinished GameState = "finished"
)

// Message represents the base message structure sent between client and server.
type Message struct {
	Type    MessageType
	Payload interface{}
}

// GameStatePayload represents the current state of the game.
type GameStatePayload struct {
	State       GameState
	PlayerCount int
}

// QuestionPayload represents a question sent to the client.
type QuestionPayload struct {
	QuestionID int
	Question   string
	Choices    []string
	TimeLimit  int // seconds
}

// TimerPayload represents a timer update for the current question.
type TimerPayload struct {
	QuestionID int
	TimeLeft   int // seconds
}

// ScorePayload represents the score update for a player(after answering a question).
type ScorePayload struct {
	PlayerName string
	Score      int
	Correct    bool // whether the answer was correct
}

// LeaderboardPayload represents the leaderboard sent to the client.
type LeaderboardPayload struct {
	Rankings []PlayerRank
}

// PlayerRank represents a player's rank in the leaderboard.
type PlayerRank struct {
	PlayerName string
	Score      int
	Rank       int
}

// SubmitAnswerPayload represents the payload for submitting an answer to a question.
type SubmitAnswerPayload struct {
	QuestionID int
	Answer     int
	TimeLeft   int
}

type JoinGamePayload struct {
	PlayerID   uuid.UUID
	PlayerName string
	GameCode   string // auto-genrerated code on the server
}

// NewClientPayload represents the payload sent to the host when a new client connects.
type NewClientPayload struct {
	PlayerName  string
	PlayerCount int
}

type StartGamePayload struct { // empty struct to signal game start from the host
}

type CreateGamePayload struct {
	PlayerID   uuid.UUID
	PlayerName string
	Config     *config.Config
	CurrentSet *config.QuestionSet
}

type JoinGameResponse struct {
	ServerURL string // URL to connect to the game server
}

type CreateGameResponse struct {
	ServerURL string // URL to connect to the game server
	GameCode  string // auto-generated game code
}

type MatchmakerError struct {
	Message string // Error message
}
