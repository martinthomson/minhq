package mw

import (
	"errors"

	"github.com/ekr/minq"
)

// ErrUnidirectional is used to signal that an operation that isn't supported on
// a unidirectional stream of the current type was attempted.
var ErrUnidirectional = errors.New("Operation not supported on this stream type")

// SendStream wraps minq.SendStream.
type SendStream struct {
	c    *Connection
	minq minq.SendStream
}

var _ minq.SendStream = &SendStream{}

// Id calls minq.SendStream.Id() directly on the assumption that this value is immutable.
func (s *SendStream) Id() uint64 {
	return s.minq.Id()
}

// SendState proxies a request for the current state.
func (s *SendStream) SendState() minq.SendStreamState {
	result := make(chan minq.SendStreamState)
	s.c.ops.Add(&getSendStateRequest{s, result})
	return <-result
}

// Write implements the io.Writer interface.
func (s *SendStream) Write(p []byte) (int, error) {
	result := make(chan *ioResult)
	s.c.ops.Add(&writeRequest{ioRequest{s.c, p, result}, s})
	resp := <-result
	return resp.n, resp.err
}

// Reset kills a stream (outbound only).
func (s *SendStream) Reset(err minq.ErrorCode) error {
	result := make(chan error)
	s.c.ops.Add(&resetRequest{s.c, s, err, reportErrorChannel{result}})
	return <-result
}

// Close implements io.Closer, but it only affects the write side (I think).
func (s *SendStream) Close() error {
	result := make(chan error)
	s.c.ops.Add(&closeStreamRequest{s.c, s, reportErrorChannel{result}})
	return <-result
}

// RecvStream wraps minq.RecvStream.
type RecvStream struct {
	c    *Connection
	minq minq.RecvStream
}

// Id calls minq.SendStream.Id() directly on the assumption that this value is immutable.
func (s *RecvStream) Id() uint64 {
	return s.minq.Id()
}

// RecvState proxies a request for the current state.
func (s *RecvStream) RecvState() minq.RecvStreamState {
	result := make(chan minq.RecvStreamState)
	s.c.ops.Add(&getRecvStateRequest{s, result})
	return <-result
}

// Read implements the io.Reader interface.
func (s *RecvStream) Read(p []byte) (int, error) {
	result := make(chan *ioResult)
	s.c.ops.Add(&readRequest{ioRequest{s.c, p, result}, s})
	resp := <-result
	return resp.n, resp.err
}

// StopSending currently does nothing because minq doesn't support it.
func (s *RecvStream) StopSending(code uint16) error {
	result := make(chan error)
	s.c.ops.Add(&stopRequest{s.c, s, code, reportErrorChannel{result}})
	return <-result
}

// Stream is a wrapper around minq.Stream.
type Stream struct {
	SendStream
	RecvStream
}

var _ minq.Stream = &Stream{}

// Id calls into SendStream (both SendStream and RecvStream should produce the same answer).
func (s *Stream) Id() uint64 {
	return s.SendStream.Id()
}
