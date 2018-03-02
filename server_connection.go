package minhq

import (
	"github.com/martinthomson/minhq/hc"
	"github.com/martinthomson/minhq/mw"
)

type ServerConnection struct {
	Connection

	maxPushId uint64
}

func NewServerConnection(mc *mw.Connection, config Config) *ServerConnection {
	hq := &ServerConnection{
		Connection: Connection{
			Connection: *mc,

			decoder: hc.NewQcramDecoder(config.DecoderTableCapacity),
			encoder: hc.NewQcramEncoder(0, 0),
		},
	}
	hq.Init(hq)
	return hq
}

func (c *ServerConnection) handleMaxPushId(f byte, r FrameReader) error {
	if f != 0 {
		return ErrNonZeroFlags
	}
	n, err := r.ReadVarint()
	if err != nil {
		c.FatalError(ErrWtf)
		return err
	}
	if n > c.maxPushId {
		c.maxPushId = n
	}
	return c.checkExtraData(r)
}

func (c *ServerConnection) HandleFrame(t frameType, f byte, r FrameReader) error {
	switch t {
	case frameMaxPushId:
		return c.handleMaxPushId(f, r)
	default:
		return ErrInvalidFrame
	}
}
