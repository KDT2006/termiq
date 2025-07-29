package client

import (
	"bufio"
	"fmt"
	"net"
	"os"
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

	go c.startWriteLoop()
	c.startReadLoop()

	return nil
}

func (c *Client) startReadLoop() {
	defer c.Conn.Close()

	for {
		buf := make([]byte, 1024)
		_, err := c.Conn.Read(buf)
		if err != nil {
			fmt.Printf("Error reading from server: %v\n", err)
			return
		}

		fmt.Printf("Received: %s\n", string(buf))
	}
}

func (c *Client) startWriteLoop() {
	defer c.Conn.Close()

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		message := scanner.Text()
		_, err := c.Conn.Write([]byte(message))
		if err != nil {
			fmt.Printf("Failed to send message: %v\n", err)
			return
		}
	}
}
