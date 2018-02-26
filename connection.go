package minhq

import (
	"bytes"
	"errors"
	"io"
	"io/ioutil"

	"github.com/ekr/minq"
	"github.com/martinthomson/minhq/hc"
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

type minqHandler struct {
	streamReadable map[*minq.Stream]chan<- struct{}
}

func (sh *minqHandler) add(s *minq.Stream) <-chan struct{} {
	ch := make(chan struct{})
	sh.streamReadable[s] = ch
	return ch
}

func (sh *minqHandler) StateChanged(s minq.State) {}
func (sh *minqHandler) NewStream(s *minq.Stream) {
	// TODO handle push promises
}
func (sh *minqHandler) StreamReadable(s *minq.Stream) {
	sh.streamReadable[s] <- struct{}{}
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

type Connection struct {
	config      Config
	connection  *minq.Connection
	minqHandler *minqHandler

	decoder         *hc.QcramDecoder
	encoder         *hc.QcramEncoder
	controlStream   *stream
	headersStream   *stream
	headerAckStream *stream
	outstanding     outstandingHeaders

	maxPushId uint64
}

func (c *Connection) createStream() *stream {
	s := c.connection.CreateStream()
	return newStream(s, c.minqHandler.add(s))
}

func (c *Connection) init() {
	handler := &minqHandler{}
	c.connection.SetHandler(handler)
	c.minqHandler = handler

	c.controlStream = c.createStream()
	c.headersStream = c.createStream()
	c.headerAckStream = c.createStream()
	go c.serviceControlStream()
	go c.serviceHeadersStream()
	go c.serviceHeaderAckStream()
}

func (c *Connection) fatalError(err HttpError) {
	c.connection.Close( /* TODO Application Close for minq */ )
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
		c.fatalError(ErrWtf)
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
		c.fatalError(ErrWtf)
		return err
	}
	return nil
}

func (c *Connection) handleMaxPushId(f byte, r FrameReader) error {
	if f != 0 {
		return ErrNonZeroFlags
	}
	n, err := r.ReadVarint()
	if err != nil {
		c.fatalError(ErrWtf)
		return err
	}
	if n > c.maxPushId {
		c.maxPushId = n
	}
	return c.checkExtraData(r)
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
		c.fatalError(ErrWtf)
		return
	}

	t, f, r, err := reader.ReadFrame()
	if err != nil {
		c.fatalError(ErrWtf)
		return
	}

	if t != frameSettings || f != 0 {
		c.fatalError(ErrWtf)
		return
	}

	err = c.readSettings(r)
	if err != nil {
		c.fatalError(ErrWtf)
		return
	}

	for {
		t, f, r, err = reader.ReadFrame()
		if err != nil {
			c.fatalError(ErrWtf)
			return
		}
		switch t {
		case framePriority:
			err = c.handlePriority(f, r)
		case frameMaxPushId:
			err = c.handleMaxPushId(f, r)
		default:
			err = ErrInvalidFrame
		}
		if err != nil {
			c.fatalError(ErrWtf)
			return
		}
	}
}

func (c *Connection) serviceHeadersStream() {
	_ = c.decoder.ReadTableUpdates(c.headersStream)
	if c.connection.GetState() != minq.StateClosed {
		c.fatalError(ErrWtf)
	}
}

func (c *Connection) serviceHeaderAckStream() {
	for {
		n, err := c.headerAckStream.ReadVarint()
		if err != nil {
			c.fatalError(ErrWtf)
			return
		}
		reqId := c.outstanding.ack(n)
		if reqId == nil {
			c.fatalError(ErrWtf)
			return
		}

		c.encoder.Acknowledge(reqId)
	}
}
