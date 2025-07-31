package server

import (
	"encoding/gob"
	"net"

	"github.com/KDT2006/termiq/internal/protocol"
)

type Client struct {
	conn       net.Conn
	encoder    *gob.Encoder
	decoder    *gob.Decoder
	playerName string
	outbound   chan protocol.Message
	score      int
}
