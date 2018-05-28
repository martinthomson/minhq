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

const (
	ErrHttpStopping            = HTTPError(0)
	ErrHttpNoError             = HTTPError(1)
	ErrHttpPushRefused         = HTTPError(2)
	ErrHttpInternalError       = HTTPError(3)
	ErrHttpPushAlreadyInCache  = HTTPError(4)
	ErrHttpRequestCancelled    = HTTPError(5)
	ErrHttpDecompressionFailed = HTTPError(6)
)

func (e HTTPError) String() string {
	switch e {
	case ErrHttpStopping:
		return "STOPPING"
	case ErrHttpNoError:
		return "NO_ERROR"
	case ErrHttpPushRefused:
		return "PUSH_REFUSED"
	case ErrHttpInternalError:
		return "INTERNAL_ERROR"
	case ErrHttpPushAlreadyInCache:
		return "PUSH_ALREADY_IN_CACHE"
	case ErrHttpRequestCancelled:
		return "REQUEST_CANCELLED"
	case ErrHttpDecompressionFailed:
		return "HTTP_HPACK_DECOMPRESSION_FAILED"
	default:
		return "Too lazy to do this right now"
	}
}

type unidirectionalStreamType byte

const (
	unidirectionalStreamControl      = unidirectionalStreamType(0x43)
	unidirectionalStreamPush         = unidirectionalStreamType(0x50)
	unidirectionalStreamQpackEncoder = unidirectionalStreamType(0x48)
	unidirectionalStreamQpackDecoder = unidirectionalStreamType(0x68)
)

func (ut unidirectionalStreamType) String() string {
	switch ut {
	case unidirectionalStreamControl:
		return "Control"
	case unidirectionalStreamPush:
		return "Push"
	case unidirectionalStreamQpackEncoder:
		return "QPACK Encoder"
	case unidirectionalStreamQpackDecoder:
		return "QPACK Decoder"
	}
	return "Unknown"
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

// connectionHandler is used by subclasses of connection to deal with frames that only they handle.
type connectionHandler interface {
	HandleFrame(FrameType, byte, FrameReader) error
	HandleUnidirectionalStream(unidirectionalStreamType, *recvStream)
}

// connection is an abstract wrapper around mw.Connection (a wrapper around
// minq.Connection in turn).
type connection struct {
	config *Config
	mw.Connection

	decoder       *hc.QpackDecoder
	encoder       *hc.QpackEncoder
	controlStream *sendStream

	// ready is closed when the connection is truly ready to send
	// requests or responses.  Read from it before sending anything that
	// depends on settings.
	ready chan struct{}
}

// init ensures that the connection is ready to go. It spawns a few goroutines
// to handle the control streams.
func (c *connection) init(handler connectionHandler) error {
	c.controlStream = newSendStream(c.CreateSendStream())
	err := c.sendSettings()
	if err != nil {
		return err
	}

	encoderStream := c.CreateSendStream()
	_, err = encoderStream.Write([]byte{byte(unidirectionalStreamQpackEncoder)})
	if err != nil {
		return err
	}
	c.encoder = hc.NewQpackEncoder(encoderStream, 0, 0)

	decoderStream := c.CreateSendStream()
	_, err = decoderStream.Write([]byte{byte(unidirectionalStreamQpackDecoder)})
	if err != nil {
		return err
	}
	c.decoder = hc.NewQpackDecoder(decoderStream, c.config.DecoderTableCapacity)

	// Asynchronously wait for incoming streams and then spawn handlers for each.
	// ready is used to signal that we have received settings from the other side.
	go c.serviceUnidirectionalStreams(handler, c.ready)
	return nil
}

// FatalError is a helper that passes on HTTP errors to the underlying connection.
func (c *connection) FatalError(e HTTPError) {
	c.Error(uint16(e), "")
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
	err := c.controlStream.WriteByte(byte(unidirectionalStreamControl))
	if err != nil {
		return err
	}
	sw := settingsWriter{c.config}
	var buf bytes.Buffer
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
func (c *connection) serviceControlStream(controlStream *recvStream,
	handler connectionHandler, ready chan<- struct{}) {
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
	close(ready)

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
			err = handler.HandleFrame(t, f, r)
		}
		if err != nil {
			c.FatalError(ErrWtf)
			return
		}
	}
}

func (c *connection) serviceUnidirectionalStreams(handler connectionHandler,
	ready chan<- struct{}) {
	for s := range c.Connection.RemoteRecvStreams {
		go func(s *recvStream) {
			b, err := s.ReadByte()
			if err != nil {
				c.FatalError(ErrWtf)
				return
			}

			t := unidirectionalStreamType(b)
			switch t {
			case unidirectionalStreamControl:
				c.serviceControlStream(s, handler, ready)
			case unidirectionalStreamQpackDecoder:
				c.encoder.ServiceAcknowledgments(s)
			case unidirectionalStreamQpackEncoder:
				c.decoder.ServiceUpdates(s)
			default:
				handler.HandleUnidirectionalStream(t, s)
			}
		}(newRecvStream(s))
	}
}
