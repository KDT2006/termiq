package main

import (
	"log"

	"github.com/KDT2006/termiq/internal/server"
)

const (
	listenAddr = "localhost:4000"
)

func main() {
	server := server.New(listenAddr)
	log.Fatal(server.Start())
}
