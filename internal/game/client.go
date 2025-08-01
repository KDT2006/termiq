package game

import (
	"encoding/gob"
	"net"

	"github.com/KDT2006/termiq/internal/protocol"
)

// Client represents a player in a game.
type Client struct {
	Conn       net.Conn
	Host       bool
	Encoder    *gob.Encoder
	Decoder    *gob.Decoder
	PlayerName string
	Outbound   chan protocol.Message
	Score      int
}
