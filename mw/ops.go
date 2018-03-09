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

type getSendStateRequest struct {
	s      *SendStream
	result chan<- minq.SendStreamState
}
type getRecvStateRequest struct {
	s      *RecvStream
	result chan<- minq.RecvStreamState
}

type createBidiStreamRequest struct {
	c      *Connection
	result chan<- minq.Stream
}

type createUniStreamRequest struct {
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
	err    minq.ErrorCode // TODO application error code
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
	getState         chan *getStateRequest
	createBidiStream chan *createBidiStreamRequest
	createUniStream  chan *createUniStreamRequest
	getSendState     chan *getSendStateRequest
	write            chan *writeRequest
	reset            chan *resetRequest
	closeStream      chan *closeStreamRequest
	getRecvState     chan *getRecvStateRequest
	read             chan *readRequest
	stopSending      chan *stopRequest
	closeConnection  chan *closeConnectionRequest
}

func newConnectionOperations() *connectionOperations {
	return &connectionOperations{
		getState:         make(chan *getStateRequest),
		createBidiStream: make(chan *createBidiStreamRequest),
		createUniStream:  make(chan *createUniStreamRequest),
		getSendState:     make(chan *getSendStateRequest),
		write:            make(chan *writeRequest),
		reset:            make(chan *resetRequest),
		closeStream:      make(chan *closeStreamRequest),
		getRecvState:     make(chan *getRecvStateRequest),
		read:             make(chan *readRequest),
		stopSending:      make(chan *stopRequest),
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
		createStreamReq.result <- &Stream{SendStream{c, s}, RecvStream{c, s}}

	case createStreamReq := <-ops.createUniStream:
		c := createStreamReq.c
		s := c.minq.CreateUnidirectionalStream()
		createStreamReq.result <- &SendStream{c, s}

	case sendStateReq := <-ops.getSendState:
		sendStateReq.result <- sendStateReq.s.minq.SendState()

	case writeReq := <-ops.write:
		n, err := writeReq.s.minq.Write(writeReq.p)
		writeReq.result <- &ioResult{n, err}

	case closeReq := <-ops.closeStream:
		closeReq.s.minq.Close()
		closeReq.result <- nil

	case resetReq := <-ops.reset:
		resetReq.result <- resetReq.s.minq.Reset(resetReq.err)

	case recvStateReq := <-ops.getRecvState:
		recvStateReq.result <- recvStateReq.s.minq.RecvState()

	case readReq := <-ops.read:
		readReq.c.handleReadRequest(readReq)

	case stopSendingReq := <-ops.stopSending:

		// Note that closing the channel shouldn't be necessary, but caution is
		// always welcome in these matters.
		c := stopSendingReq.c
		s := stopSendingReq.s
		state := c.readState[s.minq]
		if state != nil {
			if state.reader != nil {
				close(state.reader.result)
			}
			delete(c.readState, s.minq)
		}
		stopSendingReq.result <- s.StopSending(stopSendingReq.code)

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

		case ss := <-ops.getSendState:
			ss.result <- ss.s.minq.SendState()

		case w := <-ops.write:
			w.result <- &ioResult{0, ErrConnectionClosed}

		case rst := <-ops.reset:
			rst.result <- ErrConnectionClosed

		case cs := <-ops.closeStream:
			cs.result <- nil

		case rs := <-ops.getRecvState:
			rs.result <- rs.s.minq.RecvState()

		case r := <-ops.read:
			r.result <- &ioResult{0, ErrConnectionClosed}

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
