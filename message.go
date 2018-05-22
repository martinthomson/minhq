package minhq

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

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

func writeHeaderBlock(encoder *hc.QpackEncoder, headersStream FrameWriter, requestStream FrameWriter,
	token interface{}, headers []hc.HeaderField) error {
	var controlBuf, headerBuf bytes.Buffer
	err := encoder.WriteHeaderBlock(&controlBuf, &headerBuf, token, headers...)
	if err != nil {
		return err
	}
	if controlBuf.Len() > 0 {
		_, err = headersStream.WriteFrame(frameHeaders, 0, controlBuf.Bytes())
		if err != nil {
			return err
		}
	}
	_, err = requestStream.WriteFrame(frameHeaders, 0, headerBuf.Bytes())
	return err
}

type headerFieldArray []hc.HeaderField

func (a headerFieldArray) String() string {
	w := 0
	for _, h := range a {
		if len(h.Name) > w {
			w = len(h.Name)
		}
	}
	s := ""
	for _, h := range a {
		s += fmt.Sprintf(fmt.Sprintf("%%%ds: %%s\n", w), h.Name, h.Value)
	}
	return s
}

// GetHeader performs a case-insensitive lookup for a given name.
// This returns an empty string if the header field wasn't present.
// Multiple values are concatenated using commas.
func (a headerFieldArray) GetHeader(n string) string {
	v := ""
	for _, h := range a {
		// Incoming messages have all lowercase names.
		if h.Name == strings.ToLower(n) {
			if len(v) > 0 {
				v += "," + h.Value
			} else {
				v = h.Value
			}
		}
	}
	return v
}

type incomingMessageFrameHandler func(FrameType, byte, io.Reader) error

// IncomingMessage is the common parts of inbound messages (requests for
// servers, responses for clients).
type IncomingMessage struct {
	decoder *hc.QpackDecoder
	Headers headerFieldArray
	concatenatingReader
	Trailers <-chan []hc.HeaderField
	trailers chan<- []hc.HeaderField
}

func newIncomingMessage(decoder *hc.QpackDecoder, headers []hc.HeaderField) IncomingMessage {
	trailers := make(chan []hc.HeaderField)
	return IncomingMessage{
		decoder: decoder,
		Headers: headers,
		concatenatingReader: concatenatingReader{
			current: nil,
			pending: make(chan io.Reader),
			drained: make(chan struct{}),
		},
		Trailers: trailers,
		trailers: trailers,
	}
}

func (msg *IncomingMessage) read(fr FrameReader, frameHandler incomingMessageFrameHandler) error {
	defer close(msg.trailers)
	defer msg.concatenatingReader.Close()

	done := false
	for {
		t, f, r, err := fr.ReadFrame()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
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
		default:
			err := frameHandler(t, f, r)
			if err != nil {
				return err
			}
		}
	}
}

// GetHeader performs a case-insensitive lookup for a given name.
// This returns an empty string if the header field wasn't present.
// Multiple values are concatenated using commas.
func (msg *IncomingMessage) GetHeader(n string) string {
	return msg.Headers.GetHeader(n)
}

// String just formats headers.
func (msg *IncomingMessage) String() string {
	return msg.Headers.String()
}

type concatenatingReader struct {
	current io.Reader
	pending chan io.Reader
	drained chan struct{}
}

// Add adds a reader, then holds until it is fully drained.
func (cat *concatenatingReader) Add(r io.Reader) {
	cat.pending <- r
	<-cat.drained
}

func (cat *concatenatingReader) Close() error {
	close(cat.pending)
	return nil
}

func (cat *concatenatingReader) next() bool {
	if cat.current != nil {
		cat.drained <- struct{}{}
	}
	cat.current = <-cat.pending
	return cat.current != nil
}

// Read can be called from any thread, but only one thread.
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
	Headers   headerFieldArray

	writeStream FrameWriteCloser

	// This stuff is all needed for trailers (ugh).
	encoder       *hc.QpackEncoder
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

// Write fulfils the io.Writer contract.
func (msg *OutgoingMessage) Write(p []byte) (int, error) {
	// Note that WriteFrame always uses the entire input array, and it reports
	// how much it wrote, not how much it used.  It always uses the entire
	// input array.  That's not the io.Writer contract, so adapt.
	_, err := msg.writeStream.WriteFrame(frameData, 0, p)
	if err != nil {
		return 0, err
	}
	return len(p), nil
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

// String just formats headers.
func (msg *OutgoingMessage) String() string {
	return msg.Headers.String()
}
