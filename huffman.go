package minhq

import (
	"errors"
	"io"
)

// HuffmanCompressor is a progressive compressor for Huffman-encoded data.
type HuffmanCompressor struct {
	writer    BitWriter
	saved     byte
	savedBits byte
}

// NewHuffmanCompressor wraps the underlying io.Writer.
func NewHuffmanCompressor(writer io.Writer) *HuffmanCompressor {
	return &HuffmanCompressor{NewBitWriter(writer), 0, 0}
}

// Add compresses a string using the Huffman table.  Strings are provided as byte slices.
func (compressor *HuffmanCompressor) Write(input []byte) (int, error) {
	for i, c := range input {
		entry := hpackTable[c]
		err := compressor.writer.WriteBits(uint64(entry.val), entry.len)
		if err != nil {
			return i, err
		}
	}
	return len(input), nil
}

// Finalize adds a terminator value and returns the full compressed value.
func (compressor *HuffmanCompressor) Finalize() error {
	return compressor.writer.Finalize(0xff)
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

// HuffmanDecompressor is the opposite of huffmanCompressor
type HuffmanDecompressor struct {
	reader io.ByteReader
	cursor *node

	// Any overflow from decompression (see Read).  We only ever need to save
	// one octet because one octet of input expands to at most 2 octets.
	overflow byte
}

// 0 is a safe sentinel to use for overflow because it encodes to 13 bits.
const invalidOverflow byte = 0

// NewHuffmanDecompressor makes a new decompressor, which implements io.Reader.
func NewHuffmanDecompressor(reader io.Reader) *HuffmanDecompressor {
	initDecompressorTree()
	return &HuffmanDecompressor{makeByteReader(reader), decompressorTree, invalidOverflow}
}

// Add bytes of input
func (decompressor *HuffmanDecompressor) Read(p []byte) (int, error) {
	i := 0
	if decompressor.overflow != invalidOverflow {
		p[i] = decompressor.overflow
		i++
		decompressor.overflow = invalidOverflow
	}
	for i < len(p) {
		v, err := decompressor.reader.ReadByte()
		if err != nil {
			return i, err
		}

		j := byte(8)
		for j > 0 {
			j--
			decompressor.cursor = decompressor.cursor.next[(v>>j)&1]
			if decompressor.cursor == nil {
				return i, errors.New("invalid Huffman coding")
			}
			if decompressor.cursor.leaf {
				// HPACK can produce two octets of output from a single octet of input,
				// if there isn't enough room to return that octet, save it in overflow.
				if i >= len(p) {
					decompressor.overflow = decompressor.cursor.val
				} else {
					p[i] = decompressor.cursor.val
					i++
				}
				decompressor.cursor = decompressorTree
			}
		}
	}
	return i, nil
}
