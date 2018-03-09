package bitio

import (
	"bytes"
	"io"
)

// BitWriter is used to write individual bits.
type BitWriter interface {
	io.Writer
	io.ByteWriter
	WriteBit(count byte) error
	WriteBits(value uint64, count byte) error
	Pad(pad byte) error
	Written() int64
}
type bitWriter struct {
	writer    io.Writer
	saved     uint64
	savedBits byte
	written   int64
}

// NewBitWriter makes a new BitWriter.
func NewBitWriter(writer io.Writer) BitWriter {
	bw, ok := writer.(BitWriter)
	if ok {
		return bw
	}
	return &bitWriter{writer, 0, 0, 0}
}

func (bw *bitWriter) Written() int64 {
	if bw.savedBits > 0 {
		panic("Checking written with partially written bytes")
	}
	return bw.written
}

// Writes out a byte to the underlying writer.
func (bw *bitWriter) writeByteInternal(c byte) error {
	byteWriter, ok := bw.writer.(io.ByteWriter)
	if ok {
		bw.written++
		return byteWriter.WriteByte(c)
	}
	n, err := bw.writer.Write([]byte{c})
	if err != nil {
		return err
	}
	if n == 0 {
		return io.ErrShortWrite
	}
	bw.written += int64(n)
	return nil
}

// Writes out any whole octets from the saved bits.
func (bw *bitWriter) writeSaved() error {
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
func (bw *bitWriter) WriteBits(v uint64, count byte) error {
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
func (bw *bitWriter) WriteBit(bit byte) error {
	return bw.WriteBits(uint64(bit), 1)
}

// WriteByte so that we can claim to implement the io.ByteWriter interface.
func (bw *bitWriter) WriteByte(c byte) error {
	return bw.WriteBits(uint64(c), 8)
}

// Write so that we can claim to implement the io.Writer interface.
func (bw *bitWriter) Write(p []byte) (int, error) {
	if bw.savedBits == 0 {
		n, err := bw.writer.Write(p)
		bw.written += int64(n)
		return n, err
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
func (bw *bitWriter) Pad(pad byte) error {
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
type BitReader interface {
	io.Reader
	io.ByteReader
	ReadBit() (byte, error)
	ReadBits(count byte) (uint64, error)
}

type bitReader struct {
	reader    io.Reader
	saved     uint64
	savedBits byte
}

// NewBitReader makes a new BitWriter.  If the reader is already a BitReader, return that instead.
func NewBitReader(reader io.Reader) BitReader {
	br, ok := reader.(BitReader)
	if ok {
		return br
	}
	return &bitReader{reader, 0, 0}
}

func (br *bitReader) readByteInternal() (byte, error) {
	byteReader, ok := br.reader.(io.ByteReader)
	if ok {
		return byteReader.ReadByte()
	}
	buf := [1]byte{}
	n, err := br.reader.Read(buf[:])
	if err != nil {
		return 0, err
	}
	if n != 1 {
		return 0, io.ErrNoProgress
	}
	return buf[0], nil
}

// Read the next octet and update the saved state.
func (br *bitReader) readNext() error {
	b, err := br.readByteInternal()
	if err != nil {
		return err
	}
	br.saved = (br.saved << 8) | uint64(b)
	br.savedBits += 8
	return nil
}

// ReadBit reads a single bit.
func (br *bitReader) ReadBit() (byte, error) {
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
func (br *bitReader) ReadBits(count byte) (uint64, error) {
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
func (br *bitReader) ReadByte() (byte, error) {
	if br.savedBits == 0 {
		return br.readByteInternal()
	}
	b, err := br.ReadBits(8)
	return byte(b), err
}

// Read so that we can claim to support the io.Reader interface. This does
// unaligned reads if it is preceded by reads for a number of bits that aren't a
// whole multiple of 8.
func (br *bitReader) Read(p []byte) (int, error) {
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
