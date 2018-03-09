package mw

import (
	"errors"

	"github.com/ekr/minq"
)

// ErrUnidirectional is used to signal that an operation that isn't supported on
// a unidirectional stream of the current type was attempted.
var ErrUnidirectional = errors.New("Operation not supported on this stream type")

// TODO: structure this more sensibly, so that SendStream and RecvStream don't
// implement each other. That requires more breaking down that I'm prepared for
// right now.

// Stream is a wrapper around minq.Stream.
type Stream struct {
	c    *Connection
	id   uint64
	send minq.SendStream
	recv minq.RecvStream
}

func (ms *Stream) io(channel chan<- *ioRequest, p []byte) (int, error) {
	result := make(chan *ioResult)
	req := &ioRequest{ms.c, ms, p, result}
	channel <- req
	resp := <-result
	return resp.n, resp.err
}

// Id calls minq.Stream.Id() directly on the assumption that this value is immutable.
func (ms *Stream) Id() uint64 {
	return ms.id
}

// Read implements the io.Reader interface.
func (ms *Stream) Read(p []byte) (int, error) {
	return ms.io(ms.c.ops.read, p)
}

// Write implements the io.Writer interface.
func (ms *Stream) Write(p []byte) (int, error) {
	return ms.io(ms.c.ops.write, p)
}

// Reset kills a stream (outbound only).
func (ms *Stream) Reset(err minq.ErrorCode) error {
	result := make(chan error)
	ms.c.ops.reset <- &resetRequest{ms.c, ms, err, result}
	return <-result
}

// StopSending currently does nothing because minq doesn't support it.
func (ms *Stream) StopSending() error {
	result := make(chan error)
	ms.c.ops.stopSending <- &stopRequest{ms.c, ms, result}
	return <-result
}

// Close implements io.Closer, but it only affects the write side (I think).
func (ms *Stream) Close() error {
	result := make(chan error)
	ms.c.ops.closeStream <- &stopRequest{ms.c, ms, result}
	return <-result
}

// SendStream is a stream with receive parts disabled.  Pure sugar.
type SendStream struct {
	Stream
}

// Read is disabled.
func (ms *SendStream) Read(p []byte) (int, error) {
	return 0, ErrUnidirectional
}

// StopSending is disabled.
func (ms *SendStream) StopSending() error {
	return ErrUnidirectional
}

// RecvStream is a stream with send parts disabled.  Pure sugar.
type RecvStream struct {
	Stream
}

// Write is disabled.
func (ms *RecvStream) Write(p []byte) (int, error) {
	return 0, ErrUnidirectional
}

// Reset is disabled.
func (ms *RecvStream) Reset(minq.ErrorCode) error {
	return ErrUnidirectional
}

// Close is disabled.
func (ms *RecvStream) Close() error {
	return ErrUnidirectional
}
