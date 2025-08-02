package game

import (
	"encoding/gob"
	"net"

	"github.com/KDT2006/termiq/internal/protocol"
	"github.com/google/uuid"
)

// Client represents a player in a game.
type Client struct {
	Conn       net.Conn
	Host       bool
	Encoder    *gob.Encoder
	Decoder    *gob.Decoder
	PlayerName string
	PlayerID   uuid.UUID
	Outbound   chan protocol.Message
	Score      int
}
