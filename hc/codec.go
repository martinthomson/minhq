package hc

import (
	"errors"
	"io"
	"io/ioutil"
	"log"
)

// ErrIndexError is a decoder error for the case where an invalid index is
// received.
var ErrIndexError = errors.New("decoder read an invalid index")

// ErrPseudoHeaderOrdering indicates that a pseudo header field was placed after
// a non-pseudo header field.
var ErrPseudoHeaderOrdering = errors.New("invalid pseudo header field order")

// HeaderField is the interface that header fields need to comply with.
type HeaderField struct {
	Name      string
	Value     string
	Sensitive bool
}

func (hf HeaderField) String() string {
	return hf.Name + ": " + hf.Value
}

func (hf HeaderField) size() TableCapacity {
	return tableOverhead + TableCapacity(len(hf.Name)+len(hf.Value))
}

// ValidatePseudoHeaders checks that pseudo-headers appear strictly before
// all other header fields.
func ValidatePseudoHeaders(headers []HeaderField) error {
	pseudo := true
	for _, h := range headers {
		if h.Name[0] == ':' {
			if !pseudo {
				return ErrPseudoHeaderOrdering
			}
		} else {
			pseudo = false
		}
	}
	return nil
}

type logged struct {
	logger *log.Logger
}

func (lg *logged) initLogging(w io.Writer) {
	if w == nil {
		w = ioutil.Discard
	}
	lg.logger = log.New(w, "", log.Lmicroseconds|log.Lshortfile)
}

func (lg *logged) SetLogger(logger *log.Logger) {
	lg.logger = logger
}

type decoderCommon struct {
	// Table is public to provide access to its methods.
	Table Table
	logged
}

type encoderCommon struct {
	// Table is public to provide access to its methods.
	Table Table
	logged

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

	if TableCapacity(len(h.Name)+len(h.Value)+32) > encoder.Table.Capacity() {
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

// SetIndexPreference sets preferences for header fields with the given name.
// Set to true to index, false to never index.
func (encoder *encoderCommon) SetIndexPreference(name string, pref bool) {
	encoder.logger.Printf("set indexing pref for %v to %v", name, pref)
	if encoder.indexPrefs == nil {
		encoder.indexPrefs = make(map[string]bool)
	}
	encoder.indexPrefs[name] = pref
}

// ClearIndexPreference resets the preference for indexing for the named header field.
func (encoder *encoderCommon) ClearIndexPreference(name string) {
	encoder.logger.Printf("clear indexing pref for %v", name)
	delete(encoder.indexPrefs, name)
}
