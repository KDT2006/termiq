package main

import (
	"github.com/KDT2006/termiq/internal/client"
)

func main() {
	client := client.New("localhost:4000")
	client.Init()
}
