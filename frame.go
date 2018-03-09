package minhq

import (
	"bytes"
	"errors"
	"io"

	"github.com/martinthomson/minhq/bitio"
)

type frameType byte

const (
	frameData        = frameType(0)
	frameHeaders     = frameType(1)
	framePriority    = frameType(2)
	frameCancelPush  = frameType(3)
	frameSettings    = frameType(4)
	framePushPromise = frameType(5)
	frameGoaway      = frameType(7)
	frameHeaderAck   = frameType(8)
	frameMaxPushId   = frameType(13)
)

// ErrUnsupportedFrame signals that an unsupported frame was received.
var ErrUnsupportedFrame = errors.New("Unsupported frame type received")

// ErrTooLarge signals that a value was too large.
var ErrTooLarge = errors.New("Value too large for the field")

type FrameReader interface {
	bitio.BitReader
	ReadVarint() (uint64, error)
	ReadFrame() (frameType, byte, FrameReader, error)
	Limited(n uint64) FrameReader
}

type frameReader struct {
	bitio.BitReader
}

func NewFrameReader(r io.Reader) FrameReader {
	return &frameReader{bitio.NewBitReader(r)}
}

func (fr *frameReader) ReadVarint() (uint64, error) {
	len, err := fr.ReadBits(2)
	if err != nil {
		return 0, err
	}
	return fr.ReadBits((8 << len) - 2)
}

func (fr *frameReader) ReadFrame() (frameType, byte, FrameReader, error) {
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
	return frameType(t), f, fr.Limited(len), nil
}

// Limited makes an io.LimitedReader that reads the next `n` bytes from this reader.
func (fr *frameReader) Limited(n uint64) FrameReader {
	return NewFrameReader(&io.LimitedReader{R: fr, N: int64(n)})
}

type FrameWriter interface {
	bitio.BitWriter
	WriteVarint(v uint64) (int64, error)
	WriteFrame(t frameType, f byte, p []byte) error
}

type FrameWriteCloser interface {
	FrameWriter
	io.Closer
}

type frameWriter struct {
	bitio.BitWriter
}

func NewFrameWriter(w io.Writer) FrameWriter {
	return &frameWriter{bitio.NewBitWriter(w)}
}

func (fw *frameWriter) WriteVarint(v uint64) (int64, error) {
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
	return int64(n), nil
}

func (fw *frameWriter) WriteFrame(t frameType, f byte, p []byte) error {
	_, err := fw.WriteVarint(uint64(len(p)))
	if err != nil {
		return err
	}
	err = fw.WriteByte(byte(t))
	if err != nil {
		return err
	}
	err = fw.WriteByte(byte(f))
	if err != nil {
		return err
	}
	_, err = io.Copy(fw, bytes.NewReader(p))
	return err
}
