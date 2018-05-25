package minhq

import (
	"bytes"
	"errors"
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

	maxPushID    uint64
	pushLock     sync.Mutex
	pushChannels map[uint64]chan *ClientResponse
}

// NewClientConnection wraps an instance of minq.Connection.
func NewClientConnection(mwc *mw.Connection, config *Config) *ClientConnection {
	hq := &ClientConnection{
		connection: connection{
			Connection: *mwc,
		},
		pushChannels: make(map[uint64]chan *ClientResponse),
	}
	hq.Init(hq)
	hq.creditPushes(config.MaxConcurrentPushes)
	return hq
}

// HandleFrame is for dealing with those frames that Connection can't.
func (c *ClientConnection) HandleFrame(t FrameType, f byte, r FrameReader) error {
	return ErrInvalidFrame
}

// Fetch makes a request.
func (c *ClientConnection) Fetch(method string, target string, headers ...hc.HeaderField) (*ClientRequest, error) {
	<-c.Connected
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

func (c *ClientConnection) registerPushPromise(pp *PushPromise) <-chan *ClientResponse {
	defer c.pushLock.Unlock()
	c.pushLock.Lock()
	ch := c.pushChannels[pp.pushID]
	if ch == nil {
		ch = make(chan *ClientResponse)
		c.pushChannels[pp.pushID] = ch
	}
	return ch
}

func (c *ClientConnection) creditPushes(incr uint64) error {
	c.maxPushID += incr

	var buf bytes.Buffer
	w := NewFrameWriter(&buf)
	w.WriteVarint(c.maxPushID)
	_, err := c.controlStream.WriteFrame(frameMaxPushID, 0, buf.Bytes())
	return err
}
