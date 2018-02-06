package hpack

import (
	"errors"
	"io"
)

// ErrIndexError is a decoder error for the case where an invalid index is
// received.
var ErrIndexError = errors.New("HPACK decoder read an invalid index")

// ErrPseudoHeaderOrdering indicates that a pseudo header field was placed after
// a non-pseudo header field.
var ErrPseudoHeaderOrdering = errors.New("Pseudo header field ordering")

// HeaderField is the interface that header fields need to comply with.
type HeaderField struct {
	Name      string
	Value     string
	Sensitive bool
}

// Decoder is the top-level class for header decompression.
type Decoder struct {
	table Table
}

func (decoder *Decoder) readIndexed(reader *Reader) (*HeaderField, error) {
	index, err := reader.ReadInt(7)
	if err != nil {
		return nil, err
	}
	entry := decoder.table.Get(int(index))
	if entry == nil {
		return nil, ErrIndexError
	}
	return &HeaderField{entry.Name(), entry.Value(), false}, nil
}

func (decoder *Decoder) readNameValue(reader *Reader, prefix byte) (string, string, error) {
	index, err := reader.ReadInt(prefix)
	if err != nil {
		return "", "", err
	}
	var name string
	if index == 0 {
		name, err = reader.ReadString()
		if err != nil {
			return "", "", err
		}
	} else {
		entry := decoder.table.Get(int(index))
		if entry == nil {
			return "", "", ErrIndexError
		}
		name = entry.Name()
	}
	value, err := reader.ReadString()
	if err != nil {
		return "", "", err
	}
	return name, value, nil
}

func (decoder *Decoder) readIncremental(reader *Reader) (*HeaderField, error) {
	name, value, err := decoder.readNameValue(reader, 6)
	if err != nil {
		return nil, err
	}
	decoder.table.Insert(name, value)
	return &HeaderField{name, value, false}, nil
}

func (decoder *Decoder) readCapacity(reader *Reader) error {
	capacity, err := reader.ReadInt(5)
	if err != nil {
		return err
	}
	decoder.table.SetCapacity(TableCapacity(capacity))
	return nil
}

func (decoder *Decoder) readLiteral(reader *Reader) (*HeaderField, error) {
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
func (decoder *Decoder) ReadHeaderBlock(r io.Reader) ([]HeaderField, error) {
	reader := NewHpackReader(r)
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

// Encoder is the top-level class for header compression.
type Encoder struct {
	table Table
	// Track changes to capacity so that we can reflect them properly.
	minCapacity  TableCapacity
	nextCapacity TableCapacity
	indexPrefs   map[string]bool
}

func (encoder *Encoder) writeCapacity(writer *Writer, c TableCapacity) error {
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

func (encoder *Encoder) writeCapacityChange(writer *Writer) error {
	if encoder.minCapacity < encoder.table.capacity {
		err := encoder.writeCapacity(writer, encoder.minCapacity)
		if err != nil {
			return err
		}
		encoder.table.SetCapacity(encoder.minCapacity)
	}
	if encoder.nextCapacity > encoder.table.capacity {
		err := encoder.writeCapacity(writer, encoder.nextCapacity)
		if err != nil {
			return err
		}
		encoder.table.SetCapacity(encoder.nextCapacity)
	}
	return nil
}

func (encoder Encoder) writeIndexed(writer *Writer, entry Entry) error {
	err := writer.WriteBit(1)
	if err != nil {
		return err
	}
	return writer.WriteInt(uint64(entry.Index()), 7)
}

func (encoder Encoder) avoidIndexing(h HeaderField) bool {
	// Ignore the values here.
	var dontIndex = map[string]bool{
		":path":               true,
		"content-length":      true,
		"content-range":       true,
		"date":                true,
		"expires":             true,
		"etag":                true,
		"if-modified-since":   true,
		"if-range":            true,
		"if-unmodified-since": true,
		"last-modified":       true,
		"link":                true,
		"range":               true,
		"referer":             true,
		"refresh":             true,
	}

	if h.Sensitive {
		return true
	}
	pref, ok := encoder.indexPrefs[h.Name]
	if ok {
		return pref
	}
	_, d := dontIndex[h.Name]
	if d {
		return true
	}
	return false
}

func (encoder Encoder) writeIncremental(writer *Writer, h HeaderField,
	nameEntry Entry) error {
	err := writer.WriteBits(1, 2)
	if err != nil {
		return err
	}

	nameIndex := uint64(0)
	if nameEntry != nil {
		nameIndex = uint64(nameEntry.Index())
	}
	err = writer.WriteInt(nameIndex, 6)
	if err != nil {
		return err
	}
	if nameEntry == nil {
		err = writer.WriteString(h.Name)
		if err != nil {
			return err
		}
	}

	return writer.WriteString(h.Value)
}

func (encoder Encoder) writeLiteral(writer *Writer, h HeaderField,
	nameEntry Entry) error {
	code := uint64(0)
	if h.Sensitive {
		code = 1
	}
	err := writer.WriteBits(code, 4)
	if err != nil {
		return err
	}

	nameIndex := uint64(0)
	if nameEntry != nil {
		nameIndex = uint64(nameEntry.Index())
	}
	err = writer.WriteInt(nameIndex, 4)
	if err != nil {
		return err
	}
	if nameEntry == nil {
		err = writer.WriteString(h.Name)
		if err != nil {
			return err
		}
	}

	return writer.WriteString(h.Value)
}

// WriteHeaderBlock writes out a header block.
func (encoder *Encoder) WriteHeaderBlock(w io.Writer, headers ...HeaderField) error {
	writer := NewHpackWriter(w)
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
		m, nm := encoder.table.Lookup(name, value)
		if m != nil {
			err = encoder.writeIndexed(writer, m)
		} else {
			if encoder.avoidIndexing(h) {
				err = encoder.writeLiteral(writer, h, nm)
			} else {
				err = encoder.writeIncremental(writer, h, nm)
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
func (encoder *Encoder) SetCapacity(c TableCapacity) {
	if c < encoder.minCapacity {
		encoder.minCapacity = c
	}
	encoder.nextCapacity = c
}

// SetIndexPreference sets preferences for header fields with the given name.
// Set to true to index, false to never index.
func (encoder *Encoder) SetIndexPreference(name string, pref bool) {
	encoder.indexPrefs[name] = pref
}

// ClearIndexPreference resets the preference for indexing for the named header field.
func (encoder *Encoder) ClearIndexPreference(name string) {
	delete(encoder.indexPrefs, name)
}
