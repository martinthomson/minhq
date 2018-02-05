package minhq_test

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/martinthomson/minhq"
	"github.com/stvp/assert"
)

var encodedIntegers = []struct {
	value   uint64
	encoded string
	prefix  byte
}{
	{10, "0a", 8},
	{256, "ff01", 8},
	{1, "0100", 1},
	{^uint64(0), "ff80feffffffffffffff01", 8},
	{^uint64(0), "01feffffffffffffffff01", 1},
	{1 << 63, "ff81feffffffffffff7f", 8},
	{1 << 63, "01ffffffffffffffff7f", 1},
}

func TestReadIntegers(t *testing.T) {
	for _, tc := range encodedIntegers {
		encoded, err := hex.DecodeString(tc.encoded)
		assert.Nil(t, err)
		reader := minhq.NewHpackReader(bytes.NewReader(encoded))
		if tc.prefix < 8 {
			b, err := reader.ReadBits(8 - tc.prefix)
			assert.Nil(t, err)
			assert.Equal(t, uint64(0), b)
		}
		i, err := reader.ReadInt(tc.prefix)
		assert.Nil(t, err)
		assert.Equal(t, tc.value, i)
	}
}

func TestWriteIntegers(t *testing.T) {
	for _, tc := range encodedIntegers {
		var encoded bytes.Buffer
		writer := minhq.NewHpackWriter(&encoded)
		if tc.prefix < 8 {
			err := writer.WriteBits(uint64(0), 8-tc.prefix)
			assert.Nil(t, err)
		}
		err := writer.WriteInt(tc.value, tc.prefix)
		assert.Nil(t, err)
		expected, err := hex.DecodeString(tc.encoded)
		assert.Nil(t, err)
		assert.Equal(t, expected, encoded.Bytes())
	}
}

func TestIntegerOverflow(t *testing.T) {
	overflowingIntegers := []string{
		// ^uint64(0) + 1
		"ff80ffffffffffffffff01",
		// Too long an encoding (even though the value is a mere 255)
		"ff80808080808080808080",
	}
	for _, tc := range overflowingIntegers {
		encoded, err := hex.DecodeString(tc)
		assert.Nil(t, err)
		reader := minhq.NewHpackReader(bytes.NewReader(encoded))
		_, err = reader.ReadInt(8)
		assert.Equal(t, minhq.ErrIntegerOverflow, err)
	}
}

var encodedStrings = []struct {
	value   string
	encoded string
}{
	{"Hello, World!", "0d48656c6c6f2c20576f726c6421"},
	{"Hello, World!", "8bc65a283fd29c8f65127f1f"},
	{"no-cache", "086e6f2d6361636865"},
	{"no-cache", "86a8eb10649cbf"},
	{"www.example.com", "0f7777772e6578616d706c652e636f6d"},
	{"www.example.com", "8cf1e3c2e5f23a6ba0ab90f4ff"},
}

func TestReadString(t *testing.T) {
	for _, tc := range encodedStrings {
		fmt.Printf("decode %s -> '%s'\n", tc.encoded, tc.value)
		encoded, err := hex.DecodeString(tc.encoded)
		assert.Nil(t, err)
		reader := minhq.NewHpackReader(bytes.NewReader(encoded))
		s, err := reader.ReadString()
		assert.Nil(t, err)
		fmt.Printf(" = '%s'\n", s)
		assert.Equal(t, tc.value, s)
	}
}

func TestWriteString(t *testing.T) {
	for _, tc := range encodedStrings {
		fmt.Printf("encode '%s' -> %s\n", tc.value, tc.encoded)
		expected, err := hex.DecodeString(tc.encoded)
		assert.Nil(t, err)
		var huffman minhq.HuffmanCodingChoice
		if (expected[0] & 0x80) == 0 {
			huffman = minhq.HuffmanCodingNever
		} else {
			huffman = minhq.HuffmanCodingAlways
		}

		var encoded bytes.Buffer
		writer := minhq.NewHpackWriter(&encoded)
		err = writer.WriteStringRaw(tc.value, huffman)
		assert.Nil(t, err)
		fmt.Printf(" = %s\n", hex.EncodeToString(encoded.Bytes()))
		assert.Equal(t, expected, encoded.Bytes())
	}
}
