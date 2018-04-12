package minhq

import (
	"bytes"
	"errors"
	"io"
	"net/url"

	"github.com/martinthomson/minhq/hc"
)

func buildRequestHeaderFields(method string, target string, headers []hc.HeaderField) ([]hc.HeaderField, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "https" {
		return nil, errors.New("No support for non-https URLs")
	}

	return append([]hc.HeaderField{
		hc.HeaderField{Name: ":authority", Value: u.Host},
		hc.HeaderField{Name: ":path", Value: u.EscapedPath()},
		hc.HeaderField{Name: ":method", Value: method},
		hc.HeaderField{Name: ":scheme", Value: u.Scheme},
	}, headers...), nil
}

func writeHeaderBlock(encoder *hc.QcramEncoder, headersStream FrameWriter, requestStream FrameWriter,
	token interface{}, headers []hc.HeaderField) error {
	var controlBuf, headerBuf bytes.Buffer
	err := encoder.WriteHeaderBlock(&controlBuf, &headerBuf, token, headers...)
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

type incomingMessageFrameHandler func(FrameType, byte, io.Reader) error

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

func (msg *IncomingMessage) read(fr FrameReader, frameHandler incomingMessageFrameHandler) error {
	done := false
	var err error
	for t, f, r, err := fr.ReadFrame(); err == nil; {
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

// OutgoingMessage contains the common parts of outgoing messages (requests for
// clients, responses for servers).
type OutgoingMessage struct {
	requestID *requestID
	Headers   []hc.HeaderField

	writeStream FrameWriteCloser

	// This stuff is all needed for trailers (ugh).
	encoder       *hc.QcramEncoder
	headersStream FrameWriter
	outstanding   *outstandingHeaders
}

func newOutgoingMessage(c *connection, s FrameWriteCloser, requestID *requestID, headers []hc.HeaderField) OutgoingMessage {
	return OutgoingMessage{
		requestID:     requestID,
		Headers:       headers,
		writeStream:   s,
		encoder:       c.encoder,
		headersStream: c.headersStream,
		outstanding:   &c.outstanding,
	}
}

var _ io.WriteCloser = &OutgoingMessage{}

func (msg *OutgoingMessage) Write(p []byte) (int, error) {
	return msg.writeStream.WriteFrame(frameData, 0, p)
}

// End closes out the stream, writing any trailers that might be included.
func (msg *OutgoingMessage) End(trailers []hc.HeaderField) error {
	if trailers != nil {
		err := writeHeaderBlock(msg.encoder, msg.headersStream, msg.writeStream,
			msg.outstanding.add(msg.requestID), trailers)
		if err != nil {
			return err
		}
	}
	return msg.Close()
}

// Close allows OutgoingMessage to implement io.WriteCloser.
func (msg *OutgoingMessage) Close() error {
	return msg.writeStream.Close()
}
