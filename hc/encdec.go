package hc

import "errors"

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

type decoderCommon struct {
	// Table is public to provide access to its methods.
	Table Table
}

func (decoder *decoderCommon) readNameValue(reader *Reader, prefix byte, base int) (string, string, error) {
	index, err := reader.ReadIndex(prefix)
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
		entry := decoder.Table.GetBase(index, base)
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

type encoderCommon struct {
	// Table is public to provide access to its methods.
	Table Table

	// HuffmanPreference records preferences for Huffman coding of strings.
	HuffmanPreference HuffmanCodingChoice

	// This stores preferences for indexing on a per-name basis.
	indexPrefs map[string]bool
}

func (encoder encoderCommon) shouldIndex(h HeaderField) bool {
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

// Write out a name/value pair to the specified writer, with the specified
// integer prefix size on the name index.
func (encoder encoderCommon) writeNameValue(writer *Writer, h HeaderField,
	nameEntry Entry, prefix byte, base int) error {
	nameIndex := uint64(0)
	if nameEntry != nil {
		nameIndex = uint64(nameEntry.Index(base))
	}
	err := writer.WriteInt(nameIndex, prefix)
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

// SetIndexPreference sets preferences for header fields with the given name.
// Set to true to index, false to never index.
func (encoder *encoderCommon) SetIndexPreference(name string, pref bool) {
	if encoder.indexPrefs == nil {
		encoder.indexPrefs = make(map[string]bool)
	}
	encoder.indexPrefs[name] = pref
}

// ClearIndexPreference resets the preference for indexing for the named header field.
func (encoder *encoderCommon) ClearIndexPreference(name string) {
	delete(encoder.indexPrefs, name)
}
