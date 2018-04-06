package minhq

import (
	"github.com/martinthomson/minhq/hc"
	"github.com/martinthomson/minhq/mw"
)

// ServerConnection specializes Connection with server-related functions.
type ServerConnection struct {
	connection

	Requests  <-chan *ServerRequest
	maxPushID uint64
}

// NewServerConnection wraps an instance of mw.Connection with server-related capabilities.
func NewServerConnection(mc *mw.Connection, config Config) *ServerConnection {
	requests := make(chan *ServerRequest)
	hq := &ServerConnection{
		connection: connection{
			Connection: *mc,

			decoder: hc.NewQcramDecoder(config.DecoderTableCapacity),
			encoder: hc.NewQcramEncoder(0, 0),
		},
		Requests:  requests,
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
	if n > c.maxPushID {
		c.maxPushID = n
	}
	err = r.CheckForEOF()
	if err != nil {
		c.FatalError(ErrWtf)
		return err
	}

	return nil
}

// HandleFrame is for dealing with those frames that Connection can't.
func (c *ServerConnection) HandleFrame(t frameType, f byte, r FrameReader) error {
	switch t {
	case frameMaxPushID:
		return c.handleMaxPushID(f, r)
	default:
		return ErrInvalidFrame
	}
}
