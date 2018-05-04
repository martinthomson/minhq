package mw

import (
	"encoding/hex"
	"fmt"
	"errors"
	"sync/atomic"

	"github.com/ekr/minq"
)

// ErrConnectionClosed indicates that the connection closed before the requested
// operation could be completed. This is never used for Connection.Close().
var ErrConnectionClosed = errors.New("connection closed before operation could complete")

type getStateRequest struct {
	c      *Connection
	result chan<- minq.State
}

type getSendStateRequest struct {
	s      *SendStream
	result chan<- minq.SendStreamState
}
type getRecvStateRequest struct {
	s      *RecvStream
	result chan<- minq.RecvStreamState
}

type createStreamRequest struct {
	c      *Connection
	result chan<- minq.Stream
}

type createSendStreamRequest struct {
	c      *Connection
	result chan<- minq.SendStream
}

type ioResult struct {
	n   int
	err error
}

type ioRequest struct {
	c      *Connection
	p      []byte
	result chan<- *ioResult
}

type writeRequest struct {
	ioRequest
	s *SendStream
}

type readRequest struct {
	ioRequest
	s *RecvStream
}

type resetRequest struct {
	c      *Connection
	s      *SendStream
	code   minq.ErrorCode // TODO application error code
	result chan<- error
}

type closeStreamRequest struct {
	c      *Connection
	s      *SendStream
	result chan<- error
}

type stopRequest struct {
	c      *Connection
	s      *RecvStream
	code   minq.ErrorCode
	result chan<- error
}

type closeConnectionRequest struct {
	c *Connection
	// TODO error code.
}

type connectionOperations struct {
	ch     chan interface{}
	closed uint32
}

func (ops connectionOperations) Add(op interface{}) {
	if atomic.LoadUint32(&ops.closed) == 0 {
		ops.ch <- op
	}
}

// ReadPackets is intended to handle a channel of incoming packets.  Intended to be run as a goroutine.
func (ops connectionOperations) ReadPackets(incoming <-chan *Packet) {
	for {
		p, ok := <-incoming
		if !ok {
			return
		}
		ops.ch <- p
	}
}

// Select polls the set of operations and runs any necessary operations.
func (ops connectionOperations) Handle(v interface{}, packetHandler func(*Packet)) {
	switch op := v.(type) {
	case *getStateRequest:
		op.result <- op.c.minq.GetState()

	case *closeConnectionRequest:
		op.c.minq.Close( /* TODO Application Close for minq */ )

	case *createStreamRequest:
		s := op.c.minq.CreateStream()
		op.result <- &Stream{SendStream{op.c, s}, RecvStream{op.c, s}}

	case *createSendStreamRequest:
		s := op.c.minq.CreateSendStream()
		op.result <- &SendStream{op.c, s}

	case *getSendStateRequest:
		op.result <- op.s.minq.SendState()

	case *writeRequest:
		n, err := op.s.minq.Write(op.p)
		fmt.Printf("Write on stream %v: %v\n", op.s.Id(), hex.EncodeToString(op.p))
		op.result <- &ioResult{n, err}

	case *closeStreamRequest:
		op.s.minq.Close()
		op.result <- nil

	case *resetRequest:
		op.result <- op.s.minq.Reset(op.code)

	case *getRecvStateRequest:
		op.result <- op.s.minq.RecvState()

	case *readRequest:
		op.c.handleReadRequest(op)

	case *stopRequest:

		// Note that closing the channel shouldn't be necessary, but caution is
		// always welcome in these matters.
		state := op.c.readState[op.s.minq]
		if state != nil {
			if state.reader != nil {
				close(state.reader.result)
			}
			delete(op.c.readState, op.s.minq)
		}
		op.result <- op.s.StopSending(op.code)

	case *Packet:
		packetHandler(op)

	default:
		panic("unknown operation")
	}
}

func (ops connectionOperations) Close() error {
	if atomic.SwapUint32(&ops.closed, 1) == 0 {
		go ops.drain()
	}
	return nil
}

func (ops connectionOperations) drain() {
	// Drain the channel so that any outstanding operations won't hang.
	for {
		var operation interface{}
		select {
		case v, ok := <-ops.ch:
			if !ok {
				return
			}
			operation = v
		}

		switch op := operation.(type) {
		case *getStateRequest:
			op.result <- op.c.minq.GetState()

		case *closeConnectionRequest:
			// NOP

		case *createStreamRequest:
			close(op.result)

		case *createSendStreamRequest:
			close(op.result)

		case *getSendStateRequest:
			op.result <- op.s.minq.SendState()

		case *writeRequest:
			op.result <- &ioResult{0, ErrConnectionClosed}

		case *resetRequest:
			op.result <- ErrConnectionClosed

		case *closeStreamRequest:
			op.result <- nil

		case *getRecvStateRequest:
			op.result <- op.s.minq.RecvState()

		case *readRequest:
			op.result <- &ioResult{0, ErrConnectionClosed}

		case *stopRequest:
			op.result <- nil

		case *Packet:
			// NOOP

		default:
			panic("unknown operation")
		}
	}
}
