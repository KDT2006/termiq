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

// OutboundMessage represents a message that will be sent to clients.
type OutboundMessage struct {
	Client  *Client
	Type    MessageType
	Payload []byte
}
