package minhq

import (
	"bytes"
	"io"
)

// BitWriter is used to write individual bits.
type BitWriter struct {
	writer    io.Writer
	saved     uint64
	savedBits byte
}

// NewBitWriter makes a new BitWriter.
func NewBitWriter(writer io.Writer) BitWriter {
	return BitWriter{writer, 0, 0}
}

// Writes out a byte to the underlying writer.
func (bw *BitWriter) writeByteInternal(c byte) error {
	byteWriter, ok := bw.writer.(io.ByteWriter)
	if ok {
		return byteWriter.WriteByte(c)
	}
	n, err := bw.writer.Write([]byte{c})
	if err != nil {
		return err
	}
	if n == 0 {
		return io.ErrShortWrite
	}
	return nil
}

// Writes out any whole octets from the saved bits.
func (bw *BitWriter) writeSaved() error {
	for bw.savedBits >= 8 {
		x := byte(bw.saved >> (bw.savedBits - 8))
		err := bw.writeByteInternal(x)
		if err != nil {
			return err
		}
		bw.savedBits -= 8
	}
	return nil
}

// WriteBits writes up to 64 bits.
func (bw *BitWriter) WriteBits(v uint64, count byte) error {
	if count > 64 {
		return bytes.ErrTooLarge
	} else if count < 64 && v >= (1<<count) {
		return bytes.ErrTooLarge
	}

	if bw.savedBits+count < 8 {
		bw.savedBits += count
		bw.saved = (bw.saved << count) | v
		return nil
	}

	// Attempt to re-write anything that we might have saved from last time.
	err := bw.writeSaved()
	if err != nil {
		return err
	}

	// Here we don't save anything until the first write succeeds.
	remainder := count + bw.savedBits - 8
	x := byte((bw.saved << (8 - bw.savedBits)) | (v >> remainder))
	err = bw.writeByteInternal(x)
	if err != nil {
		return err
	}
	bw.saved = v
	bw.savedBits = remainder

	// But if the first write succeeds, pretend that it worked because the
	// extra bits were saved anyway.
	_ = bw.writeSaved()
	return nil
}

// WriteBit writes a single bit.
func (bw *BitWriter) WriteBit(bit byte) error {
	return bw.WriteBits(uint64(bit), 1)
}

// WriteByte so that we can claim to implement the io.ByteWriter interface.
func (bw *BitWriter) WriteByte(c byte) error {
	return bw.WriteBits(uint64(c), 8)
}

// Write so that we can claim to implement the io.Writer interface.
func (bw *BitWriter) Write(p []byte) (int, error) {
	if bw.savedBits == 0 {
		return bw.writer.Write(p)
	}
	for i, b := range p {
		err := bw.WriteByte(b)
		if err != nil {
			return i, err
		}
	}
	return len(p), nil
}

// Pad pads out any partially filled octet with the high bits of pad.
// Pad also serves as a flush, in case there are saved bits that couldn't be written.
func (bw *BitWriter) Pad(pad byte) error {
	if bw.savedBits > 0 {
		err := bw.writeSaved()
		if err != nil {
			return err
		}
		err = bw.WriteBits(uint64(pad>>bw.savedBits), 8-bw.savedBits)
		if err != nil {
			return err
		}
		bw.saved = 0
		bw.savedBits = 0
	}
	return nil
}

// BitReader reads individual bits
type BitReader struct {
	reader    io.Reader
	saved     uint64
	savedBits byte
}

// NewBitReader makes a new BitWriter.
func NewBitReader(reader io.Reader) *BitReader {
	return &BitReader{reader, 0, 0}
}

func (br *BitReader) readByteInternal() (byte, error) {
	byteReader, ok := br.reader.(io.ByteReader)
	if ok {
		return byteReader.ReadByte()
	}
	buf := make([]byte, 1)
	n, err := br.reader.Read(buf)
	if err != nil {
		return 0, err
	}
	if n != 1 {
		return 0, io.ErrNoProgress
	}
	return buf[0], nil
}

// Read the next octet and update the saved state.
func (br *BitReader) readNext() error {
	b, err := br.readByteInternal()
	if err != nil {
		return err
	}
	br.saved = (br.saved << 8) | uint64(b)
	br.savedBits += 8
	return nil
}

// ReadBit reads a single bit.
func (br *BitReader) ReadBit() (byte, error) {
	if br.savedBits > 0 {
		br.savedBits--
		return byte(br.saved>>br.savedBits) & 1, nil
	}

	err := br.readNext()
	if err != nil {
		return 0, err
	}
	br.savedBits--
	return byte(br.saved>>br.savedBits) & 1, nil
}

// ReadBits reads up to 64 bits.
func (br *BitReader) ReadBits(count byte) (uint64, error) {
	if count > 64 {
		return 0, bytes.ErrTooLarge
	}

	// Note the contract here: br.saved and br.savedBits are always updated after
	// reading a byte.  That way, if there is an error, those values are accurate.
	// However, after we use it, br.saved can contain junk above br.savedBits.
	for br.savedBits+8 <= count {
		err := br.readNext()
		if err != nil {
			return 0, err
		}
	}
	if br.savedBits >= count {
		br.savedBits -= count
		return (br.saved >> br.savedBits) & (^uint64(0) >> (64 - count)), nil
	}
	result := br.saved & (^uint64(0) >> (64 - br.savedBits))
	remainder := count - br.savedBits

	// Can't use readNext() because br.saved might overflow.
	b, err := br.readByteInternal()
	if err != nil {
		return 0, err
	}
	br.saved = uint64(b)
	br.savedBits = 8 - remainder
	return (result << remainder) | (br.saved >> (8 - remainder)), nil
}

// ReadByte so that we can claim to support the io.ByteReader interface.
func (br *BitReader) ReadByte() (byte, error) {
	if br.savedBits == 0 {
		return br.readByteInternal()
	}
	b, err := br.ReadBits(8)
	return byte(b), err
}

// Read so that we can claim to support the io.Reader interface.
func (br *BitReader) Read(p []byte) (int, error) {
	if br.savedBits == 0 {
		return br.reader.Read(p)
	}
	for i := range p {
		b, err := br.ReadByte()
		if err != nil {
			return i, err
		}
		p[i] = b
	}
	return len(p), nil
}
