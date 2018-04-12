package minhq

import (
	"github.com/ekr/minq"
)

type stream struct {
	sendStream2
	recvStream
}

var _ minq.Stream = &stream{}

func newStream(s minq.Stream) *stream {
	return &stream{
		sendStream2: sendStream2{NewFrameWriter(s), s},
		recvStream:  recvStream{NewFrameReader(s), s},
	}
}

// Id is needed to resolve an ambiguity between sendStream and recvStream.
func (s *stream) Id() uint64 {
	return s.sendStream2.Id()
}

// abort is the option of last resort.
func (s *stream) abort() {
	s.Reset(ErrQuicWtf)
	s.StopSending(ErrQuicWtf) // TODO change error codes to something sensible.
}

type sendStream2 struct {
	FrameWriter
	minq.SendStream
}

var _ minq.SendStream = &sendStream2{}

func newSendStream(s minq.SendStream) *sendStream2 {
	return &sendStream2{NewFrameWriter(s), s}
}

func (s *sendStream2) Write(p []byte) (int, error) {
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
