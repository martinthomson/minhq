package minhq

import (
	"errors"
	"io"
	"net/url"
	"sync/atomic"

	"github.com/ekr/minq"
	"github.com/martinthomson/minhq/hc"
	"github.com/martinthomson/minhq/mw"
)

type ClientConnection struct {
	Connection
	requestId uint64
}

func NewClientConnection(qc *minq.Connection, config Config) *ClientConnection {
	hq := &ClientConnection{
		Connection: Connection{
			Connection: *mw.NewConnection(qc),

			decoder: hc.NewQcramDecoder(config.DecoderTableCapacity),
			encoder: hc.NewQcramEncoder(0, 0),
		},
		requestId: 0,
	}
	hq.Init(hq)
	return hq
}

func (c *ClientConnection) HandleFrame(t frameType, f byte, r FrameReader) error {
	return ErrInvalidFrame
}

func (c *ClientConnection) nextRequestId() *requestId {
	return &requestId{atomic.AddUint64(&c.requestId, 1), 0}
}

type writerTo func(w io.Writer) (n int64, err error)

func (wt writerTo) WriteTo(w io.Writer) (n int64, err error) {
	return wt(w)
}

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

	requestId := c.nextRequestId()
	s := newStream(c.CreateStream())
	writer := NewFrameWriter(s)
	_, err = writer.WriteVarint(requestId.id)
	if err != nil {
		return nil, err
	}

	headerWriter := writerTo(func(w io.Writer) (n int64, err error) {
		return c.encoder.WriteHeaderBlock(w, s, requestId)
	})
	writer.WriteFrame(frameHeaders, 0, headerWriter)

	responseChannel := make(chan *ClientResponse)
	req := &ClientRequest{
		Headers:   allHeaders,
		Response:  responseChannel,
		requestId: requestId,

		encoder:       c.encoder,
		headersStream: c.headersStream,
		requestStream: s,
		outstanding:   &c.outstanding,
	}
	go req.readResponse(s, c, responseChannel)

	return req, nil
}
