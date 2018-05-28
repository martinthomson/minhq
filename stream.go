package minhq

import (
	"github.com/ekr/minq"
)

type stream struct {
	sendStream
	recvStream
}

var _ minq.Stream = &stream{}

func newStream(s minq.Stream) *stream {
	return &stream{
		sendStream: sendStream{NewFrameWriter(s), s},
		recvStream: recvStream{NewFrameReader(s), s},
	}
}

// Id is needed to resolve an ambiguity between sendStream and recvStream.
func (s *stream) Id() uint64 {
	return s.sendStream.Id()
}

// abort is the option of last resort.
func (s *stream) abort() {
	s.Reset(uint16(ErrHttpInternalError))
	s.StopSending(uint16(ErrHttpInternalError))
}

type sendStream struct {
	FrameWriter
	minq.SendStream
}

var _ minq.SendStream = &sendStream{}

func newSendStream(s minq.SendStream) *sendStream {
	return &sendStream{NewFrameWriter(s), s}
}

func (s *sendStream) Write(p []byte) (int, error) {
	return s.FrameWriter.Write(p)
}

type recvStream struct {
	FrameReader
	minq.RecvStream
}

var _ minq.RecvStream = &recvStream{}

func newRecvStream(s minq.RecvStream) *recvStream {
	return &recvStream{NewFrameReader(s), s}
}

func (s *recvStream) Read(p []byte) (int, error) {
	return s.FrameReader.Read(p)
}
