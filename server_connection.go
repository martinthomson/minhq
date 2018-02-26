package minhq

import (
	"github.com/ekr/minq"
	"github.com/martinthomson/minhq/hc"
)

type ServerConnection struct {
	Connection
}

func NewServerConnection(qc *minq.Connection, config Config) *ServerConnection {
	hq := &ServerConnection{
		Connection: Connection{
			connection: qc,

			decoder: hc.NewQcramDecoder(config.DecoderTableCapacity),
			encoder: hc.NewQcramEncoder(0, 0),
		},
	}
	hq.init()
	return hq
}
