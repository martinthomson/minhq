package minhq

import (
	"bytes"
	"errors"
	"io"
	"sync"

	"github.com/ekr/minq"
	"github.com/martinthomson/minhq/hc"
	"github.com/martinthomson/minhq/mw"
)

// ErrStreamBlocked is used to indicate that there are no streams available.
// TODO: consider blocking until a stream is available.
var ErrStreamBlocked = errors.New("Unable to open a new stream for the request")

// ClientConnection is a connection specialized for use by clients.
type ClientConnection struct {
	connection

	maxPushID uint64
	pushLock  sync.Mutex
	promises  map[uint64]*PushPromise
}

// NewClientConnection wraps an instance of minq.Connection.
func NewClientConnection(mwc *mw.Connection, config *Config) *ClientConnection {
	return &ClientConnection{
		connection: connection{
			config:     config,
			Connection: *mwc,
			ready:      make(chan struct{}),
		},
		promises: make(map[uint64]*PushPromise),
	}
}

// Connect waits until the connection is setup and ready.
func (c *ClientConnection) Connect() error {
	err := c.connect(c)
	if err != nil {
		return err
	}
	c.creditPushes(c.config.MaxConcurrentPushes)
	return nil
}

func (c *ClientConnection) handleCancelPush(r FrameReader) error {
	pushID, err := r.ReadVarint()
	if err != nil {
		return err
	}
	err = r.CheckForEOF()
	if err != nil {
		return err
	}
	promise := c.getPushPromise(pushID)
	promise.fulfill(nil, true)
	return nil
}

// HandleFrame is for dealing with those frames that Connection can't.
func (c *ClientConnection) HandleFrame(t FrameType, r FrameReader) error {
	switch t {
	case frameCancelPush:
		return c.handleCancelPush(r)
	default:
		return ErrInvalidFrame
	}
}

// Fetch makes a request.
func (c *ClientConnection) Fetch(method string, target string, headers ...hc.HeaderField) (*ClientRequest, error) {
	<-c.ready
	if c.GetState() != minq.StateEstablished {
		return nil, errors.New("connection not open")
	}

	url, allHeaders, err := buildRequestHeaderFields(method, nil, target, headers)
	if err != nil {
		return nil, err
	}

	s := newStream(c.CreateStream())
	if s == nil {
		return nil, ErrStreamBlocked
	}

	responseChannel := make(chan *ClientResponse)
	pushes := make(chan *PushPromise)
	var informational chan *InformationalResponse
	if c.config.InformationalResponses {
		informational = make(chan *InformationalResponse)
	}
	req := &ClientRequest{
		method:                 method,
		target:                 url,
		response:               responseChannel,
		OutgoingMessage:        newOutgoingMessage(&c.connection, &s.sendStream, allHeaders),
		Pushes:                 pushes,
		pushes:                 pushes,
		InformationalResponses: informational,
		informationalResponses: informational,
	}

	err = req.writeHeaderBlock(allHeaders)
	if err != nil {
		return nil, err
	}

	go req.readResponse(s, c, responseChannel)
	return req, nil
}

func (c *ClientConnection) getPushPromise(pushID uint64) *PushPromise {
	defer c.pushLock.Unlock()
	c.pushLock.Lock()
	promise := c.promises[pushID]
	if promise == nil {
		promise = &PushPromise{pushID: pushID, responseChannel: make(chan *ClientResponse)}
		c.promises[pushID] = promise
	}
	return promise
}

func (c *ClientConnection) handlePushStream(s *recvStream) error {
	pushID, err := s.ReadVarint()
	if err != nil {
		return err
	}

	promise := c.getPushPromise(pushID)
	if promise.isFulfilled() {
		return errors.New("double fulfilment of promise")
	}

	resp := &ClientResponse{
		Request:         nil,
		IncomingMessage: newIncomingMessage(s, c.connection.decoder, nil),
	}

	err = resp.read(func(headers headerFieldArray) (bool, error) {
		resp.setHeaders(headers)
		switch headers.GetStatus() / 100 {
		case 0:
			return false, errors.New("invalid or missing status")
		case 1:
			if promise.informationalResponses != nil {
				promise.informationalResponses <- &InformationalResponse{headers.GetStatus(), headers}
			}
			return false, nil
		default:
			promise.fulfill(resp, false)
			return true, nil
		}
	}, func(t FrameType, r io.Reader) error {
		return ErrUnsupportedFrame
	})
	if err != nil {
		return err
	}
	c.creditPushes(1)
	return nil
}

// HandleUnidirectionalStream manages receipt of a new unidirectional stream.
// For clients, that's just push for now.
func (c *ClientConnection) HandleUnidirectionalStream(t unidirectionalStreamType, s *recvStream) error {
	switch t {
	case unidirectionalStreamPush:
		return c.handlePushStream(s)
	default:
		return s.StopSending(uint16(ErrHttpUnknownStreamType))
	}
}

func (c *ClientConnection) creditPushes(incr uint64) error {
	c.maxPushID += incr

	var buf bytes.Buffer
	w := NewFrameWriter(&buf)
	w.WriteVarint(c.maxPushID)
	_, err := c.controlStream.WriteFrame(frameMaxPushID, buf.Bytes())
	return err
}
