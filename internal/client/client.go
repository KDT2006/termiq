package client

import (
	"bufio"
	"encoding/gob"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/KDT2006/termiq/internal/protocol"
)

type Client struct {
	ServerAddr      string
	conn            net.Conn
	playerName      string
	encoder         *gob.Encoder
	decoder         *gob.Decoder
	currentQuestion *protocol.QuestionPayload
	gameState       protocol.GameState
	timeLeft        int // seconds left for the current question
}

func New(serverAddr string) *Client {
	return &Client{
		ServerAddr: serverAddr,
	}
}

func (c *Client) Connect() error {
	// Initialize gob types
	protocol.Init()

	fmt.Print("Enter your player name: ")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	c.playerName = scanner.Text()

	conn, err := net.Dial("tcp", c.ServerAddr)
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}
	c.conn = conn
	c.encoder = gob.NewEncoder(conn)
	c.decoder = gob.NewDecoder(conn)

	fmt.Printf("Connected to server at %s\n", c.ServerAddr)

	// Send join message
	err = c.sendMessage(protocol.Message{
		Type: protocol.JoinGame,
		Payload: protocol.JoinGamePayload{
			PlayerName: c.playerName,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to send join message: %w", err)
	}

	go c.readLoop()
	c.inputLoop()

	return nil
}

func (c *Client) sendMessage(msg protocol.Message) error {
	return c.encoder.Encode(msg)
}

func (c *Client) readLoop() {
	for {
		var msg protocol.Message
		if err := c.decoder.Decode(&msg); err != nil {
			fmt.Printf("Error reading message: %v\n", err)
			return
		}
		c.handleMessage(msg)
	}
}

func (c *Client) handleMessage(msg protocol.Message) {
	switch msg.Type {
	case protocol.GameStateUpdate:
		state := msg.Payload.(protocol.GameStatePayload)
		if state.State == protocol.GameStateFinished {
			fmt.Println("Game is finished! Shutting down client...")
			c.conn.Close()
			os.Exit(0)
		}
		c.gameState = state.State
		c.displayGameState(state)

	case protocol.QuestionMessage:
		question := msg.Payload.(protocol.QuestionPayload)
		c.currentQuestion = &question
		c.displayQuestion(question)

	case protocol.TimerUpdate:
		timer := msg.Payload.(protocol.TimerPayload)
		c.timeLeft = timer.TimeLeft
		// c.displayTimer(timer) // Can be used to display timer updates if needed

	case protocol.ScoreUpdate:
		score := msg.Payload.(protocol.ScorePayload)
		c.displayScore(score)

	case protocol.LeaderboardMsg:
		leaderboard := msg.Payload.(protocol.LeaderboardPayload)
		c.displayLeaderboard(leaderboard)
	}
}

func (c *Client) inputLoop() {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		c.handleInput(scanner.Text())
	}
}

func (c *Client) handleInput(input string) {
	if c.gameState != protocol.GameStateQuestion || c.currentQuestion == nil {
		return
	}

	input = strings.TrimSpace(input)
	if len(input) != 1 {
		fmt.Println("Please enter a single letter (A, B, C, or D)")
		return
	}

	answerIndex := int(input[0] - 'A')
	if answerIndex < 0 || answerIndex >= len(c.currentQuestion.Choices) {
		fmt.Println("Invalid choice. Please enter A, B, C, or D")
		return
	}

	c.sendMessage(protocol.Message{
		Type: protocol.SubmitAnswer,
		Payload: protocol.SubmitAnswerPayload{
			QuestionID: c.currentQuestion.QuestionID,
			Answer:     answerIndex,
			TimeLeft:   c.timeLeft,
		},
	})
}

// Display functions
func (c *Client) displayGameState(state protocol.GameStatePayload) {
	fmt.Printf("\n=== Game State: %s ===\n", state.State)
	if state.State == "lobby" {
		fmt.Printf("Waiting for players... (%d connected)\n", state.PlayerCount)
	}
}

func (c *Client) displayQuestion(question protocol.QuestionPayload) {
	fmt.Printf("\n=== Question %d ===\n", question.QuestionID+1)
	fmt.Printf("%s\n\n", question.Question)
	for i, choice := range question.Choices {
		fmt.Printf("%c) %s\n", 'A'+i, choice)
	}
	fmt.Printf("Time limit: %d seconds\n", question.TimeLimit)
}

func (c *Client) displayTimer(timer protocol.TimerPayload) {
	fmt.Printf("Time remaining: %d seconds\n", timer.TimeLeft)
}

func (c *Client) displayScore(score protocol.ScorePayload) {
	if score.PlayerName == c.playerName {
		if score.Correct {
			fmt.Printf("\nCorrect! Score: %d\n", score.Score)
		} else {
			fmt.Printf("\nIncorrect. Score: %d\n", score.Score)
		}
	}
}

func (c *Client) displayLeaderboard(leaderboard protocol.LeaderboardPayload) {
	fmt.Println("\n=== LEADERBOARD ===")
	for _, rank := range leaderboard.Rankings {
		fmt.Printf("%d. %s: %d points\n", rank.Rank, rank.PlayerName, rank.Score)
	}
	fmt.Println("=================")
}
