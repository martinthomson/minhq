package minhq

import (
	"bytes"
	"errors"
	"io"
	"net/url"
	"strconv"

	"github.com/martinthomson/minhq/hc"
)

// ServerRequest handles incoming requests.
type ServerRequest struct {
	C      *ServerConnection
	s      *stream
	ID     uint64
	Method string
	Target *url.URL
	IncomingMessage
}

func newServerRequest(c *ServerConnection, s *stream) *ServerRequest {
	return &ServerRequest{
		C:               c,
		s:               s,
		ID:              0,
		Method:          "",
		Target:          nil,
		IncomingMessage: newIncomingMessage(c.connection.decoder, nil),
	}
}

func (req *ServerRequest) handle(requests chan<- *ServerRequest) {
	err := req.read(&req.s.recvStream, func(headers []hc.HeaderField) error {
		req.setHeaders(headers)
		requests <- req
		return nil
	}, func(t FrameType, f byte, r io.Reader) error {
		return ErrUnsupportedFrame
	})
	if err != nil {
		req.s.abort()
		return
	}
}

type hasHeaders interface {
	GetHeader(n string) string
}

func (req *ServerRequest) setHeaders(headers []hc.HeaderField) error {
	req.Headers = headers
	var err error
	req.Method, req.Target, err = req.Headers.getMethodAndTarget()
	return err
}

func (req *ServerRequest) sendResponse(statusCode int, headers []hc.HeaderField,
	s *sendStream, push *ServerPushRequest) (*ServerResponse, error) {
	allHeaders := append([]hc.HeaderField{
		hc.HeaderField{Name: ":status", Value: strconv.Itoa(statusCode)},
	}, headers...)

	response := &ServerResponse{
		Request:         req,
		PushRequest:     push,
		OutgoingMessage: newOutgoingMessage(&req.C.connection, s, allHeaders),
	}
	err := response.writeHeaderBlock(allHeaders)
	if err != nil {
		return nil, err
	}

	return response, nil
}

// Respond creates a response, starting by writing the response header block.
func (req *ServerRequest) Respond(statusCode int, headers ...hc.HeaderField) (*ServerResponse, error) {
	return req.sendResponse(statusCode, headers, &req.s.sendStream, nil)
}

// Push creates a new server push.
func (req *ServerRequest) Push(method string, target string, headers ...hc.HeaderField) (*ServerPushRequest, error) {
	url, allHeaders, err := buildRequestHeaderFields(method, req.Target, target, headers)
	if err != nil {
		return nil, err
	}

	pushID, err := req.C.getNextPushID()
	if err != nil {
		return nil, err
	}

	var headerBuf bytes.Buffer
	headerWriter := NewFrameWriter(&headerBuf)
	_, err = headerWriter.WriteVarint(pushID)
	if err != nil {
		return nil, err
	}
	err = req.C.encoder.WriteHeaderBlock(headerWriter, req.s.Id(), allHeaders...)
	if err != nil {
		return nil, err
	}
	_, err = req.s.WriteFrame(framePushPromise, 0, headerBuf.Bytes())
	if err != nil {
		return nil, err
	}

	return &ServerPushRequest{
		Target:  url,
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
func (resp *ServerResponse) Push(method string, target string, headers ...hc.HeaderField) (*ServerPushRequest, error) {
	return resp.Request.Push(method, target, headers...)
}

// ServerPushRequest is a more limited version of ServerRequest.
type ServerPushRequest struct {
	Target  *url.URL
	Request *ServerRequest
	PushID  uint64
	Headers []hc.HeaderField
}

// Respond on ServerPushRequest is functionally identical to the same function on ServerRequest.
func (push *ServerPushRequest) Respond(statusCode int, headers ...hc.HeaderField) (*ServerResponse, error) {
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
