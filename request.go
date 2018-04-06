package minhq

import (
	"bytes"
	"io"

	"github.com/martinthomson/minhq/hc"
)

type incomingMessageFrameHandler func(frameType, byte, io.Reader) error

// IncomingMessage is the common parts of inbound messages (requests for
// servers, responses for clients).
type IncomingMessage struct {
	decoder *hc.QcramDecoder
	Headers []hc.HeaderField
	concatenatingReader
	Trailers <-chan []hc.HeaderField
	trailers chan<- []hc.HeaderField
}

func newIncomingMessage(decoder *hc.QcramDecoder, headers []hc.HeaderField) IncomingMessage {
	trailers := make(chan []hc.HeaderField)
	return IncomingMessage{
		decoder: decoder,
		Headers: headers,
		concatenatingReader: concatenatingReader{
			current: nil,
			pending: make(chan io.Reader),
		},
		Trailers: trailers,
		trailers: trailers,
	}
}

func (msg *IncomingMessage) read(s *stream, frameHandler incomingMessageFrameHandler) error {
	done := false
	var err error
	for t, f, r, err := s.ReadFrame(); err == nil; {
		if done {
			return ErrInvalidFrame
		}

		switch t {
		case frameData:
			msg.concatenatingReader.Add(r)
		case frameHeaders:
			done = true
			headers, err := msg.decoder.ReadHeaderBlock(r)
			if err != nil {
				return err
			}
			msg.trailers <- headers
			close(msg.trailers)
		default:
			err := frameHandler(t, f, r)
			if err != nil {
				return err
			}
		}
	}
	if err == io.EOF {
		close(msg.trailers)
		msg.concatenatingReader.Close()
		return nil
	}
	return err
}

func writeHeaderBlock(encoder *hc.QcramEncoder, headersStream FrameWriter, requestStream FrameWriter, token interface{}) error {
	var controlBuf, headerBuf bytes.Buffer
	err := encoder.WriteHeaderBlock(&controlBuf, &headerBuf, token)
	if err != nil {
		return err
	}
	_, err = headersStream.WriteFrame(frameHeaders, 0, controlBuf.Bytes())
	if err != nil {
		return err
	}
	_, err = requestStream.WriteFrame(frameHeaders, 0, headerBuf.Bytes())
	return err
}

type concatenatingReader struct {
	current io.Reader
	pending chan io.Reader
}

func (cat *concatenatingReader) Add(r io.Reader) {
	cat.pending <- r
}

func (cat *concatenatingReader) Close() error {
	close(cat.pending)
	return nil
}

func (cat *concatenatingReader) next() bool {
	cat.current = <-cat.pending
	return cat.current != nil
}

func (cat *concatenatingReader) Read(p []byte) (int, error) {
	if cat.current == nil {
		if !cat.next() {
			return 0, io.EOF
		}
	}

	n, err := cat.current.Read(p)
	for err == io.EOF {
		if !cat.next() {
			return 0, io.EOF
		}
		n, err = cat.current.Read(p)
	}
	return n, err
}
