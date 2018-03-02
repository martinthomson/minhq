package minhq

import (
	"github.com/ekr/minq"
	"github.com/martinthomson/minhq/mw"
)

type stream struct {
	FrameWriter
	FrameReader
	s *mw.Stream
}

func newStream(s *mw.Stream) *stream {
	return &stream{
		NewFrameWriter(s),
		NewFrameReader(s),
		s,
	}
}

func (s *stream) Close() error {
	return s.s.Close()
}

func (s *stream) Reset(err minq.ErrorCode) error {
	return s.s.Reset(err)
}
