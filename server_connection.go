package minhq

import (
	"bytes"
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

	cancelledPushesLock sync.RWMutex
	cancelledPushes     map[uint64]bool
}

// newServerConnection wraps an instance of mw.Connection with server-related capabilities.
func newServerConnection(mwc *mw.Connection, config *Config) *ServerConnection {
	return &ServerConnection{
		connection: connection{
			config:     config,
			Connection: *mwc,
			ready:      make(chan struct{}),
		},
		cancelledPushes: make(map[uint64]bool),
	}
}

// Connect waits until the connection is up and sends requests to the provided channel.
func (c *ServerConnection) Connect(requests chan<- *ServerRequest) error {
	err := c.connect(c)
	if err != nil {
		return err
	}
	go c.serviceRequests(requests)
	return nil
}

func (c *ServerConnection) serviceRequests(requests chan<- *ServerRequest) {
	for {
		s := newStream(<-c.RemoteStreams)
		req := newServerRequest(c, s)
		go req.handle(requests)
	}
}

func (c *ServerConnection) handleMaxPushID(r FrameReader) error {
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

func (c *ServerConnection) handleCancelPush(r FrameReader) error {
	pushID, err := r.ReadVarint()
	if err != nil {
		return err
	}
	err = r.CheckForEOF()
	if err != nil {
		return err
	}
	c.cancelledPushesLock.Lock()
	defer c.cancelledPushesLock.Unlock()
	c.cancelledPushes[pushID] = true
	return nil
}

func (c *ServerConnection) cancelPush(pushID uint64) error {
	var buf bytes.Buffer
	_, err := NewFrameWriter(&buf).WriteVarint(pushID)
	if err != nil {
		return err
	}
	_, err = c.controlStream.WriteFrame(frameCancelPush, buf.Bytes())
	if err != nil {
		return err
	}

	c.cancelledPushesLock.Lock()
	defer c.cancelledPushesLock.Unlock()
	c.cancelledPushes[pushID] = true
	return nil
}

func (c *ServerConnection) pushCancelled(pushID uint64) bool {
	c.cancelledPushesLock.RLock()
	defer c.cancelledPushesLock.RUnlock()
	return c.cancelledPushes[pushID]
}

// HandleFrame is for dealing with those frames that Connection can't.
func (c *ServerConnection) HandleFrame(t FrameType, r FrameReader) error {
	switch t {
	case frameMaxPushID:
		return c.handleMaxPushID(r)
	case frameCancelPush:
		return c.handleCancelPush(r)
	default:
		return ErrInvalidFrame
	}
}

// HandleUnidirectionalStream causes a fatal error because servers don't expect to see these.
func (c *ServerConnection) HandleUnidirectionalStream(t unidirectionalStreamType, s *recvStream) {
	s.StopSending(uint16(ErrHttpUnknownStreamType))
}
