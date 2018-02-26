package minhq

import (
	"io"

	"github.com/ekr/minq"
)

type minqReadWrapper struct {
	r        io.Reader
	readable <-chan struct{}
}

func (w *minqReadWrapper) Read(p []byte) (int, error) {
	n, err := w.r.Read(p)
	if err == minq.ErrorWouldBlock {
		<-w.readable
		n, err = w.r.Read(p)
	}
	return n, err
}

type stream struct {
	FrameWriter
	FrameReader
	s *minq.Stream
}

func newStream(s *minq.Stream, readable <-chan struct{}) *stream {
	return &stream{
		NewFrameWriter(s),
		NewFrameReader(&minqReadWrapper{s, readable}),
		s,
	}
}

func (s *stream) Close() error {
	s.s.Close()
	return nil
}

func (s *stream) Reset(err minq.ErrorCode) error {
	return s.s.Reset(err)
}
