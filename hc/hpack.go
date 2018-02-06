package hc

import (
	"io"
)

// HpackDecoder is the top-level class for header decompression.
type HpackDecoder struct {
	decoderCommon
}

func (decoder *HpackDecoder) readIndexed(reader *Reader) (*HeaderField, error) {
	index, err := reader.ReadInt(7)
	if err != nil {
		return nil, err
	}
	entry := decoder.Table.Get(int(index))
	if entry == nil {
		return nil, ErrIndexError
	}
	return &HeaderField{entry.Name(), entry.Value(), false}, nil
}

func (decoder *HpackDecoder) readIncremental(reader *Reader) (*HeaderField, error) {
	name, value, err := decoder.readNameValue(reader, 6)
	if err != nil {
		return nil, err
	}
	decoder.Table.Insert(name, value)
	return &HeaderField{name, value, false}, nil
}

func (decoder *HpackDecoder) readCapacity(reader *Reader) error {
	capacity, err := reader.ReadInt(5)
	if err != nil {
		return err
	}
	decoder.Table.SetCapacity(TableCapacity(capacity))
	return nil
}

func (decoder *HpackDecoder) readLiteral(reader *Reader) (*HeaderField, error) {
	ni, err := reader.ReadBit()
	if err != nil {
		return nil, err
	}

	name, value, err := decoder.readNameValue(reader, 4)
	if err != nil {
		return nil, err
	}
	return &HeaderField{name, value, ni == 1}, nil
}

// ReadHeaderBlock decodes header fields as they arrive.
func (decoder *HpackDecoder) ReadHeaderBlock(r io.Reader) ([]HeaderField, error) {
	reader := NewReader(r)
	headers := []HeaderField{}
	for {
		b, err := reader.ReadBit()
		if err == io.EOF {
			break // Success!
		}
		if err != nil {
			return nil, err
		}

		if b == 1 {
			h, err := decoder.readIndexed(reader)
			if err != nil {
				return nil, err
			}
			headers = append(headers, *h)
			continue
		}

		b, err = reader.ReadBit()
		if err != nil {
			return nil, err
		}

		if b == 1 {
			h, err := decoder.readIncremental(reader)
			if err != nil {
				return nil, err
			}
			headers = append(headers, *h)
			continue
		}

		b, err = reader.ReadBit()
		if err != nil {
			return nil, err
		}

		if b == 1 {
			err := decoder.readCapacity(reader)
			if err != nil {
				return nil, err
			}
			continue
		}

		h, err := decoder.readLiteral(reader)
		if err != nil {
			return nil, err
		}
		headers = append(headers, *h)
	}

	// Sanity-check header ordering.
	pseudo := true
	for _, h := range headers {
		if h.Name[0] == ':' {
			if !pseudo {
				return nil, ErrPseudoHeaderOrdering
			}
		} else {
			pseudo = false
		}
	}
	return headers, nil
}

// HpackEncoder is the top-level class for header compression.
type HpackEncoder struct {
	encoderCommon
	// Track changes to capacity so that we can reflect them properly.
	minCapacity  TableCapacity
	nextCapacity TableCapacity
}

func (encoder *HpackEncoder) writeCapacity(writer *Writer, c TableCapacity) error {
	err := writer.WriteBits(1, 3)
	if err != nil {
		return err
	}
	err = writer.WriteInt(uint64(c), 5)
	if err != nil {
		return err
	}
	return nil
}

func (encoder *HpackEncoder) writeCapacityChange(writer *Writer) error {
	if encoder.minCapacity < encoder.Table.capacity {
		err := encoder.writeCapacity(writer, encoder.minCapacity)
		if err != nil {
			return err
		}
		encoder.Table.SetCapacity(encoder.minCapacity)
	}
	if encoder.nextCapacity > encoder.Table.capacity {
		err := encoder.writeCapacity(writer, encoder.nextCapacity)
		if err != nil {
			return err
		}
		encoder.Table.SetCapacity(encoder.nextCapacity)
		encoder.minCapacity = encoder.nextCapacity
	}
	return nil
}

func (encoder *HpackEncoder) writeIndexed(writer *Writer, entry Entry) error {
	err := writer.WriteBit(1)
	if err != nil {
		return err
	}
	return writer.WriteInt(uint64(entry.Index()), 7)
}

func (encoder *HpackEncoder) writeIncremental(writer *Writer, h HeaderField, nameEntry Entry) error {
	err := writer.WriteBits(1, 2)
	if err != nil {
		return err
	}

	err = encoder.writeNameValue(writer, h, nameEntry, 6)
	if err != nil {
		return err
	}
	_ = encoder.Table.Insert(h.Name, h.Value)
	return nil
}

func (encoder HpackEncoder) writeLiteral(writer *Writer, h HeaderField, nameEntry Entry) error {
	code := uint64(0)
	if h.Sensitive {
		code = 1
	}
	err := writer.WriteBits(code, 4)
	if err != nil {
		return err
	}

	return encoder.writeNameValue(writer, h, nameEntry, 4)
}

// WriteHeaderBlock writes out a header block.
func (encoder *HpackEncoder) WriteHeaderBlock(w io.Writer, headers ...HeaderField) error {
	writer := NewWriter(w)
	err := encoder.writeCapacityChange(writer)
	if err != nil {
		return err
	}
	pseudo := true
	for _, h := range headers {
		name, value := h.Name, h.Value
		if name[0] == ':' {
			if !pseudo {
				return ErrPseudoHeaderOrdering
			}
		} else {
			pseudo = false
		}
		if h.Sensitive {
			// It's not clear here whether the name is sensitive, but let's assume that
			// it might be. It's not exactly rational to put secrets in header field
			// names (how do you find them again?), but it's safer not to assume rational
			// behaviour.
			err = encoder.writeLiteral(writer, h, nil)
		} else {
			m, nm := encoder.Table.Lookup(name, value)
			if m != nil {
				err = encoder.writeIndexed(writer, m)
			} else if encoder.shouldIndex(h) {
				err = encoder.writeIncremental(writer, h, nm)
			} else {
				err = encoder.writeLiteral(writer, h, nm)
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// SetCapacity is used to set the new header table capacity. This could reflect
// the value from the peer's settings. Smaller values than the one provided by
// the peer can be set, if there are constraints on memory and the peer isn't
// trusted to set sane values. Failing to call this will result in no additions
// to the dynamic table and poor compression performance.
func (encoder *HpackEncoder) SetCapacity(c TableCapacity) {
	if c < encoder.minCapacity {
		encoder.minCapacity = c
	}
	encoder.nextCapacity = c
}
