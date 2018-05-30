package minhq

import (
	"bytes"
	"errors"
	"io"
	"net/url"
	"strings"

	"github.com/martinthomson/minhq/hc"
)

func buildRequestHeaderFields(method string, base *url.URL, target string, headers []hc.HeaderField) (*url.URL, []hc.HeaderField, error) {
	var u *url.URL
	var err error
	if base != nil {
		u, err = base.Parse(target)
	} else {
		u, err = url.Parse(target)
	}
	if err != nil {
		return nil, nil, err
	}
	if u.Scheme != "https" {
		return nil, nil, errors.New("No support for non-https URLs")
	}

	return u, append([]hc.HeaderField{
		hc.HeaderField{Name: ":authority", Value: u.Host},
		hc.HeaderField{Name: ":path", Value: u.EscapedPath()},
		hc.HeaderField{Name: ":method", Value: method},
		hc.HeaderField{Name: ":scheme", Value: u.Scheme},
	}, headers...), nil
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
		s += h.String() + "\n"
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

func (h headerFieldArray) getMethodAndTarget() (string, *url.URL, error) {
	method := h.GetHeader(":method")
	if method == "" {
		return "", nil, errors.New("Missing :method from request")
	}

	u := url.URL{
		Scheme: h.GetHeader(":scheme"),
		Host:   h.GetHeader(":authority"),
	}
	if u.Scheme == "" {
		return "", nil, errors.New("Missing :scheme from request")
	}
	if u.Host == "" {
		u.Host = h.GetHeader("Host")
	}
	if u.Host == "" {
		return "", nil, errors.New("Missing :authority/Host from request")
	}
	p := h.GetHeader(":path")
	if p == "" {
		return "", nil, errors.New("Missing :path from request")
	}
	// Let url.Parse() handle all the nasty corner cases in path syntax.
	withPath, err := u.Parse(p)
	if err != nil {
		return "", nil, err
	}
	return method, withPath, nil
}

type initialHeadersHandler func(headers []hc.HeaderField) error
type incomingMessageFrameHandler func(FrameType, byte, io.Reader) error

// IncomingMessage is the common parts of inbound messages (requests for
// servers, responses for clients).
type IncomingMessage struct {
	s       *recvStream
	decoder *hc.QpackDecoder
	Headers headerFieldArray
	concatenatingReader
	Trailers <-chan []hc.HeaderField
	trailers chan<- []hc.HeaderField
}

func newIncomingMessage(s *recvStream, decoder *hc.QpackDecoder, headers []hc.HeaderField) IncomingMessage {
	trailers := make(chan []hc.HeaderField)
	return IncomingMessage{
		s:       s,
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

func (msg *IncomingMessage) read(headersHandler initialHeadersHandler,
	frameHandler incomingMessageFrameHandler) error {
	defer close(msg.trailers)
	defer msg.concatenatingReader.Close()

	err := func() error {
		beforeFirstHeaders := true
		afterTrailers := false
		for {
			t, f, r, err := msg.s.ReadFrame()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			if afterTrailers {
				return ErrInvalidFrame
			}

			switch t {
			case frameData:
				if beforeFirstHeaders {
					return ErrInvalidFrame
				}
				msg.concatenatingReader.Add(r)

			case frameHeaders:
				if f != 0 {
					return ErrInvalidFrame
				}
				headers, err := msg.decoder.ReadHeaderBlock(r, msg.s.Id())
				if err != nil {
					return err
				}

				if beforeFirstHeaders {
					err = headersHandler(headers)
					if err != nil {
						return err
					}
					beforeFirstHeaders = false
				} else {
					msg.trailers <- headers
					afterTrailers = true
				}

			default:
				err := frameHandler(t, f, r)
				if err != nil {
					return err
				}
			}
		}
	}()
	if err != nil {
		msg.decoder.Cancelled(msg.s.Id())
	}
	return err
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
	headers headerFieldArray

	s *sendStream

	// encoder is needed for encoding trailers (ugh)
	encoder *hc.QpackEncoder
}

var _ io.WriteCloser = &OutgoingMessage{}

func newOutgoingMessage(c *connection, s *sendStream, headers []hc.HeaderField) OutgoingMessage {
	return OutgoingMessage{
		headers: headers,
		s:       s,
		encoder: c.encoder,
	}
}

func (msg *OutgoingMessage) Headers() []hc.HeaderField {
	return msg.headers[:]
}

// Write fulfils the io.Writer contract.
func (msg *OutgoingMessage) Write(p []byte) (int, error) {
	// Note that WriteFrame always uses the entire input array, and it reports
	// how much it wrote, not how much it used.  It always uses the entire
	// input array.  That's not the io.Writer contract, so adapt.
	_, err := msg.s.WriteFrame(frameData, 0, p)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

func (msg *OutgoingMessage) writeHeaderBlock(headers []hc.HeaderField) error {
	// TODO: ensure that header blocks are properly dropped if the stream is reset.
	var headerBuf bytes.Buffer
	err := msg.encoder.WriteHeaderBlock(&headerBuf, msg.s.Id(), headers...)
	if err != nil {
		return err
	}
	_, err = msg.s.WriteFrame(frameHeaders, 0, headerBuf.Bytes())
	return err
}

// End closes out the stream, writing any trailers that might be included.
func (msg *OutgoingMessage) End(trailers []hc.HeaderField) error {
	if trailers != nil {
		err := msg.writeHeaderBlock(trailers)
		if err != nil {
			return err
		}
	}
	return msg.Close()
}

// Close allows OutgoingMessage to implement io.WriteCloser.
func (msg *OutgoingMessage) Close() error {
	return msg.s.Close()
}

// String just formats headers.
func (msg *OutgoingMessage) String() string {
	return msg.headers.String()
}
