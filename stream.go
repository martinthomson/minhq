package minhq

import (
	"github.com/ekr/minq"
)

type stream struct {
	sendStream
	recvStream
}

func newStream(s minq.Stream) *stream {
	return &stream{
		sendStream: sendStream{NewFrameWriter(s), s},
		recvStream: recvStream{NewFrameReader(s), s},
	}
}

var _ minq.Stream = &stream{}

type sendStream struct {
	FrameWriter
	s minq.SendStream
}

func newSendStream(s minq.SendStream) *sendStream {
	return &sendStream{
		NewFrameWriter(s),
		s,
	}
}

func (s *sendStream) Close() error {
	return s.s.Close()
}

func (s *sendStream) Reset(code minq.ErrorCode) error {
	return s.s.Reset(code)
}

type recvStream struct {
	FrameReader
	s minq.RecvStream
}

func newRecvStream(s minq.RecvStream) *recvStream {
	return &recvStream{
		NewFrameReader(s),
		s,
	}
}

func (s *recvStream) StopSending(code minq.ErrorCode) error {
	return s.s.StopSending(code)
}
