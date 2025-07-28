package server

import "log"

type Server struct {
	ListenAddr string
}

func New(listenAddr string) *Server {
	return &Server{
		ListenAddr: listenAddr,
	}
}

func (s *Server) Start() {
	log.Println("Server running on address:", s.ListenAddr)
}
