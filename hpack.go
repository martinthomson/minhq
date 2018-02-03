package minhq

import (
	"bytes"
	"io"
)

// HpackReader wraps BitReader with more methods
type HpackReader struct {
	BitReader
}

// NewHpackReader wraps the reader with HPACK-specific reading functions.
func NewHpackReader(reader io.Reader) *HpackReader {
	return &HpackReader{*NewBitReader(reader)}
}

// ReadInt reads an HPACK integer with the specified prefix length.
func (hr *HpackReader) ReadInt(prefix byte) (uint64, error) {
	v, err := hr.ReadBits(prefix)
	if err != nil {
		return 0, err
	}
	if v < ((1 << prefix) - 1) {
		return v, nil
	}

	for done := false; !done; {
		b, err := hr.ReadBits(8)
		if err != nil {
			return 0, err
		}
		v = (v << 7) | (b & 0x7f)
		done = b&0x80 != 0
	}
	return v, nil
}

// ReadString reads an HPACK-encoded string.
func (hr *HpackReader) ReadString() (string, error) {
	huffman, err := hr.ReadBit()
	if err != nil {
		return "", nil
	}
	len, err := hr.ReadInt(7)
	if err != nil {
		return "", nil
	}
	buf := make([]byte, len)
	n, err := io.ReadFull(hr, buf[0:len])
	if err != nil {
		return "", nil
	}
	if huffman != 0 {
		dec := NewHuffmanDecompressor(bytes.NewReader(buf))
		// Allocate enough for maximum HPACK expansion.
		expanded := make([]byte, len*8/5+1)
		n, err = io.ReadFull(dec, buf[0:len])
		if err != nil && err != io.ErrUnexpectedEOF {
			return "", err
		}
		buf = expanded[0:n]
	}

	return string(buf), nil
}
