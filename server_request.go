package minhq

import "io"

// ServerRequest handles incoming requests.
type ServerRequest struct {
	c  *ServerConnection
	s  *stream
	ID uint64
	IncomingMessage
}

func newServerRequest(c *ServerConnection, s *stream) *ServerRequest {
	return &ServerRequest{
		c:               c,
		s:               s,
		ID:              0,
		IncomingMessage: newIncomingMessage(c.connection.decoder, nil),
	}
}

func (req *ServerRequest) handle(requests chan<- *ServerRequest) {
	reqID, err := req.s.ReadVarint()
	if err != nil {
		req.s.abort()
		return
	}
	req.ID = reqID

	headers, err := req.c.decoder.ReadHeaderBlock(req.s)
	if err != nil {
		req.s.abort()
		return
	}
	req.Headers = headers
	requests <- req
	err = req.read(req.s, func(t frameType, f byte, r io.Reader) error {
		return ErrUnsupportedFrame
	})
	if err != nil {
		req.s.abort()
		return
	}
}
