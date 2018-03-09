package mw

import (
	"errors"

	"github.com/ekr/minq"
)

// ErrConnectionClosed indicates that the connection closed before the requested
// operation could be completed. This is never used for Connection.Close().
var ErrConnectionClosed = errors.New("connection closed before operation could complete")

type getStateRequest struct {
	c      *Connection
	result chan<- minq.State
}

type createBidiStreamRequest struct {
	c      *Connection
	result chan<- *Stream
}

type createUniStreamRequest struct {
	c      *Connection
	result chan<- *SendStream
}

type ioResult struct {
	n   int
	err error
}

type ioRequest struct {
	c      *Connection
	s      *Stream
	p      []byte
	result chan<- *ioResult
}

type resetRequest struct {
	c      *Connection
	s      *Stream
	err    minq.ErrorCode // TODO application error code
	result chan<- error
}

type stopRequest struct {
	c      *Connection
	s      *Stream
	result chan<- error
}

type closeConnectionRequest struct {
	c *Connection
	// TODO error code.
}

type connectionOperations struct {
	getState         chan *getStateRequest
	createBidiStream chan *createBidiStreamRequest
	createUniStream  chan *createUniStreamRequest
	read             chan *ioRequest
	write            chan *ioRequest
	reset            chan *resetRequest
	stopSending      chan *stopRequest
	closeStream      chan *stopRequest
	closeConnection  chan *closeConnectionRequest
}

func newConnectionOperations() *connectionOperations {
	return &connectionOperations{
		getState:         make(chan *getStateRequest),
		createBidiStream: make(chan *createBidiStreamRequest),
		createUniStream:  make(chan *createUniStreamRequest),
		read:             make(chan *ioRequest),
		write:            make(chan *ioRequest),
		reset:            make(chan *resetRequest),
		stopSending:      make(chan *stopRequest),
		closeStream:      make(chan *stopRequest),
		closeConnection:  make(chan *closeConnectionRequest),
	}
}

// Select polls the set of operations and runs any necessary operations.
func (ops *connectionOperations) Select() {
	select {
	case getStateReq := <-ops.getState:
		getStateReq.result <- getStateReq.c.GetState()

	case closeConnectionReq := <-ops.closeConnection:
		closeConnectionReq.c.minq.Close( /* TODO Application Close for minq */ )

	case createStreamReq := <-ops.createBidiStream:
		c := createStreamReq.c
		s := c.minq.CreateBidirectionalStream()
		createStreamReq.result <- &Stream{c, s.Id(), s, s}

	case createStreamReq := <-ops.createUniStream:
		c := createStreamReq.c
		s := c.minq.CreateUnidirectionalStream()
		createStreamReq.result <- &SendStream{Stream{c, s.Id(), s, nil}}

	case readReq := <-ops.read:
		readReq.c.handleReadRequest(readReq)

	case writeReq := <-ops.write:
		n, err := writeReq.s.send.Write(writeReq.p)
		writeReq.result <- &ioResult{n, err}

	case closeReq := <-ops.closeStream:
		closeReq.s.send.Close()
		closeReq.result <- nil

	case resetReq := <-ops.reset:
		resetReq.result <- resetReq.s.send.Reset(resetReq.err)

	case stopSendingReq := <-ops.stopSending:
		// TODO minq doesn't support stop sending
		// Note that closing the channel shouldn't be necessary, but caution is
		// always welcome in these matters.
		c := stopSendingReq.c
		state := c.readState[stopSendingReq.s.recv]
		if state != nil {
			if state.reader != nil {
				close(state.reader.result)
			}
			delete(c.readState, stopSendingReq.s.recv)
		}
		stopSendingReq.result <- nil

	default:
		// Do nothing
	}
}

func (ops *connectionOperations) Close() error {
	for {
		select {
		case gs := <-ops.getState:
			gs.result <- gs.c.minq.GetState()

		case cs := <-ops.createBidiStream:
			close(cs.result)

		case cs := <-ops.createUniStream:
			close(cs.result)

		case r := <-ops.read:
			r.result <- &ioResult{0, ErrConnectionClosed}

		case w := <-ops.write:
			w.result <- &ioResult{0, ErrConnectionClosed}

		case rst := <-ops.reset:
			rst.result <- ErrConnectionClosed

		case cs := <-ops.closeStream:
			cs.result <- nil

		case ss := <-ops.stopSending:
			ss.result <- nil

		case <-ops.closeConnection:
			// NOP

		default:
			break
		}
	}

	close(ops.getState)
	close(ops.createBidiStream)
	close(ops.createUniStream)
	close(ops.read)
	close(ops.write)
	close(ops.reset)
	close(ops.stopSending)
	close(ops.closeStream)
	close(ops.closeConnection)
	return nil
}
