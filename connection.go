package minhq

import (
	"bytes"
	"errors"
	"io"
	"io/ioutil"

	"github.com/ekr/minq"
	"github.com/martinthomson/minhq/hc"
	"github.com/martinthomson/minhq/mw"
)

type HttpError uint16

func (e HttpError) String() string {
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

var ErrWtf = HttpError(3)
var ErrQuicWtf = minq.ErrorCode(0xa) // PROTOCOL_VIOLATION
var ErrExtraData = errors.New("Extra data at the end of a frame")
var ErrNonZeroFlags = errors.New("Frame flags were non-zero")
var ErrInvalidFrame = errors.New("Invalid frame type for context")

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

func (oh *outstandingHeaders) add(id *requestId) *requestId {
	o, ok := oh.outstanding[id.id]
	if ok {
		o.sent++
	} else {
		o = &outstandingHeaderBlock{1, 0}
		oh.outstanding[id.id] = o
	}
	return &requestId{id.id, o.sent}
}

func (oh *outstandingHeaders) ack(id uint64) *requestId {
	o, ok := oh.outstanding[id]
	if !ok {
		return nil
	}
	o.acknowledged++
	if o.acknowledged == o.sent {
		delete(oh.outstanding, id)
	}
	return &requestId{id, o.acknowledged}
}

type FrameHandler interface {
	HandleFrame(frameType, byte, FrameReader) error
}

type Connection struct {
	config Config
	mw.Connection

	decoder         *hc.QcramDecoder
	encoder         *hc.QcramEncoder
	controlStream   *sendStream
	headersStream   *sendStream
	headerAckStream *sendStream
	outstanding     outstandingHeaders

	unknownFrameHandler FrameHandler
}

func (c *Connection) Init(fh FrameHandler) {
	c.unknownFrameHandler = fh

	// TODO unidirectional
	c.controlStream = newSendStream(c.CreateUnidirectionalStream())
	c.headersStream = newSendStream(c.CreateUnidirectionalStream())
	c.headerAckStream = newSendStream(c.CreateUnidirectionalStream())
	go c.serviceControlStream()
	go c.serviceHeadersStream()
	go c.serviceHeaderAckStream()
}

func (c *Connection) FatalError(e HttpError) {
	c.Close()
}

func (c *Connection) checkExtraData(r io.Reader) error {
	var p [1]byte
	n, err := r.Read(p[:])
	if err != nil && err != io.EOF {
		c.FatalError(ErrWtf)
		return err
	}
	if n > 0 {
		return ErrExtraData
	}
	return nil
}

func (c *Connection) handlePriority(f byte, r io.Reader) error {
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
func (c *Connection) serviceControlStream() {
	var buf bytes.Buffer
	sw := settingsWriter{&c.config}
	n, err := sw.WriteTo(&buf)
	if err != nil || n != int64(buf.Len()) {
		c.FatalError(ErrWtf)
		return
	}
	err = c.controlStream.WriteFrame(frameSettings, 0, buf.Bytes())
	if err != nil {
		c.FatalError(ErrWtf)
		return
	}

reader := <- c.remoteControlStream
	t, f, r, err := reader.ReadFrame()
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
		t, f, r, err = reader.ReadFrame()
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

func (c *Connection) serviceHeadersStream() {
	_ = c.decoder.ReadTableUpdates(c.headersStream)
	if c.GetState() != minq.StateClosed {
		c.FatalError(ErrWtf)
	}
}

func (c *Connection) serviceHeaderAckStream() {
	for {
		n, err := c.headerAckStream.ReadVarint()
		if err != nil {
			c.FatalError(ErrWtf)
			return
		}
		reqId := c.outstanding.ack(n)
		if reqId == nil {
			c.FatalError(ErrWtf)
			return
		}

		c.encoder.Acknowledge(reqId)
	}
}
