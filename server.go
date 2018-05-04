package minhq

import (
	"github.com/ekr/minq"
	"github.com/martinthomson/minhq/mw"
)

// Server is a basic HTTP server.  New requests are emitted from the Requests channel.
type Server struct {
	mw.Server
	config *Config

	// Incoming requests are the primary purpose of this API.
	Requests <-chan *ServerRequest

	// Connections holds the connections that have been established.
	// Users of this API should ignore these with a simple goroutine like
	// `go func() { for <-server.Connections != nil {} }()` unless they
	// need direct access to the connection.
	Connections <-chan *ServerConnection
}

// RunServer takes a minq Server and starts the various goroutines that service it.
func RunServer(ms *minq.Server, config *Config) *Server {
	requests := make(chan *ServerRequest)
	connections:= make(chan *ServerConnection)
	s := &Server{
		Server:      *mw.RunServer(ms),
		config:      config,
		Requests:    requests,
		Connections: connections,
	}

	go s.serviceConnections(requests, connections)
	return s
}

func (s *Server) serviceConnections(requests chan<- *ServerRequest, connections chan<- *ServerConnection) {
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
