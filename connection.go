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
	ConcurrentDecoders   uint16
	MaxConcurrentPushes  uint64
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

	decoder       *hc.QpackDecoder
	encoder       *hc.QpackEncoder
	controlStream *sendStream

	unknownFrameHandler FrameHandler
}

// Init ensures that the connection is ready to go. It spawns a few goroutines
// to handle the control streams.
func (c *connection) Init(fh FrameHandler) {
	c.unknownFrameHandler = fh

	c.controlStream = newSendStream(c.CreateSendStream())
	c.sendSettings()
	c.encoder = hc.NewQpackEncoder(c.CreateSendStream(), 0, 0)
	c.decoder = hc.NewQpackDecoder(c.CreateSendStream(), c.config.DecoderTableCapacity)

	// Asynchronously wait for incoming streams and then spawn handlers for each.
	go func() {
		go c.serviceControlStream(newRecvStream(<-c.RemoteRecvStreams))
		go c.decoder.ServiceUpdates(<-c.RemoteRecvStreams)
		go c.encoder.ServiceAcknowledgments(<-c.RemoteRecvStreams)
	}()
}

// FatalError is a helper that passes on HTTP errors to the underlying connection.
func (c *connection) FatalError(e HTTPError) {
	c.Close()
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

func (c *connection) sendSettings() error {
	var buf bytes.Buffer
	sw := settingsWriter{&c.config}
	n, err := sw.WriteTo(&buf)
	if err != nil {
		return err
	}
	if n != int64(buf.Len()) {
		return ErrStreamBlocked
	}
	_, err = c.controlStream.WriteFrame(frameSettings, 0, buf.Bytes())
	return err
}

// This spits out a SETTINGS frame and then sits there reading the control
// stream until it encounters an error.
func (c *connection) serviceControlStream(controlStream *recvStream) {

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
