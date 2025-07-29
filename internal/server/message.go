package server

type MessageType byte

const (
	MessageTypeChat MessageType = iota
	MessageTypeAnswer
	MessageTypeLeaderboard
)

// Message represents an incoming message from a client.
type Message struct {
	Client  *Client
	Type    MessageType
	Payload []byte
}
