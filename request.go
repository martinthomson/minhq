package minhq

import (
	"io"

	"github.com/martinthomson/minhq/hc"
)

type requestId struct {
	id    uint64
	index uint16
}

// Request is a representation of a request. A Connection will return one of
// these in response to a request to send a request. It is writable so that
// requests with bodies can be sent. A channel indicates where responses can be
// retrieved.
//
// To use this, make one using Connection.Fetch(). Write any body, then close
// the request with any trailers.
type ClientRequest struct {
	Headers   []hc.HeaderField
	Response  <-chan *ClientResponse
	requestId *requestId

	requestStream io.WriteCloser

	// This stuff is all needed for trailers (ugh).
	encoder       *hc.QcramEncoder
	headersStream io.Writer
	outstanding   *outstandingHeaders
}

func (req *ClientRequest) Write(p []byte) (int, error) {
	return req.requestStream.Write(p)
}

func (req *ClientRequest) Close(trailers []hc.HeaderField) error {
	if trailers != nil {
		err := req.encoder.WriteHeaderBlock(req.headersStream, req.requestStream,
			req.outstanding.add(req.requestId))
		if err != nil {
			return err
		}
	}
	return req.requestStream.Close()
}

func (req *ClientRequest) handlePushPromise(f byte, r io.Reader) error {
	// TODO something useful
	return nil
}

func (req *ClientRequest) readResponse(s *stream, c *ClientConnection,
	responseChannel chan<- *ClientResponse) {
	t, f, r, err := s.ReadFrame()
	if err != nil {
		s.Reset(ErrQuicWtf)
		return
	}

	headers, err := c.decoder.ReadHeaderBlock(r)
	if err != nil {
		c.fatalError(ErrWtf)
		return
	}

	data := make(chan io.Reader)
	trailers := make(chan []hc.HeaderField)
	responseChannel <- &ClientResponse{
		Headers: headers,
		Request: req,
		concatenatingReader: concatenatingReader{
			current: nil,
			pending: data,
		},
		Trailers: trailers,
	}

	done := false
	for t, f, r, err = s.ReadFrame(); err == nil; {
		if done {
			// Can't receive any other frame after trailers.
			c.fatalError(ErrWtf)
			return
		}

		switch t {
		case frameData:
			data <- r
		case frameHeaders:
			done = true
			headers, err = c.decoder.ReadHeaderBlock(r)
			if err != nil {
				c.fatalError(ErrWtf)
				return
			}
			trailers <- headers
			close(trailers)
		case framePushPromise:
			err := req.handlePushPromise(f, r)
			if err != nil {
				return
			}
		default:
			c.fatalError(ErrWtf)
			return
		}
	}
	if err == io.EOF {
		close(data)
	} else if err != nil {
		c.fatalError(ErrWtf)
	}
}

type concatenatingReader struct {
	current io.Reader
	pending <-chan io.Reader
}

func (cat *concatenatingReader) next() bool {
	cat.current = <-cat.pending
	return cat.current != nil
}

func (cat *concatenatingReader) Read(p []byte) (int, error) {
	if cat.current == nil {
		if !cat.next() {
			return 0, io.EOF
		}
	}

	n, err := cat.current.Read(p)
	for err == io.EOF {
		if !cat.next() {
			return 0, io.EOF
		}
		n, err = cat.current.Read(p)
	}
	return n, err
}

// ClientResponse includes all that a client needs to handle a response.
type ClientResponse struct {
	Headers []hc.HeaderField
	Request *ClientRequest
	concatenatingReader
	Trailers <-chan []hc.HeaderField
}
