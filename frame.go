package minhq

import (
	"bytes"
	"errors"
	"io"

	"github.com/martinthomson/minhq/bitio"
)

// FrameType is the type of an HTTP/QUIC frame.
type FrameType byte

const (
	frameData        = FrameType(0)
	frameHeaders     = FrameType(1)
	framePriority    = FrameType(2)
	frameCancelPush  = FrameType(3)
	frameSettings    = FrameType(4)
	framePushPromise = FrameType(5)
	frameGoaway      = FrameType(7)
	frameHeaderAck   = FrameType(8)
	frameMaxPushID   = FrameType(13)
)

// ErrUnsupportedFrame signals that an unsupported frame was received.
var ErrUnsupportedFrame = errors.New("Unsupported frame type received")

// ErrTooLarge signals that a value was too large.
var ErrTooLarge = errors.New("Value too large for the field")

// FrameReader wraps a reader with helper functions specific to HTTP/QUIC.
type FrameReader interface {
	bitio.BitReader
	ReadVarint() (uint64, error)
	ReadFrame() (FrameType, byte, FrameReader, error)
	Limited(n uint64) FrameReader
	CheckForEOF() error
}

type frameReader struct {
	bitio.BitReader
}

// NewFrameReader wraps the given io.Reader.
func NewFrameReader(r io.Reader) FrameReader {
	return &frameReader{bitio.NewBitReader(r)}
}

// ReadVarint reads a variable length integer.
func (fr *frameReader) ReadVarint() (uint64, error) {
	len, err := fr.ReadBits(2)
	if err != nil {
		return 0, err
	}
	return fr.ReadBits((8 << len) - 2)
}

// ReadFrame reads a frame header and returns the different pieces of the frame.
func (fr *frameReader) ReadFrame() (FrameType, byte, FrameReader, error) {
	len, err := fr.ReadVarint()
	if err != nil {
		return 0, 0, nil, err
	}
	t, err := fr.ReadByte()
	if err != nil {
		return 0, 0, nil, err
	}
	f, err := fr.ReadByte()
	if err != nil {
		return 0, 0, nil, err
	}
	return FrameType(t), f, fr.Limited(len), nil
}

// Limited makes an io.LimitedReader that reads the next `n` bytes from this reader.
func (fr *frameReader) Limited(n uint64) FrameReader {
	return NewFrameReader(&io.LimitedReader{R: fr, N: int64(n)})
}

// CheckForEOF returns an error if the reader has remaining data.
func (fr *frameReader) CheckForEOF() error {
	var p [1]byte
	n, err := fr.Read(p[:])
	if err != nil && err != io.EOF {
		return err
	}
	if n > 0 {
		return ErrExtraData
	}
	return nil
}

// FrameWriter wraps the io.Writer interface with HTTP/QUIC helper functions.
type FrameWriter interface {
	bitio.BitWriter
	WriteVarint(v uint64) (int, error)
	WriteFrame(t FrameType, f byte, p []byte) (int, error)
}

type frameWriter struct {
	bitio.BitWriter
}

// NewFrameWriter makes a FrameWriter.
func NewFrameWriter(w io.Writer) FrameWriter {
	return &frameWriter{bitio.NewBitWriter(w)}
}

func (fw *frameWriter) WriteVarint(v uint64) (int, error) {
	var size byte
	switch {
	case v >= 1<<62:
		return 0, ErrTooLarge
	case v >= 1<<30:
		size = 3
	case v >= 1<<14:
		size = 2
	case v >= 1<<6:
		size = 1
	default:
		size = 0
	}
	err := fw.WriteBits(uint64(size), 2)
	if err != nil {
		return 0, err
	}
	n := byte(1) << size
	err = fw.WriteBits(v, n*8-2)
	if err != nil {
		return 0, nil
	}
	return int(n), nil
}

func (fw *frameWriter) WriteFrame(t FrameType, f byte, p []byte) (int, error) {
	written, err := fw.WriteVarint(uint64(len(p)))
	if err != nil {
		return written, err
	}
	err = fw.WriteByte(byte(t))
	if err != nil {
		return written, err
	}
	err = fw.WriteByte(byte(f))
	if err != nil {
		return written, err
	}
	n, err := io.Copy(fw, bytes.NewReader(p))
	return written + int(n) + 2, err
}

// FrameWriteCloser adds io.Closer to the FrameWriter interface.
type FrameWriteCloser interface {
	FrameWriter
	io.Closer
}

type frameWriteCloser struct {
	FrameWriter
	io.Closer
}

// NewFrameWriteCloser makes a FrameWriteCloser.
func NewFrameWriteCloser(w io.WriteCloser) FrameWriteCloser {
	return &frameWriteCloser{NewFrameWriter(w), w}
}
