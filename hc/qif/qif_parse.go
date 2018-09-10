package main

import (
	"bytes"
	"io"

	"github.com/martinthomson/minhq/hc"
)

// Reader reads QIF files: https://github.com/quicwg/base-drafts/wiki/QPACK-Offline-Interop
type Reader struct {
	r   io.Reader
	eol bool
}

// NewReader creates a new Reader, wrapping the underlying reader.
func NewReader(r io.Reader) *Reader {
	return &Reader{r, false}
}

func (qr *Reader) reallyReadByte() (byte, error) {
	byteReader, ok := qr.r.(io.ByteReader)
	if ok {
		return byteReader.ReadByte()
	}
	buf := [1]byte{}
	n, err := qr.r.Read(buf[:])
	if err != nil {
		return 0, err
	}
	if n != 1 {
		return 0, io.ErrNoProgress
	}
	return buf[0], nil
}

func (qr *Reader) readByte() (byte, error) {
	b, err := qr.reallyReadByte()
	if err == nil && qr.eol && b == '\n' {
		b, err = qr.reallyReadByte()
	}
	qr.eol = b == '\r'
	return b, err
}

func (qr *Reader) readLine() ([]byte, error) {
	var buf bytes.Buffer
	for {
		b, err := qr.readByte()
		if err != nil {
			return nil, err
		}
		switch b {
		case '\r', '\n':
			return buf.Bytes(), nil
		default:
			err = buf.WriteByte(b)
			if err != nil {
				return nil, err
			}
		}
	}
}

func (qr *Reader) ignoreUntilEol() error {
	for {
		b, err := qr.readByte()
		if err != nil {
			return err
		}
		if b == '\r' || b == '\n' {
			return nil
		}
	}
}

// Read a single header field.  Returns nil, nil if the line was empty.
func (qr *Reader) readHeaderField() (*hc.HeaderField, error) {
	line, err := qr.readLine()
	if err != nil {
		return nil, err
	}

	// Empty line: end of block.
	if len(line) == 0 {
		return nil, nil
	}

	// Skip all comment lines.
	for line[0] == '#' {
		line, err = qr.readLine()
		if err != nil {
			return nil, err
		}
	}

	splitLine := bytes.SplitN(line, []byte{'\t'}, 2)
	return &hc.HeaderField{Name: string(splitLine[0]), Value: string(splitLine[1])}, nil
}

// ReadHeaderBlock reads a single header block.
func (qr *Reader) ReadHeaderBlock() ([]hc.HeaderField, error) {
	var block []hc.HeaderField
	for {
		hf, err := qr.readHeaderField()
		if err != nil {
			return nil, err
		}
		if hf == nil {
			return block, nil
		}
		block = append(block, *hf)
	}
}
