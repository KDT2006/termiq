package protocol

type MessageType string
type GameState string

const (
	// Server -> Client
	GameStateUpdate MessageType = "game_state"
	QuestionMessage MessageType = "question"
	TimerUpdate     MessageType = "timer"
	ScoreUpdate     MessageType = "score"
	LeaderboardMsg  MessageType = "leaderboard"

	// Client -> Server
	JoinGame     MessageType = "join"
	SubmitAnswer MessageType = "answer"
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
	PlayerID string
	Score    int
	Correct  bool // whether the answer was correct
}

// LeaderboardPayload represents the leaderboard sent to the client.
type LeaderboardPayload struct {
	Rankings []PlayerRank
}

// PlayerRank represents a player's rank in the leaderboard.
type PlayerRank struct {
	PlayerID string
	Score    int
	Rank     int
}

type JoinGamePayload struct {
	PlayerName string
}

type SubmitAnswerPayload struct {
	QuestionID int
	Answer     string
	TimeLeft   int
}
