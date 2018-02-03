package minhq

import (
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

/*
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
	n, err := io.ReadFull(hr, buf)
	s := ""
	return s, nil
}*/
