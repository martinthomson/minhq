package minhq

import (
	"io"
	"io/ioutil"
	"strconv"
)

type requestID struct {
	id    uint64
	index int
}

func (rid *requestID) Id() uint64 {
	return rid.id
}

// ClientRequest is a representation of a request. A Connection will return one
// of these in response to a request to send a request. It is writable so that
// requests with bodies can be sent. A channel indicates where responses can be
// retrieved.
//
// To use this, make one using Connection.Fetch(). Write any body, then close
// the request with any trailers.
type ClientRequest struct {
	Response <-chan *ClientResponse

	OutgoingMessage
}

func (req *ClientRequest) handlePushPromise(f byte, r io.Reader) error {
	// TODO something more than a straight discard
	_, err := io.Copy(ioutil.Discard, r)
	return err
}

func (req *ClientRequest) readResponse(s *stream, c *ClientConnection,
	responseChannel chan<- *ClientResponse) {
	t, f, r, err := s.ReadFrame()
	if err != nil || t != frameHeaders || f != 0 {
		s.abort()
		return
	}

	headers, err := c.decoder.ReadHeaderBlock(r, s.Id())
	if err != nil {
		s.abort()
		return
	}

	resp := &ClientResponse{
		Request:         req,
		IncomingMessage: newIncomingMessage(c.connection.decoder, headers),
	}
	resp.Status, err = strconv.Atoi(resp.GetHeader(":status"))
	if err != nil {
		s.abort()
		return
	}

	responseChannel <- resp
	err = resp.read(&s.recvStream, func(t FrameType, f byte, r io.Reader) error {
		switch t {
		case framePushPromise:
			err := req.handlePushPromise(f, r)
			if err != nil {
				return err
			}
		default:
			return ErrUnsupportedFrame
		}
		return nil
	})
	if err != nil {
		s.abort()
		return
	}
}

// ClientResponse includes all that a client needs to handle a response.
type ClientResponse struct {
	Request *ClientRequest
	IncomingMessage
	Status int
}
