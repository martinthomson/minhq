package mw

import (
	"time"

	"github.com/ekr/minq"
)

type Server struct {
	s *minq.Server

	// Connections is the connections that are created.
	Connections <-chan *Connection

	// IncomingPackets are incoming packets.
	IncomingPackets chan<- *Packet

	ops      connectionOperations
	shutdown chan chan<- struct{}
}

type serverHandler struct {
	connections chan<- *Connection
	ops         connectionOperations
}

func (sh *serverHandler) NewConnection(mc *minq.Connection) {
	c := newServerConnection(mc, sh.ops)
	go func() {
		sh.connections <- <-c.Connected
	}()
}

func RunServer(ms *minq.Server) *Server {
	connections := make(chan *Connection)
	incoming := make(chan *Packet)
	s := &Server{
		s:               ms,
		Connections:     connections,
		IncomingPackets: incoming,
		ops:             connectionOperations(make(chan interface{})),
		shutdown:        make(chan chan<- struct{}),
	}
	ms.SetHandler(&serverHandler{connections, s.ops})
	go s.ops.ReadPackets(incoming)
	go s.service()
	return s
}

func (s *Server) service() {
	defer s.cleanup()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case op := <-s.ops:
			s.ops.Handle(op, func(p *Packet) {
				_, _ = s.s.Input(p.RemoteAddr, p.Data)
			})

		case <-ticker.C:
			s.s.CheckTimer()

		case done := <-s.shutdown:
			close(done)
			return
		}
	}
}

func (s *Server) cleanup() {
	s.ops.Close()
}

// Close implements io.Closer.
func (s *Server) Close() error {
	done := make(chan struct{})
	s.shutdown <- done
	<-done
	return nil
}