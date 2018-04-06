package minhq

import (
	"io"
	"io/ioutil"

	"github.com/martinthomson/minhq/hc"
)

type requestID struct {
	id    uint64
	index uint16
}

// ClientRequest is a representation of a request. A Connection will return one
// of these in response to a request to send a request. It is writable so that
// requests with bodies can be sent. A channel indicates where responses can be
// retrieved.
//
// To use this, make one using Connection.Fetch(). Write any body, then close
// the request with any trailers.
type ClientRequest struct {
	Headers   []hc.HeaderField
	Response  <-chan *ClientResponse
	requestID *requestID

	requestStream FrameWriteCloser

	// This stuff is all needed for trailers (ugh).
	encoder       *hc.QcramEncoder
	headersStream FrameWriter
	outstanding   *outstandingHeaders
}

func (req *ClientRequest) Write(p []byte) (int, error) {
	return req.requestStream.WriteFrame(frameData, 0, p)
}

// Close closes out the stream, writing any trailers that might be included.
func (req *ClientRequest) Close(trailers []hc.HeaderField) error {
	if trailers != nil {
		err := writeHeaderBlock(req.encoder, req.headersStream, req.requestStream,
			req.outstanding.add(req.requestID))
		if err != nil {
			return err
		}
	}
	return req.requestStream.Close()
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

	headers, err := c.decoder.ReadHeaderBlock(r)
	if err != nil {
		s.abort()
		return
	}

	resp := &ClientResponse{
		Request:         req,
		IncomingMessage: newIncomingMessage(c.connection.decoder, headers),
	}
	responseChannel <- resp
	err = resp.read(s, func(t frameType, f byte, r io.Reader) error {
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
}
