package minhq

import (
	"github.com/ekr/minq"
	"github.com/martinthomson/minhq/mw"
)

// Server is a basic HTTP server.  New requests are emitted from the Requests channel.
type Server struct {
	mw.Server
	config *Config

	Connections <-chan *ServerConnection
	Requests    <-chan *ServerRequest
}

func RunServer(ms *minq.Server, config *Config) *Server {
	connections := make(chan *ServerConnection)
	requests := make(chan *ServerRequest)
	s := &Server{
		Server:      *mw.RunServer(ms),
		config:      config,
		Connections: connections,
		Requests:    requests,
	}

	go s.serviceConnections(connections, requests)
	return s
}

func (s *Server) serviceConnections(connections chan<- *ServerConnection, requests chan<- *ServerRequest) {
	for {
		select {
		case c := <-s.Server.Connections:
			if c == nil {
				return
			}
			connections <- newServerConnection(c, s.config, requests)
		}
	}
}
