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

	maxPushID uint64
	pushLock  sync.Mutex
	promises  map[uint64]*promiseTracker
}

// NewClientConnection wraps an instance of minq.Connection.
func NewClientConnection(mwc *mw.Connection, config *Config) *ClientConnection {
	hq := &ClientConnection{
		connection: connection{
			Connection: *mwc,
		},
		promises: make(map[uint64]*promiseTracker),
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
	tracker := c.promises[pp.pushID]
	if tracker == nil {
		tracker = &promiseTracker{
			promises:        []*PushPromise{pp},
			responseChannel: make(chan *ClientResponse),
			response:        nil,
		}
		c.promises[pp.pushID] = tracker
	} else {
		tracker.Add(pp)
	}
	return tracker.responseChannel
}

func (c *ClientConnection) creditPushes(incr uint64) error {
	c.maxPushID += incr

	var buf bytes.Buffer
	w := NewFrameWriter(&buf)
	w.WriteVarint(c.maxPushID)
	_, err := c.controlStream.WriteFrame(frameMaxPushID, 0, buf.Bytes())
	return err
}

// promiseTracker looks after push promises.
type promiseTracker struct {
	promises        []*PushPromise
	responseChannel chan *ClientResponse
	response        *ClientResponse
}

// Add adds a push promise to an existing tracker entry.
func (pt *promiseTracker) Add(pp *PushPromise) {
	pt.promises = append(pt.promises, pp)
}

// Fulfill fills out the response and lets all the promises know about it.
func (pt *promiseTracker) Fulfill(resp *ClientResponse) {
	pt.response = resp
	pt.responseChannel <- resp
	close(pt.responseChannel)
}
