package minhq

import (
	"errors"
	"net/url"
	"sync/atomic"

	"github.com/ekr/minq"
	"github.com/martinthomson/minhq/hc"
	"github.com/martinthomson/minhq/mw"
)

// ClientConnection is a connection specialized for use by clients.
type ClientConnection struct {
	connection
	requestID uint64
}

// NewClientConnection wraps an instance of minq.Connection.
func NewClientConnection(qc *minq.Connection, config Config) *ClientConnection {
	hq := &ClientConnection{
		connection: connection{
			Connection: *mw.NewConnection(qc),

			decoder: hc.NewQcramDecoder(config.DecoderTableCapacity),
			encoder: hc.NewQcramEncoder(0, 0),
		},
		requestID: 0,
	}
	hq.Init(hq)
	return hq
}

// HandleFrame is for dealing with those frames that Connection can't.
func (c *ClientConnection) HandleFrame(t frameType, f byte, r FrameReader) error {
	return ErrInvalidFrame
}

func (c *ClientConnection) nextRequestID() *requestID {
	return &requestID{atomic.AddUint64(&c.requestID, 1), 0}
}

// Fetch makes a request.
func (c *ClientConnection) Fetch(method string, target string, h []hc.HeaderField) (*ClientRequest, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "https" {
		return nil, errors.New("No support for non-https URLs")
	}

	allHeaders := make([]hc.HeaderField, len(h)+4)
	allHeaders[0].Name = ":method"
	allHeaders[0].Value = method
	allHeaders[1].Name = ":authority"
	allHeaders[1].Value = u.Host
	allHeaders[2].Name = ":path"
	allHeaders[2].Value = u.EscapedPath()
	allHeaders[3].Name = ":scheme"
	allHeaders[3].Value = "https"
	copy(allHeaders[4:], h)

	requestID := c.nextRequestID()
	s := newStream(c.CreateStream())
	_, err = s.WriteVarint(requestID.id)
	if err != nil {
		return nil, err
	}

	err = writeHeaderBlock(c.encoder, c.headersStream, s, requestID)
	if err != nil {
		return nil, err
	}

	responseChannel := make(chan *ClientResponse)
	req := &ClientRequest{
		Headers:   allHeaders,
		Response:  responseChannel,
		requestID: requestID,

		requestStream: s,

		encoder:       c.encoder,
		headersStream: c.headersStream,
		outstanding:   &c.outstanding,
	}
	go req.readResponse(s, c, responseChannel)

	return req, nil
}
