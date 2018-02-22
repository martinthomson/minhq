package bitio_test

import (
	"bytes"
	"io"
	"testing"

	"github.com/martinthomson/minhq/bitio"
	"github.com/stvp/assert"
)

func TestWriterWrap(t *testing.T) {
	var buf bytes.Buffer
	bw1 := bitio.NewBitWriter(&buf)
	bw2 := bitio.NewBitWriter(bw1)
	assert.Equal(t, bw1, bw2)

	p := []byte{1, 2, 3}
	n, err := bw1.Write(p)
	assert.Nil(t, err)
	assert.Equal(t, len(p), n)
	assert.Equal(t, p, buf.Bytes())
}

func TestWriter(t *testing.T) {
	var buf bytes.Buffer
	writer := bitio.NewBitWriter(&buf)
	assert.Nil(t, writer.WriteBit(0))
	assert.Equal(t, 0, len(buf.Bytes()))
	assert.Nil(t, writer.WriteBit(1))
	assert.Equal(t, 0, len(buf.Bytes()))
	assert.Nil(t, writer.WriteBits(1, 7))
	assert.Equal(t, []byte{0x40}, buf.Bytes())
	assert.Nil(t, writer.Pad(0x55))
	assert.Equal(t, []byte{0x40, 0xaa}, buf.Bytes())
	assert.Nil(t, writer.WriteBits(1, 64))
	assert.Equal(t, []byte{0x40, 0xaa, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
		buf.Bytes())
	assert.Nil(t, writer.WriteBits(1, 3))
	assert.Nil(t, writer.WriteBits(^uint64(0), 64))
	assert.Nil(t, writer.Pad(0x03))
	assert.Equal(t, []byte{0x40, 0xaa, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01,
		0x3f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xe0},
		buf.Bytes())
}

type blockingByteWriter struct {
	writer     io.ByteWriter
	writesLeft int
}

// WriteByte writes, unless writesLeft hits zero.
func (bbw *blockingByteWriter) WriteByte(b byte) error {
	bbw.writesLeft -= 1
	if bbw.writesLeft == 0 {
		return io.ErrShortWrite
	}
	return bbw.writer.WriteByte(b)
}

// Write fulfills the io.Writer contract.
func (bbw *blockingByteWriter) Write(p []byte) (int, error) {
	for i, b := range p {
		err := bbw.WriteByte(b)
		if err != nil {
			return i, err
		}
	}
	return len(p), nil
}

// This test is probably invalid on the grounds that blocking doesn't manifest
// as an error.
func TestBlockingWrite(t *testing.T) {
	var buf bytes.Buffer
	writer := bitio.NewBitWriter(&blockingByteWriter{&buf, 1})
	assert.Nil(t, writer.WriteBit(1)) // buffered
	assert.NotNil(t, writer.WriteBits(1, 7))
	assert.Nil(t, writer.WriteBits(1, 7))
	assert.Equal(t, []byte{0x81}, buf.Bytes())

	buf.Truncate(0)
	writer = bitio.NewBitWriter(&blockingByteWriter{&buf, 2})
	assert.Nil(t, writer.WriteBits(0xffff, 16))
	assert.Equal(t, []byte{0xff}, buf.Bytes())
	assert.Nil(t, writer.WriteBits(0x5555, 16))
	assert.Equal(t, []byte{0xff, 0xff, 0x55, 0x55}, buf.Bytes())
}

func TestWriteError(t *testing.T) {
	var buf bytes.Buffer
	writer := bitio.NewBitWriter(&buf)
	assert.NotNil(t, writer.WriteBits(1, 65))
	assert.NotNil(t, writer.WriteBits(2, 1))
}

func TestReaderWrap(t *testing.T) {
	p := []byte{1, 2, 3}
	buf := bytes.NewBuffer(p)
	br1 := bitio.NewBitReader(buf)
	br2 := bitio.NewBitReader(br1)
	assert.Equal(t, br1, br2)
	r := make([]byte, len(p))
	n, err := br1.Read(r)
	assert.Nil(t, err)
	assert.Equal(t, len(p), n)
	assert.Equal(t, p, r)
}

func TestReader(t *testing.T) {
	buf := bytes.NewReader([]byte{0x40, 0xaa, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01,
		0x3f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xe0})
	reader := bitio.NewBitReader(buf)
	b, err := reader.ReadBit()
	assert.Nil(t, err)
	assert.Equal(t, uint8(0), b)
	b, err = reader.ReadBit()
	assert.Nil(t, err)
	assert.Equal(t, uint8(1), b)
	v, err := reader.ReadBits(7)
	assert.Nil(t, err)
	assert.Equal(t, uint64(1), v)
	v, err = reader.ReadBits(7)
	assert.Nil(t, err)
	assert.Equal(t, uint64(0x55>>1), v)
	v, err = reader.ReadBits(64)
	assert.Nil(t, err)
	assert.Equal(t, uint64(1), v)
	v, err = reader.ReadBits(3)
	assert.Nil(t, err)
	assert.Equal(t, uint64(1), v)
	v, err = reader.ReadBits(64)
	assert.Nil(t, err)
	assert.Equal(t, ^uint64(0), v)
	v, err = reader.ReadBits(5)
	assert.Nil(t, err)
	assert.Equal(t, uint64(0x03>>3), v)
}
