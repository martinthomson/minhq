package minhq

import (
	"bytes"
	"errors"
	"io"
	"net/url"
	"sync"

	"github.com/martinthomson/minhq/hc"
)

// ErrInvalidPushPromise occurs if a push promise isn't well formed.
var ErrInvalidPushPromise = errors.New("invalid push promise")

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

	pp := c.getPushPromise(pushID)
	err = pp.setHeaders(headers)
	if err != nil {
		return err
	}

	req.pushes <- pp
	return nil
}

func (req *ClientRequest) readResponse(s *stream, c *ClientConnection,
	responseChannel chan<- *ClientResponse) {
	resp := &ClientResponse{
		Request:         req,
		IncomingMessage: newIncomingMessage(&s.recvStream, c.connection.decoder, nil),
	}
	err := resp.read(func(headers headerFieldArray) (bool, error) {
		is1xx := (headers.GetStatus() / 100) == 1
		resp.setHeaders(headers)
		if resp.Status == 0 {
			return false, errors.New("invalid or missing status")
		}
		responseChannel <- resp
		return !is1xx, nil
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
	close(req.pushes)
}

// ClientResponse includes all that a client needs to handle a response.
type ClientResponse struct {
	Request Request
	IncomingMessage
	Status int
}

// setHeaders sets the header fields, and updates the Status field value.
func (resp *ClientResponse) setHeaders(headers headerFieldArray) {
	resp.Headers = headers
	resp.Status = headers.GetStatus()
}

// PushPromise is what you get when you receive a push promise.
// Note that the same object is returned to different requests if the server
// promises with the same identifier in response to multiple requests.  See
// Response() for the consequences of that.
type PushPromise struct {
	c *ClientConnection

	headersLock sync.RWMutex
	headers     headerFieldArray
	method      string
	target      *url.URL
	pushID      uint64

	responseLock    sync.RWMutex
	responseChannel chan *ClientResponse
	response        *ClientResponse
	cancelled       bool
}

// Method returns the obvious thing.
func (pp *PushPromise) Method() string {
	defer pp.headersLock.RUnlock()
	pp.headersLock.RLock()
	return pp.method
}

// Target returns the obvious thing.
func (pp *PushPromise) Target() *url.URL {
	defer pp.headersLock.RUnlock()
	pp.headersLock.RLock()
	return pp.target
}

// Headers returns the headers from the promise.
func (pp *PushPromise) Headers() []hc.HeaderField {
	defer pp.headersLock.RUnlock()
	pp.headersLock.RLock()
	return pp.headers[:]
}

func (pp *PushPromise) setHeaders(h []hc.HeaderField) error {
	defer pp.headersLock.Unlock()
	pp.headersLock.Lock()
	pp.headers = h
	var err error
	pp.method, pp.target, err = pp.headers.getMethodAndTarget()
	return err
}

func (pp *PushPromise) fulfill(resp *ClientResponse, cancelled bool) {
	defer pp.responseLock.Unlock()
	pp.responseLock.Lock()
	pp.responseChannel <- resp
	pp.response = resp
	pp.cancelled = cancelled
	close(pp.responseChannel)
}

func (pp *PushPromise) isFulfilled() bool {
	defer pp.responseLock.RUnlock()
	pp.responseLock.RLock()
	return pp.response != nil || pp.cancelled
}

// Response returns a response.  Note that because multiple push promises
// can be made for the same response, only one call to this function will
// receive a response.  Others receive a nil value.  This prevents
// concurrent reads of the response body.
// If the push is cancelled, this returns nil.
func (pp *PushPromise) Response() *ClientResponse {
	return <-pp.responseChannel
}

// Cancel cancels the push promise, either by sending CANCEL_PUSH, or by
// stopping the stream if it has already started to arrive.
func (pp *PushPromise) Cancel() error {
	if pp.isFulfilled() {
		return pp.response.s.StopSending(uint16(ErrHttpRequestCancelled))
	}

	var buf bytes.Buffer
	_, err := NewFrameWriter(&buf).WriteVarint(pp.pushID)
	if err != nil {
		return err
	}
	_, err = pp.c.controlStream.WriteFrame(frameCancelPush, 0, buf.Bytes())
	return err
}
