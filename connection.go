package minhq

import (
	"bytes"
	"errors"
	"io"
	"io/ioutil"
	"sync/atomic"

	"github.com/ekr/minq"
	"github.com/martinthomson/minhq/hc"
	"github.com/martinthomson/minhq/mw"
)

// HTTPError is one of the QUIC/HTTP error codes defined.
type HTTPError uint16

func (e HTTPError) String() string {
	switch e {
	case 0:
		return "STOPPING"
	case 1:
		return "NO_ERROR"
	case 2:
		return "PUSH_REFUSED"
	case 3:
		return "INTERNAL_ERROR"
	default:
		return "Too lazy to do this right now"
	}
}

// These errors are commonly reported error codes.
var (
	ErrWtf          = HTTPError(3)
	ErrQuicWtf      = minq.ErrorCode(0xa) // TODO use app error code
	ErrExtraData    = errors.New("Extra data at the end of a frame")
	ErrNonZeroFlags = errors.New("Frame flags were non-zero")
	ErrInvalidFrame = errors.New("Invalid frame type for context")
)

// Config contains connection-level configuration options, such as the intended
// capacity of the header table.
type Config struct {
	DecoderTableCapacity hc.TableCapacity
}

type outstandingHeaderBlock struct {
	sent         uint16
	acknowledged uint16
}

type outstandingHeaders struct {
	outstanding map[uint64]*outstandingHeaderBlock
}

func (oh *outstandingHeaders) add(id *requestID) *requestID {
	o, ok := oh.outstanding[id.id]
	if ok {
		o.sent++
	} else {
		o = &outstandingHeaderBlock{1, 0}
		oh.outstanding[id.id] = o
	}
	return &requestID{id.id, o.sent}
}

func (oh *outstandingHeaders) ack(id uint64) *requestID {
	o, ok := oh.outstanding[id]
	if !ok {
		return nil
	}
	o.acknowledged++
	if o.acknowledged == o.sent {
		delete(oh.outstanding, id)
	}
	return &requestID{id, o.acknowledged}
}

// FrameHandler is used by subclasses of connection to deal with frames that only they handle.
type FrameHandler interface {
	HandleFrame(FrameType, byte, FrameReader) error
}

// connection is an abstract wrapper around mw.Connection (a wrapper around
// minq.Connection in turn).
type connection struct {
	config Config
	mw.Connection

	decoder         *hc.QpackDecoder
	encoder         *hc.QpackEncoder
	controlStream   *sendStream
	headersStream   *sendStream
	headerAckStream *sendStream
	requestID       uint64
	outstanding     outstandingHeaders

	unknownFrameHandler FrameHandler
}

// Init ensures that the connection is ready to go. It spawns a few goroutines
// to handle the control streams.
func (c *connection) Init(fh FrameHandler) {
	c.unknownFrameHandler = fh

	c.controlStream = newSendStream(c.CreateSendStream())
	c.headersStream = newSendStream(c.CreateSendStream())
	c.headerAckStream = newSendStream(c.CreateSendStream())

	// Asynchronously wait for incoming streams and then spawn handlers for each.
	go func() {
		go c.serviceControlStream(newRecvStream(<-c.RemoteRecvStreams))
		go c.serviceHeadersStream(newRecvStream(<-c.RemoteRecvStreams))
		go c.serviceHeaderAckStream(newRecvStream(<-c.RemoteRecvStreams))
	}()
}

// FatalError is a helper that passes on HTTP errors to the underlying connection.
func (c *connection) FatalError(e HTTPError) {
	c.Close()
}

// Both client and server need to track outstanding header blocks. They both use
// the same structure. The server only uses this for internal tracking, but the
// client also uses this to pick the request ID it includes in requests.
func (c *connection) nextRequestID() *requestID {
	return &requestID{atomic.AddUint64(&c.requestID, 1), 0}
}

func (c *connection) handlePriority(f byte, r io.Reader) error {
	// TODO implement something useful
	_, err := io.Copy(ioutil.Discard, r)
	if err != nil {
		c.FatalError(ErrWtf)
		return err
	}
	return nil
}

// This spits out a SETTINGS frame and then sits there reading the control
// stream until it encounters an error.
func (c *connection) serviceControlStream(controlStream *recvStream) {
	var buf bytes.Buffer
	sw := settingsWriter{&c.config}
	n, err := sw.WriteTo(&buf)
	if err != nil || n != int64(buf.Len()) {
		c.FatalError(ErrWtf)
		return
	}
	_, err = c.controlStream.WriteFrame(frameSettings, 0, buf.Bytes())
	if err != nil {
		c.FatalError(ErrWtf)
		return
	}

	t, f, r, err := controlStream.ReadFrame()
	if err != nil {
		c.FatalError(ErrWtf)
		return
	}

	if t != frameSettings || f != 0 {
		c.FatalError(ErrWtf)
		return
	}

	sr := settingsReader{c}
	err = sr.readSettings(r)
	if err != nil {
		c.FatalError(ErrWtf)
		return
	}

	for {
		t, f, r, err = controlStream.ReadFrame()
		if err != nil {
			c.FatalError(ErrWtf)
			return
		}
		switch t {
		case framePriority:
			err = c.handlePriority(f, r)
		default:
			err = c.unknownFrameHandler.HandleFrame(t, f, r)
		}
		if err != nil {
			c.FatalError(ErrWtf)
			return
		}
	}
}

func (c *connection) serviceHeadersStream(headersStream *recvStream) {
	for {
		t, f, r, err := headersStream.ReadFrame()
		if err != nil || t != frameHeaders || f != 0 {
			c.FatalError(ErrWtf)
			return
		}

		err = c.decoder.ReadTableUpdates(r)
		if err != nil {
			c.FatalError(ErrWtf)
			return
		}
	}
}

func (c *connection) serviceHeaderAckStream(headerAckStream *recvStream) {
	for {
		n, err := headerAckStream.ReadVarint()
		if err != nil {
			c.FatalError(ErrWtf)
			return
		}
		reqID := c.outstanding.ack(n)
		if reqID == nil {
			c.FatalError(ErrWtf)
			return
		}

		c.encoder.AcknowledgeHeader(reqID)
	}
}
