package client

import (
	"fmt"
	"net"
)

type Client struct {
	ServerAddr string
	Conn       net.Conn
}

func New(serverAddr string) *Client {
	return &Client{
		ServerAddr: serverAddr,
	}
}

func (c *Client) Connect() error {
	conn, err := net.Dial("tcp", c.ServerAddr)
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}
	c.Conn = conn
	fmt.Printf("Connected to server at %s\n", c.ServerAddr)
	c.startWriteLoop()

	return nil
}

func (c *Client) startWriteLoop() {
	defer c.Conn.Close()

	for i := range 100 {
		message := fmt.Sprintf("Message %d", i)
		c.Conn.Write([]byte(message))

		buf := make([]byte, 1024)
		_, err := c.Conn.Read(buf)
		if err != nil {
			fmt.Printf("failed to read response: %v\n", err)
			return
		}
		fmt.Printf("Received response: %s\n", buf)

		// time.Sleep(time.Millisecond)
	}
}
