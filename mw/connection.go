package mw

import (
	"net"
	"time"

	"github.com/ekr/minq"
)

// Packet represents a UDP packet.  It has addresses and a payload.
type Packet struct {
	DestAddr *net.UDPAddr
	SrcAddr  *net.UDPAddr
	Data     []byte
}

// Connection is an async wrapper around minq.Connection
type Connection struct {
	minq *minq.Connection

	// Connected produces this connection when the connection is established.
	Connected    <-chan struct{}
	wasConnected bool
	connected    chan<- struct{}
	closed       chan struct{}
	// RemoteStreams is an unbuffered channel of streams created by a peer.
	RemoteStreams <-chan minq.Stream
	remoteStreams chan<- minq.Stream
	// RemoteRecvStreams is an unbuffered channel of unidirectional streams created by a peer.
	RemoteRecvStreams <-chan minq.RecvStream
	remoteRecvStreams chan<- minq.RecvStream
	// IncomingPackets are packets that arrive at the connection.
	IncomingPackets chan<- *Packet

	readState map[minq.RecvStream]*readRequest
	ops       *connectionOperations
}

func newConnection(mc *minq.Connection, ops *connectionOperations) *Connection {
	connected := make(chan struct{})
	streams := make(chan minq.Stream)
	recvStreams := make(chan minq.RecvStream)
	c := &Connection{
		minq:              mc,
		Connected:         connected,
		connected:         connected,
		closed:            make(chan struct{}),
		RemoteStreams:     streams,
		remoteStreams:     streams,
		RemoteRecvStreams: recvStreams,
		remoteRecvStreams: recvStreams,

		readState: make(map[minq.RecvStream]*readRequest),
		ops:       ops,
	}
	mc.SetHandler(c)
	return c
}

// NewConnection makes a new client connection.
func NewConnection(mc *minq.Connection) *Connection {
	ops := newConnectionOperations()
	c := newConnection(mc, ops)
	// Only clients need to handle packets directly. Server handles routing of
	// incoming packets for servers.
	incoming := make(chan *Packet)
	c.IncomingPackets = incoming
	go c.service(incoming)
	return c
}

// newServerConnection is used by Server to make connections. The resulting
// connection doesn't accept incoming packets from Connection.IncomingPackets
// (that is set to nil), because the expectation is that packets will be passed
// to the server.
func newServerConnection(mc *minq.Connection, ops *connectionOperations) *Connection {
	if mc.Role() != minq.RoleServer {
		panic("minq.Server spat out a client")
	}
	return newConnection(mc, ops)
}

// Service is intended to be run as a goroutine. This is the only goroutine that
// can touch the underlying functions on minq objects.
func (c *Connection) service(incoming <-chan *Packet) {
	defer c.cleanup()

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		switch c.minq.GetState() {
		case minq.StateClosed, minq.StateError:
			return
		}
		select {
		case op := <-c.ops.ch:
			c.ops.Handle(op)
		case p := <-incoming:
			_ = c.minq.Input(p.Data)
		case <-ticker.C:
			c.minq.CheckTimer()
		}
	}
}

func (c *Connection) cleanup() {
	c.ops.Close()
	if !c.wasConnected {
		close(c.connected)
	}
	close(c.remoteStreams)
}

// Note: each signal/upcall from minq uses a goroutine so that the main
// goroutine doesn't block on any of these operations.

// StateChanged is required by the minq.ConnectionHandler interface.
func (c *Connection) StateChanged(s minq.State) {
	switch s {
	case minq.StateEstablished:
		c.wasConnected = true
		close(c.connected)
	case minq.StateClosed, minq.StateError:
		close(c.closed)
	}
}

// NewStream is required by the minq.ConnectionHandler interface.
func (c *Connection) NewStream(s minq.Stream) {
	go func() {
		c.remoteStreams <- &Stream{SendStream{c, s}, RecvStream{c, s}}
	}()
}

// NewRecvStream is required by the minq.ConnectionHandler interface.
func (c *Connection) NewRecvStream(s minq.RecvStream) {
	go func() {
		c.remoteRecvStreams <- &RecvStream{c, s}
	}()
}

// StreamReadable is required by the minq.ConnectionHandler interface.
func (c *Connection) StreamReadable(s minq.RecvStream) {
	req := c.readState[s]
	if req == nil {
		return
	}

	delete(req.c.readState, req.s.minq)
	if !req.read() {
		panic("read failed after readable event")
	}
}

func (c *Connection) handleReadRequest(req *readRequest) {
	if c.readState[req.s.minq] != nil {
		panic("someone else is waiting on a read")
	}

	if !req.read() {
		c.readState[req.s.minq] = req
	}
}

// GetState returns the current connection of the connection.
func (c *Connection) GetState() minq.State {
	state := make(chan minq.State)
	c.ops.Add(&getStateRequest{c, state})
	return <-state
}

// Close the connection.
func (c *Connection) Close( /* TODO application error code */ ) error {
	result := make(chan error)
	c.ops.Add(&closeConnectionRequest{c, reportErrorChannel{result}})
	return <-result
}

// CreateStream creates a new bidirectional stream.
func (c *Connection) CreateStream() minq.Stream {
	result := make(chan minq.Stream)
	c.ops.Add(&createStreamRequest{c, result})
	return <-result
}

// CreateSendStream creates a new unidirectional stream for sending.
func (c *Connection) CreateSendStream() minq.SendStream {
	result := make(chan minq.SendStream)
	c.ops.Add(&createSendStreamRequest{c, result})
	s := <-result
	return s
}
