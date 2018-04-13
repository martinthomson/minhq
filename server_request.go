package minhq

import (
	"bytes"
	"errors"
	"io"

	"github.com/martinthomson/minhq/hc"
)

// ServerRequest handles incoming requests.
type ServerRequest struct {
	C         *ServerConnection
	s         *stream
	ID        uint64
	requestID *requestID // for tracking outstanding header fields
	IncomingMessage
}

func newServerRequest(c *ServerConnection, s *stream) *ServerRequest {
	return &ServerRequest{
		C:               c,
		s:               s,
		ID:              0,
		requestID:       c.nextRequestID(),
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

	headers, err := req.C.decoder.ReadHeaderBlock(req.s)
	if err != nil {
		req.s.abort()
		return
	}
	req.Headers = headers
	requests <- req
	err = req.read(req.s, func(t FrameType, f byte, r io.Reader) error {
		return ErrUnsupportedFrame
	})
	if err != nil {
		req.s.abort()
		return
	}
}

func (req *ServerRequest) sendResponse(statusCode int, headers []hc.HeaderField,
	s FrameWriteCloser, push *ServerPushRequest) (*ServerResponse, error) {
	allHeaders := append([]hc.HeaderField{
		hc.HeaderField{Name: ":status", Value: string(statusCode)},
	}, headers...)

	err := writeHeaderBlock(req.C.encoder, req.C.headersStream, s, req.requestID, allHeaders)
	if err != nil {
		return nil, err
	}

	return &ServerResponse{
		Request:         req,
		PushRequest:     push,
		OutgoingMessage: newOutgoingMessage(&req.C.connection, s, req.requestID, allHeaders),
	}, nil
}

// Respond creates a response, starting by writing the response header block.
func (req *ServerRequest) Respond(statusCode int, headers... hc.HeaderField) (*ServerResponse, error) {
	return req.sendResponse(statusCode, headers, req.s, nil)
}

// Push creates a new server push.
func (req *ServerRequest) Push(method string, target string, headers []hc.HeaderField) (*ServerPushRequest, error) {
	allHeaders, err := buildRequestHeaderFields(method, target, headers)
	if err != nil {
		return nil, err
	}

	pushID, err := req.C.getNextPushID()
	if err != nil {
		return nil, err
	}

	var controlBuf, headerBuf bytes.Buffer
	headerWriter := NewFrameWriter(&headerBuf)
	_, err = headerWriter.WriteVarint(pushID)
	if err != nil {
		return nil, err
	}
	err = req.C.encoder.WriteHeaderBlock(NewFrameWriter(&controlBuf), headerWriter,
		req.C.outstanding.add(req.requestID), allHeaders...)
	if err != nil {
		return nil, err
	}
	_, err = req.C.headersStream.WriteFrame(frameHeaders, 0, controlBuf.Bytes())
	if err != nil {
		return nil, err
	}
	_, err = req.s.WriteFrame(framePushPromise, 0, headerBuf.Bytes())
	if err != nil {
		return nil, err
	}

	return &ServerPushRequest{
		Request: req,
		PushID:  pushID,
		Headers: allHeaders,
	}, nil
}

// ServerResponse can be used to write response bodies and trailers.
type ServerResponse struct {
	// Request is the request that this response is associated with.
	Request *ServerRequest
	// Push is the push promise, which might be nil.
	PushRequest *ServerPushRequest
	OutgoingMessage
}

// Push just forwards the server push to ServerRequest.Push.
func (resp *ServerResponse) Push(method string, target string, headers []hc.HeaderField) (*ServerPushRequest, error) {
	return resp.Request.Push(method, target, headers)
}

// ServerPushRequest is a more limited version of ServerRequest.
type ServerPushRequest struct {
	Request *ServerRequest
	PushID  uint64
	Headers []hc.HeaderField
}

// Respond on ServerPushRequest is functionally identical to the same function on ServerRequest.
func (push *ServerPushRequest) Respond(statusCode int, headers []hc.HeaderField) (*ServerResponse, error) {
	send := push.Request.C.CreateSendStream()
	if send == nil {
		return nil, errors.New("No available send streams for push response")
	}
	s := newSendStream(send)
	_, err := s.WriteVarint(push.PushID)
	if err != nil {
		return nil, err
	}
	return push.Request.sendResponse(statusCode, headers, s, push)
}
