package minhq

import (
	"github.com/martinthomson/minhq/hc"
	"io"
	"net/url"
	"strconv"
)

type requestID struct {
	id    uint64
	index int
}

func (rid *requestID) Id() uint64 {
	return rid.id
}

// Request covers requests the client makes as well as push promises, both of
// these produce a response.
type Request interface {
	Method() string
	Target() *url.URL
	Headers() []hc.HeaderField
	Response() *ClientResponse
}

// ClientRequest is a representation of a request. A Connection will return one
// of these in response to a request to send a request. It is writable so that
// requests with bodies can be sent. A channel indicates where responses can be
// retrieved.
//
// To use this, make one using Connection.Fetch(). Write any body, then close
// the request with any trailers.
type ClientRequest struct {
	method string
	target *url.URL

	response <-chan *ClientResponse
	OutgoingMessage

	// Pushes is a feed of push promises.  Note that if pushes are not accepted
	// the response will not be available.  So if you don't want these, then
	// make sure to read and reject these using something like
	// `for pp := range req.Pushes { pp.Cancel() }`
	Pushes <-chan *PushPromise
	pushes chan<- *PushPromise
}

// Method returns the obvious thing.
func (req *ClientRequest) Method() string {
	return req.method
}

// Target returns the obvious thing.
func (req *ClientRequest) Target() *url.URL {
	return req.target
}

// Response awaits the response and returns it.
func (req *ClientRequest) Response() *ClientResponse {
	return <-req.response
}

func (req *ClientRequest) handlePushPromise(s *stream, c *ClientConnection, f byte, r io.Reader) error {
	if f != 0 {
		return ErrNonZeroFlags
	}
	fr := NewFrameReader(r)
	pushID, err := fr.ReadVarint()
	if err != nil {
		return err
	}

	headers, err := c.connection.decoder.ReadHeaderBlock(fr, s.Id())
	if err != nil {
		return err
	}
	err = fr.CheckForEOF()
	if err != nil {
		return err
	}

	pp := &PushPromise{
		headers: nil,
		pushID:  pushID,
	}
	err = pp.setHeaders(headers)
	if err != nil {
		return err
	}
	pp.Response = c.registerPushPromise(pp)
	req.pushes <- pp
	return nil
}

func (req *ClientRequest) readResponse(s *stream, c *ClientConnection,
	responseChannel chan<- *ClientResponse) {
	resp := &ClientResponse{
		Request:         req,
		IncomingMessage: newIncomingMessage(c.connection.decoder, nil),
	}
	err := resp.read(&s.recvStream, func(headers []hc.HeaderField) error {
		err := resp.setHeaders(headers)
		if err != nil {
			return err
		}
		responseChannel <- resp
		return nil
	}, func(t FrameType, f byte, r io.Reader) error {
		switch t {
		case framePushPromise:
			err := req.handlePushPromise(s, c, f, r)
			if err != nil {
				return err
			}
		default:
			return ErrUnsupportedFrame
		}
		return nil
	})
	if err != nil {
		s.abort()
		return
	}
}

// ClientResponse includes all that a client needs to handle a response.
type ClientResponse struct {
	Request Request
	IncomingMessage
	Status int
}

// setHeaders sets the header fields, and updates the Status field value.
func (resp *ClientResponse) setHeaders(headers []hc.HeaderField) error {
	resp.Headers = headers
	var err error
	resp.Status, err = strconv.Atoi(resp.GetHeader(":status"))
	return err
}

// PushPromise is what you get when you receive a push promise.
type PushPromise struct {
	headers headerFieldArray
	method  string
	target  *url.URL
	pushID  uint64

	Response <-chan *ClientResponse
}

// Method returns the obvious thing.
func (pp *PushPromise) Method() string {
	return pp.method
}

// Target returns the obvious thing.
func (pp *PushPromise) Target() *url.URL {
	return pp.target
}

func (pp *PushPromise) Headers() []hc.HeaderField {
	return pp.headers[:]
}

func (pp *PushPromise) setHeaders(h []hc.HeaderField) error {
	pp.headers = h
	var err error
	pp.method, pp.target, err = pp.headers.getMethodAndTarget()
	return err
}

func (pp *PushPromise) Cancel() error {
	// TODO
	return nil
}
