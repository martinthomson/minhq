package minhq

import (
	"bytes"
	"io"
)

type simpleByteWriter struct {
	writer io.Writer
}

func (sbw simpleByteWriter) WriteByte(c byte) error {
	n, err := sbw.writer.Write([]byte{c})
	if err != nil {
		return err
	}
	if n == 0 {
		return io.ErrShortWrite
	}
	return nil
}

func makeByteWriter(writer io.Writer) io.ByteWriter {
	bw, ok := writer.(io.ByteWriter)
	if ok {
		return bw
	}
	return simpleByteWriter{writer}
}

// BitWriter is used to write individual bits.
type BitWriter struct {
	writer    io.ByteWriter
	saved     byte
	savedBits byte
}

// NewBitWriter makes a new BitWriter.
func NewBitWriter(writer io.Writer) BitWriter {
	return BitWriter{makeByteWriter(writer), 0, 0}
}

// WriteBits writes up to 64 bits.
func (bw *BitWriter) WriteBits(v uint64, count byte) error {
	if count > 64 {
		return bytes.ErrTooLarge
	}
	bits := count + bw.savedBits
	x := bw.saved
	for bits >= 8 {
		bits -= 8
		x |= byte((v >> bits) & 0xff)
		err := bw.writer.WriteByte(x)
		if err != nil {
			return err
		}
		x = 0
	}
	bw.saved = x | byte(v<<(8-bits))
	bw.savedBits = bits
	return nil
}

// WriteBit writes a single bit.
func (bw *BitWriter) WriteBit(bit byte) error {
	return bw.WriteBits(uint64(bit), 1)
}

// Finalize pads out any partially filled octet with the high bits of pad.
func (bw *BitWriter) Finalize(pad byte) error {
	if bw.savedBits > 0 {
		err := bw.writer.WriteByte(bw.saved | (pad >> bw.savedBits))
		if err != nil {
			return err
		}
	}
	return nil
}

type simpleByteReader struct {
	reader io.Reader
}

func (sbr simpleByteReader) ReadByte() (byte, error) {
	buf := make([]byte, 1)
	n, err := sbr.reader.Read(buf)
	if err != nil {
		return 0, err
	}
	if n != 1 {
		return 0, io.ErrNoProgress
	}
	return buf[0], nil
}

func makeByteReader(reader io.Reader) io.ByteReader {
	br, ok := reader.(io.ByteReader)
	if ok {
		return br
	}
	return simpleByteReader{reader}
}
