package minhq

import (
	"errors"
	"sync"

	"github.com/martinthomson/minhq/mw"
)

// ServerConnection specializes Connection with server-related functions.
type ServerConnection struct {
	connection

	pushIDLock sync.RWMutex
	nextPushID uint64
	maxPushID  uint64
}

// newServerConnection wraps an instance of mw.Connection with server-related capabilities.
func newServerConnection(mwc *mw.Connection, config *Config, requests chan<- *ServerRequest) *ServerConnection {
	c := &ServerConnection{
		connection: connection{
			config:     config,
			Connection: *mwc,
			ready:      make(chan struct{}),
		},
		maxPushID: 0,
	}
	err := c.init(c)
	if err != nil {
		return nil
	}
	go c.serviceRequests(requests)
	return c
}

func (c *ServerConnection) serviceRequests(requests chan<- *ServerRequest) {
	for {
		s := newStream(<-c.RemoteStreams)
		req := newServerRequest(c, s)
		go req.handle(requests)
	}
}

func (c *ServerConnection) handleMaxPushID(f byte, r FrameReader) error {
	if f != 0 {
		return ErrNonZeroFlags
	}
	n, err := r.ReadVarint()
	if err != nil {
		c.FatalError(ErrWtf)
		return err
	}
	err = r.CheckForEOF()
	if err != nil {
		c.FatalError(ErrWtf)
		return err
	}

	c.pushIDLock.Lock()
	defer c.pushIDLock.Unlock()
	if n > c.maxPushID {
		c.maxPushID = n
	}
	return nil
}

func (c *ServerConnection) getNextPushID() (uint64, error) {
	c.pushIDLock.RLock()
	defer c.pushIDLock.RUnlock()
	if c.nextPushID >= c.maxPushID {
		return 0, errors.New("No push IDs available")
	}

	id := c.nextPushID
	c.nextPushID++
	return id, nil
}

// HandleFrame is for dealing with those frames that Connection can't.
func (c *ServerConnection) HandleFrame(t FrameType, f byte, r FrameReader) error {
	switch t {
	case frameMaxPushID:
		return c.handleMaxPushID(f, r)
	default:
		return ErrInvalidFrame
	}
}

// HandleUnidirectionalStream causes a fatal error because servers don't expect to see these.
func (c *ServerConnection) HandleUnidirectionalStream(t unidirectionalStreamType, s *recvStream) {
	s.StopSending(0)
}
