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

func (c *ClientConnection) handleCancelPush(f byte, r FrameReader) error {
	if f != 0 {
		return ErrNonZeroFlags
	}
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
func (c *ClientConnection) HandleFrame(t FrameType, f byte, r FrameReader) error {
	switch t {
	case frameCancelPush:
		return c.handleCancelPush(f, r)
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
	req := &ClientRequest{
		method:          method,
		target:          url,
		response:        responseChannel,
		OutgoingMessage: newOutgoingMessage(&c.connection, &s.sendStream, allHeaders),
		Pushes:          pushes,
		pushes:          pushes,
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

func (c *ClientConnection) handlePushStream(s *recvStream) {
	pushID, err := s.ReadVarint()
	if err != nil {
		c.FatalError(ErrWtf)
		return
	}

	promise := c.getPushPromise(pushID)
	if promise.isFulfilled() {
		c.FatalError(ErrWtf)
		return
	}

	resp := &ClientResponse{
		Request:         nil,
		IncomingMessage: newIncomingMessage(s, c.connection.decoder, nil),
	}

	err = resp.read(func(headers []hc.HeaderField) error {
		err := resp.setHeaders(headers)
		if err != nil {
			return err
		}
		promise.fulfill(resp, false)
		return nil
	}, func(t FrameType, f byte, r io.Reader) error {
		return ErrUnsupportedFrame
	})
	if err != nil {
		return
	}
	c.creditPushes(1)
}

// HandleUnidirectionalStream manages receipt of a new unidirectional stream.
// For clients, that's just push for now.
func (c *ClientConnection) HandleUnidirectionalStream(t unidirectionalStreamType, s *recvStream) {
	switch t {
	case unidirectionalStreamPush:
		c.handlePushStream(s)
	default:
		s.StopSending(uint16(ErrHttpUnknownStreamType))
	}
}

func (c *ClientConnection) creditPushes(incr uint64) error {
	c.maxPushID += incr

	var buf bytes.Buffer
	w := NewFrameWriter(&buf)
	w.WriteVarint(c.maxPushID)
	_, err := c.controlStream.WriteFrame(frameMaxPushID, 0, buf.Bytes())
	return err
}
