package main

import (
	"log"

	"github.com/KDT2006/termiq/internal/client"
)

func main() {
	client := client.New("localhost:4000")
	log.Fatal(client.Connect())
}
