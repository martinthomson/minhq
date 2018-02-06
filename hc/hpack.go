package hc

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

// HpackDecoder is the top-level class for header decompression.
type HpackDecoder struct {
	Table Table
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

func (decoder *HpackDecoder) readNameValue(reader *Reader, prefix byte) (string, string, error) {
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
		entry := decoder.Table.Get(int(index))
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
	// Table is public to provide access to its methods.
	Table Table
	// HuffmanPreference records preferences for Huffman coding of strings.
	HuffmanPreference HuffmanCodingChoice
	// Track changes to capacity so that we can reflect them properly.
	minCapacity  TableCapacity
	nextCapacity TableCapacity
	indexPrefs   map[string]bool
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

func (encoder HpackEncoder) shouldIndex(h HeaderField) bool {
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

	if TableCapacity(len(h.Name)+len(h.Value)+32) > encoder.Table.capacity {
		return false
	}
	pref, ok := encoder.indexPrefs[h.Name]
	if ok {
		return pref
	}
	_, d := dontIndex[h.Name]
	if d {
		return false
	}
	return true
}

func (encoder *HpackEncoder) writeIncremental(writer *Writer, h HeaderField, nameEntry Entry) error {
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
		err = writer.WriteStringRaw(h.Name, encoder.HuffmanPreference)
		if err != nil {
			return err
		}
	}

	err = writer.WriteStringRaw(h.Value, encoder.HuffmanPreference)
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

	nameIndex := uint64(0)
	if nameEntry != nil {
		nameIndex = uint64(nameEntry.Index())
	}
	err = writer.WriteInt(nameIndex, 4)
	if err != nil {
		return err
	}
	if nameEntry == nil {
		err = writer.WriteStringRaw(h.Name, encoder.HuffmanPreference)
		if err != nil {
			return err
		}
	}

	return writer.WriteStringRaw(h.Value, encoder.HuffmanPreference)
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

// SetIndexPreference sets preferences for header fields with the given name.
// Set to true to index, false to never index.
func (encoder *HpackEncoder) SetIndexPreference(name string, pref bool) {
	if encoder.indexPrefs == nil {
		encoder.indexPrefs = make(map[string]bool)
	}
	encoder.indexPrefs[name] = pref
}

// ClearIndexPreference resets the preference for indexing for the named header field.
func (encoder *HpackEncoder) ClearIndexPreference(name string) {
	delete(encoder.indexPrefs, name)
}
