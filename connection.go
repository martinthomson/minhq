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
	controlStream   *stream
	headersStream   *stream
	headerAckStream *stream
	outstanding     outstandingHeaders

	unknownFrameHandler FrameHandler
}

func (c *Connection) Init(fh FrameHandler) {
	c.unknownFrameHandler = fh

	// TODO unidirectional
	c.controlStream = newStream(c.CreateStream())
	c.headersStream = newStream(c.CreateStream())
	c.headerAckStream = newStream(c.CreateStream())
	go c.serviceControlStream()
	go c.serviceHeadersStream()
	go c.serviceHeaderAckStream()
}

func (c *Connection) FatalError(e HttpError) {
	c.Close()
}

type settingsWriter struct {
	config *Config
}

// WriteTo writes out just one integer setting for the moment.
func (sw *settingsWriter) WriteTo(w io.Writer) (int64, error) {
	fw := NewFrameWriter(w)
	return sw.writeIntSetting(fw, settingTableSize,
		uint64(sw.config.DecoderTableCapacity))
}

func (sw *settingsWriter) writeIntSetting(fw FrameWriter, s settingType, v uint64) (int64, error) {
	var buf bytes.Buffer
	tmpfw := NewFrameWriter(&buf)
	_, err := tmpfw.WriteVarint(v)
	if err != nil {
		return 0, err
	}

	err = fw.WriteBits(uint64(s), 16)
	written := int64(2)
	n64, err := fw.WriteVarint(uint64(buf.Len()))
	written += n64
	if err != nil {
		return written, err
	}
	n64, err = io.Copy(fw, &buf)
	written += n64
	return written, err
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

func (c *Connection) readSettings(r FrameReader) error {
	for {
		s, err := r.ReadBits(16)
		if err != nil {
			return err
		}
		len, err := r.ReadVarint()
		if err != nil {
			return err
		}
		lr := r.Limited(len)
		switch settingType(s) {
		case settingTableSize:
			n, err := lr.ReadVarint()
			if err != nil {
				return err
			}
			c.encoder.SetCapacity(hc.TableCapacity(n))
		default:
			_, err = io.Copy(ioutil.Discard, lr)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// This spits out a SETTINGS frame and then sits there reading the control
// stream until it encounters an error.
func (c *Connection) serviceControlStream() {
	reader := NewFrameReader(c.controlStream)
	writer := NewFrameWriter(c.controlStream)
	err := writer.WriteFrame(frameSettings, 0, &settingsWriter{&c.config})
	if err != nil {
		c.FatalError(ErrWtf)
		return
	}

	t, f, r, err := reader.ReadFrame()
	if err != nil {
		c.FatalError(ErrWtf)
		return
	}

	if t != frameSettings || f != 0 {
		c.FatalError(ErrWtf)
		return
	}

	err = c.readSettings(r)
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
