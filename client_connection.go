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
	hq := &ClientConnection{
		connection: connection{
			config:     config,
			Connection: *mwc,
			ready:      make(chan struct{}),
		},
		promises: make(map[uint64]*PushPromise),
	}
	err := hq.init(hq)
	if err != nil {
		return nil
	}
	hq.creditPushes(config.MaxConcurrentPushes)
	return hq
}

// HandleFrame is for dealing with those frames that Connection can't.
func (c *ClientConnection) HandleFrame(t FrameType, f byte, r FrameReader) error {
	return ErrInvalidFrame
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
		promise.fulfill(resp)
		return nil
	}, func(t FrameType, f byte, r io.Reader) error {
		return ErrUnsupportedFrame
	})
	if err != nil {
		c.FatalError(0)
		return
	}
	c.creditPushes(1)
}

func (c *ClientConnection) HandleUnidirectionalStream(t unidirectionalStreamType, s *recvStream) {
	switch t {
	case unidirectionalStreamPush:
		c.handlePushStream(s)
	default:
		s.StopSending(0)
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
