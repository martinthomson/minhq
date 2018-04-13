package mw

import (
	"net"
	"time"

	"github.com/ekr/minq"
)

// Packet represents a packet.  It has addresses and a payload.
type Packet struct {
	RemoteAddr *net.UDPAddr
	LocalAddr  *net.UDPAddr
	Data       []byte
}

type readState struct {
	readable bool
	reader   *readRequest
}

func (state *readState) readFrom(minqs minq.RecvStream) {
	if state.reader == nil || !state.readable {
		return // noop
	}
	n, err := minqs.Read(state.reader.p)
	if err == minq.ErrorWouldBlock {
		state.readable = false
		return // That blocked.  Leave the reader in place.
	}
	state.reader.result <- &ioResult{n, err}
	state.reader = nil
}

// Connection is an async wrapper around minq.Connection
type Connection struct {
	minq *minq.Connection

	// Connected produces this connection when the connection is established.
	Connected <-chan *Connection
	connected chan<- *Connection
	closed    chan struct{}
	// RemoteStreams is an unbuffered channel of streams created by a peer.
	RemoteStreams <-chan minq.Stream
	remoteStreams chan<- minq.Stream
	// RemoteRecvStreams is an unbuffered channel of unidirectional streams created by a peer.
	RemoteRecvStreams <-chan minq.RecvStream
	remoteRecvStreams chan<- minq.RecvStream
	// IncomingPackets are packets that arrive at the connection.
	IncomingPackets chan<- *Packet

	readState map[minq.RecvStream]*readState
	ops       connectionOperations
}

func newConnection(mc *minq.Connection, ops connectionOperations) *Connection {
	connected := make(chan *Connection)
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

		readState: make(map[minq.RecvStream]*readState),
		ops:       ops,
	}
	mc.SetHandler(c)
	return c
}

// NewConnection makes a new client connection.
func NewConnection(mc *minq.Connection) *Connection {
	ops := connectionOperations{make(chan interface{}), 0}
	c := newConnection(mc, ops)
	// Only clients need to handle packets directly. Server handles routing of
	// incoming packets for servers.
	incoming := make(chan *Packet)
	c.IncomingPackets = incoming
	go ops.ReadPackets(incoming)
	go c.service()
	return c
}

// newServerConnection is used by Server to make connections. The resulting
// connection doesn't accept incoming packets from Connection.IncomingPackets
// (that is set to nil), because the expectation is that packets will be passed
// to the server.
func newServerConnection(mc *minq.Connection, ops connectionOperations) *Connection {
	if mc.Role() != minq.RoleServer {
		panic("minq.Server spat out a client")
	}
	return newConnection(mc, ops)
}

// Service is intended to be run as a goroutine. This is the only goroutine that
// can touch the underlying functions on minq objects.
func (c *Connection) service() {
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
			c.ops.Handle(op, func(p *Packet) {
				_ = c.minq.Input(p.Data)
			})
		case <-ticker.C:
			c.minq.CheckTimer()
		}
	}
}

func (c *Connection) cleanup() {
	c.ops.Close()
	close(c.connected)
	close(c.remoteStreams)
}

// StateChanged is required by the minq.ConnectionHandler interface.
func (c *Connection) StateChanged(s minq.State) {

	switch s {
	case minq.StateEstablished:
		c.connected <- c
	case minq.StateClosed, minq.StateError:
		close(c.closed)
	}
}

// NewStream is required by the minq.ConnectionHandler interface.
func (c *Connection) NewStream(s minq.Stream) {
	c.remoteStreams <- &Stream{SendStream{c, s}, RecvStream{c, s}}
}

// NewRecvStream is required by the minq.ConnectionHandler interface.
func (c *Connection) NewRecvStream(s minq.RecvStream) {
	c.remoteRecvStreams <- &RecvStream{c, s}
}

// StreamReadable is required by the minq.ConnectionHandler interface.
func (c *Connection) StreamReadable(s minq.RecvStream) {
	state := c.readState[s]
	if state == nil {
		state = &readState{true, nil}
		c.readState[s] = state
	} else {
		state.readable = true
	}
	state.readFrom(s)
}

func (c *Connection) handleReadRequest(req *readRequest) {
	state := c.readState[req.s.minq]
	if state == nil {
		state = &readState{false, req}
		c.readState[req.s.minq] = state
	} else if state.reader == nil {
		state.reader = req
	} else {
		panic("Concurrent reads from the same stream")
	}
	state.readFrom(req.s.minq)
}

// GetState returns the current connection of the connection.
func (c *Connection) GetState() minq.State {
	state := make(chan minq.State)
	c.ops.Add(&getStateRequest{c, state})
	return <-state
}

// Close the connection.
func (c *Connection) Close( /* TODO application error code */ ) error {
	c.ops.Add(&closeConnectionRequest{c})
	<-c.closed
	return nil
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
	return <-result
}
