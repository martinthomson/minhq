package mw

import (
	"errors"
	"sync"

	"github.com/ekr/minq"
)

// ErrConnectionClosed indicates that the connection closed before the requested
// operation could be completed. This is never used for Connection.Close().
var ErrConnectionClosed = errors.New("connection closed before operation could complete")

// ErrUnknownOperation is what you get when operations are used improperly.
var ErrUnknownOperation = errors.New("unknown operation provided")

type connectionOperation interface {
	report(error)
}

type reportErrorChannel struct {
	result chan<- error
}

func (rec *reportErrorChannel) report(err error) {
	rec.result <- err
}

type getStateRequest struct {
	c      *Connection
	result chan<- minq.State
}

func (op *getStateRequest) report(err error) {
	op.result <- op.c.minq.GetState()
}

type getSendStateRequest struct {
	s      *SendStream
	result chan<- minq.SendStreamState
}

func (op *getSendStateRequest) report(error) {
	op.result <- op.s.SendState()
}

type getRecvStateRequest struct {
	s      *RecvStream
	result chan<- minq.RecvStreamState
}

func (op *getRecvStateRequest) report(error) {
	op.result <- op.s.RecvState()
}

type createStreamRequest struct {
	c      *Connection
	result chan<- minq.Stream
}

func (req *createStreamRequest) report(error) {
	req.result <- nil
}

type createSendStreamRequest struct {
	c      *Connection
	result chan<- minq.SendStream
}

func (req *createSendStreamRequest) report(error) {
	req.result <- nil
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

func (ior *ioRequest) report(err error) {
	ior.result <- &ioResult{0, err}
}

type writeRequest struct {
	ioRequest
	s *SendStream
}

type readRequest struct {
	ioRequest
	s *RecvStream
}

func (req *readRequest) read() bool {
	n, err := req.s.minq.Read(req.p)
	success := err != minq.ErrorWouldBlock
	if success {
		//fmt.Printf("%v %d < %x\n", req.s.c.minq.Role(), req.s.Id(), req.p)
		req.result <- &ioResult{n, err}
	}
	return success
}

type resetRequest struct {
	c    *Connection
	s    *SendStream
	code uint16
	reportErrorChannel
}

type closeStreamRequest struct {
	c *Connection
	s *SendStream
	reportErrorChannel
}

type stopRequest struct {
	c    *Connection
	s    *RecvStream
	code uint16
	reportErrorChannel
}

type closeConnectionRequest struct {
	c *Connection
	reportErrorChannel
}

type applicationCloseRequest struct {
	c    *Connection
	code uint16
	text string
	reportErrorChannel
}

type connectionOperations struct {
	ch        chan connectionOperation
	closed    chan struct{}
	closeOnce sync.Once
}

func newConnectionOperations() *connectionOperations {
	return &connectionOperations{
		ch:        make(chan connectionOperation),
		closed:    make(chan struct{}),
		closeOnce: sync.Once{},
	}
}

func (ops *connectionOperations) Add(op connectionOperation) {
	select {
	case <-ops.closed:
		op.report(ErrConnectionClosed)
	default:
		ops.ch <- op
	}
}

// Select polls the set of operations and runs any necessary operations.
func (ops *connectionOperations) Handle(v connectionOperation) {
	switch op := v.(type) {
	case *closeConnectionRequest:
		op.report(op.c.minq.Close())

	case *applicationCloseRequest:
		op.report(op.c.minq.Error(op.code, op.text))

	case *createStreamRequest:
		s := op.c.minq.CreateStream()
		op.result <- &Stream{SendStream{op.c, s}, RecvStream{op.c, s}}

	case *createSendStreamRequest:
		s := op.c.minq.CreateSendStream()
		op.result <- &SendStream{op.c, s}

	case *writeRequest:
		// fmt.Printf("%v %d > %x\n", op.s.c.minq.Role(), op.s.Id(), op.p)
		n, err := op.s.minq.Write(op.p)
		op.result <- &ioResult{n, err}

	case *closeStreamRequest:
		op.report(op.s.minq.Close())

	case *resetRequest:
		op.report(op.s.minq.Reset(op.code))

	case *readRequest:
		op.c.handleReadRequest(op)

	case *stopRequest:
		// Note that closing the channel shouldn't be necessary, but caution is
		// always welcome in these matters.
		readReq := op.c.readState[op.s.minq]
		if readReq != nil {
			close(readReq.result)
			delete(op.c.readState, op.s.minq)
		}
		op.report(op.s.minq.StopSending(op.code))

	default:
		// This covers the various state inquiry functions, which return
		// a valid result from the reporting function.
		op.report(ErrUnknownOperation)
	}
}

func (ops *connectionOperations) Close() error {
	ops.closeOnce.Do(func() {
		close(ops.closed)
		go ops.drain()
	})
	return nil
}

// Drain the channel so that any outstanding operations won't hang.
func (ops *connectionOperations) drain() {
	for op := range ops.ch {
		op.report(ErrConnectionClosed)
	}
}
