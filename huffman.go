package minhq

import (
	"bytes"
	"errors"
	"io"
)

// TODO: implement these as io.Reader/io.Writer

// HuffmanCompressor is a progressive compressor for Huffman-encoded data.
type HuffmanCompressor struct {
	writer    io.ByteWriter
	saved     byte
	savedBits byte
}

type simpleByteWriter struct {
	writer io.Writer
}

func (sbw simpleByteWriter) WriteByte(c byte) error {
	n, err := sbw.writer.Write([]byte{c})
	if err != nil {
		return err
	}
	if n == 0 {
		return io.ErrShortWrite
	}
	return nil
}

func makeByteWriter(writer io.Writer) io.ByteWriter {
	bw, ok := writer.(io.ByteWriter)
	if ok {
		return bw
	}
	return simpleByteWriter{writer}
}

// NewHuffmanCompressor wraps the underlying io.Writer.
func NewHuffmanCompressor(writer io.Writer) *HuffmanCompressor {
	return &HuffmanCompressor{makeByteWriter(writer), 0, 0}
}

// This writes out the next codepoint.  This fails if the Writer blocks.
func (compressor *HuffmanCompressor) addEntry(entry hpackEntry) error {
	b := entry.len + compressor.savedBits
	v := compressor.saved
	for b >= 8 {
		b -= 8
		v |= byte((entry.val >> b) & 0xff)
		err := compressor.writer.WriteByte(v)
		if err != nil {
			return err
		}
		v = 0
	}
	compressor.saved = v | byte(entry.val<<(8-b))
	compressor.savedBits = b
	return nil
}

// Add compresses a string using the Huffman table.  Strings are provided as byte slices.
func (compressor *HuffmanCompressor) Write(input []byte) (int, error) {
	for i, c := range input {
		err := compressor.addEntry(hpackTable[c])
		if err != nil {
			return i, err
		}
	}
	return len(input), nil
}

// Finalize adds a terminator value and returns the full compressed value.
func (compressor *HuffmanCompressor) Finalize() error {
	if compressor.savedBits > 0 {
		err := compressor.writer.WriteByte(compressor.saved | (0xff >> compressor.savedBits))
		if err != nil {
			return err
		}
	}
	return nil
}

// This is a node in the reverse mapping tree.  We use 4-bit chunks because those result in at most a single emission of a character.
type node struct {
	next [2]*node
	leaf bool
	val  byte
}

func makeLayer(prefix uint32, prefixLen byte) *node {
	layer := new(node)
	found := false
	for i, e := range hpackTable {
		if e.len < prefixLen+1 {
			continue
		}
		if (e.val >> (e.len - prefixLen)) != prefix {
			continue
		}
		arity := (e.val >> (e.len - prefixLen - 1)) & 1
		var child *node
		if e.len == prefixLen+1 {
			child = new(node)
			child.leaf = true
			child.val = byte(i)
			layer.next[arity] = child
			if layer.next[arity^1] != nil {
				return layer
			}
		}
		found = true
	}
	// There are unused parts of the tree, so leave the branches as nil if we reach those
	if found {
		if layer.next[0] == nil {
			layer.next[0] = makeLayer(prefix<<1, prefixLen+1)
		}
		if layer.next[1] == nil {
			layer.next[1] = makeLayer((prefix<<1)|1, prefixLen+1)
		}
	}
	return layer
}

var decompressorTree *node

func initDecompressorTree() {
	if decompressorTree == nil {
		decompressorTree = makeLayer(0, 0)
	}
}

// HuffmanDecompressor is the opposite of HuffmanCompressor
type HuffmanDecompressor struct {
	cursor *node
	buffer bytes.Buffer
}

// Add bytes of input
func (decompressor *HuffmanDecompressor) Add(input []byte) error {
	if decompressor.cursor == nil {
		initDecompressorTree()
		decompressor.cursor = decompressorTree
	}
	for _, v := range input {
		i := uint8(8)
		for i > 0 {
			i--
			decompressor.cursor = decompressor.cursor.next[(v>>i)&1]
			if decompressor.cursor == nil {
				decompressor.cursor = decompressorTree
				return errors.New("invalid Huffman coding")
			}
			if decompressor.cursor.leaf {
				err := decompressor.buffer.WriteByte(decompressor.cursor.val)
				if err != nil {
					return err
				}
				decompressor.cursor = decompressorTree
			}
		}
	}
	return nil
}

// Bytes retrieves a slice of the current compressed state.  This state contains only fully compressed values.
func (decompressor *HuffmanDecompressor) Bytes() []byte {
	return decompressor.buffer.Bytes()
}

// Finalize just calls Bytes.
func (decompressor *HuffmanDecompressor) Finalize() ([]byte, error) {
	decompressor.cursor = nil
	return decompressor.Bytes(), nil
}
