package hpack

import (
	"bytes"
	"errors"
	"io"

	"github.com/martinthomson/minhq/bitio"
)

// ErrIntegerOverflow is used to signal integer overflow.
var ErrIntegerOverflow = errors.New("HPACK integer overflow")

// Reader wraps BitReader with more methods
type Reader struct {
	bitio.BitReader
}

// NewHpackReader wraps the reader with HPACK-specific reading functions.
func NewHpackReader(reader io.Reader) *Reader {
	return &Reader{*bitio.NewBitReader(reader)}
}

// ReadInt reads an HPACK integer with the specified prefix length.
func (hr *Reader) ReadInt(prefix byte) (uint64, error) {
	v, err := hr.ReadBits(prefix)
	if err != nil {
		return 0, err
	}
	if v < ((1 << prefix) - 1) {
		return v, nil
	}

	for s := uint8(0); s < 64; s += 7 {
		b, err := hr.ReadBits(8)
		if err != nil {
			return 0, err
		}
		// When the shift hits 63, then don't allow the next byte to overflow.
		// If that octet is > 1, then assume that it will overflow (don't
		// allow extra zero bits beyond this point, even though 0x80 can be
		// added indefinitely without increasing the value).  If the octet is
		// 1, then it can still overflow if the current value already has the
		// same bit set.  If the octet is 0, then it's OK.
		//
		if s == 63 && (b > 1 || (b == 1 && ((v >> 63) == 1))) {
			return 0, ErrIntegerOverflow
		}
		v += (b & 0x7f) << s
		if (b & 0x80) == 0 {
			break
		}
	}
	return v, nil
}

// ReadString reads an HPACK-encoded string.
func (hr *Reader) ReadString() (string, error) {
	huffman, err := hr.ReadBit()
	if err != nil {
		return "", nil
	}
	len, err := hr.ReadInt(7)
	if err != nil {
		return "", nil
	}
	var valueReader io.Reader
	valueReader = &io.LimitedReader{R: hr, N: int64(len)}
	var buf []byte
	if huffman != 0 {
		valueReader = NewHuffmanDecompressor(valueReader)
		// Allocate enough for maximum HPACK expansion.
		buf = make([]byte, len*8/5+1)
	} else {
		buf = make([]byte, len)
	}

	n, err := io.ReadFull(valueReader, buf)
	if err != nil && err != io.ErrUnexpectedEOF {
		return "", err
	}
	return string(buf[0:n]), nil
}

// Writer wraps BitWriter with more methods
type Writer struct {
	bitio.BitWriter
}

// NewHpackWriter wraps the writer with HPACK-specific writing functions.
func NewHpackWriter(writer io.Writer) *Writer {
	return &Writer{*bitio.NewBitWriter(writer)}
}

// WriteInt writes an integer of the specific prefix length.
func (hw *Writer) WriteInt(p uint64, prefix byte) error {
	if prefix > 8 || prefix == 0 {
		return errors.New("invalid HPACK integer prefix")
	}
	ones := (uint64(1) << prefix) - 1
	if p < ones {
		return hw.WriteBits(p, prefix)
	}
	err := hw.WriteBits(ones, prefix)
	if err != nil {
		return err
	}
	p -= ones
	for done := false; !done; {
		b := byte(p & 0x7f)
		p >>= 7
		if p > 0 {
			b |= 0x80
		} else {
			done = true
		}
		err = hw.WriteByte(b)
		if err != nil {
			return err
		}
	}
	return nil
}

// HuffmanCodingChoice controls whether Huffman coding is used.
type HuffmanCodingChoice byte

const (
	// HuffmanCodingAuto attempts to use Huffman, but will choose not to
	// if this causes the encoding to grow in size.
	HuffmanCodingAuto = HuffmanCodingChoice(iota)
	// HuffmanCodingAlways = HuffmanCodingChoice(iota)
	HuffmanCodingAlways = HuffmanCodingChoice(iota)
	// HuffmanCodingNever = HuffmanCodingChoice(iota)
	HuffmanCodingNever = HuffmanCodingChoice(iota)
)

// WriteStringRaw writes out the specified string.
func (hw *Writer) WriteStringRaw(s string, huffman HuffmanCodingChoice) error {
	var reader io.Reader
	reader = bytes.NewReader([]byte(s))
	l := len(s)
	hbit := byte(0)
	if huffman != HuffmanCodingNever {
		var buf bytes.Buffer
		compressor := NewHuffmanCompressor(&buf)
		n, err := io.Copy(compressor, reader)
		if err != nil {
			return err
		}
		if n < int64(l) {
			return io.ErrShortWrite
		}
		err = compressor.Pad()
		if err != nil {
			return err
		}

		if (huffman == HuffmanCodingAlways) || (buf.Len() < l) {
			reader = &buf
			l = buf.Len()
			hbit = 1
		} else {
			reader = bytes.NewReader([]byte(s))
		}
	}

	err := hw.WriteBit(hbit)
	if err != nil {
		return err
	}
	err = hw.WriteInt(uint64(l), 7)
	if err != nil {
		return err
	}
	n, err := io.Copy(hw, reader)
	if err != nil {
		return err
	}
	if n < int64(l) {
		return io.ErrShortWrite
	}
	return nil
}

// WriteString writes a string, using automatic Huffman coding.
func (hw *Writer) WriteString(s string) error {
	return hw.WriteStringRaw(s, HuffmanCodingAuto)
}
