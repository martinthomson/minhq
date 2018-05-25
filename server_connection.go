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
	hq := &ServerConnection{
		connection: connection{
			Connection: *mwc,
		},
		maxPushID: 0,
	}
	hq.Init(hq)
	go hq.serviceRequests(requests)
	return hq
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
	println("MAX_PUSH_ID", n)
	if n > c.maxPushID {
		c.maxPushID = n
	}
	return nil
}

func (c *ServerConnection) getNextPushID() (uint64, error) {
	c.pushIDLock.RLock()
	defer c.pushIDLock.RUnlock()
	println("GET MAX_PUSH_ID", c.nextPushID, c.maxPushID)
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
