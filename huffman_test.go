package minhq_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/martinthomson/minhq"
	"github.com/stvp/assert"
)

var tests = []struct {
	text  string
	hpack string
}{
	{"www.example.com", "f1e3c2e5f23a6ba0ab90f4ff"},
	{"no-cache", "a8eb10649cbf"},
	{"custom-key", "25a849e95ba97d7f"},
	{"custom-value", "25a849e95bb8e8b4bf"},
	{"private", "aec3771a4b"},
	{"Mon, 21 Oct 2013 20:13:21 GMT", "d07abe941054d444a8200595040b8166e082a62d1bff"},
	{"https://www.example.com", "9d29ad171863c78f0b97c8e9ae82ae43d3"},
	{"Mon, 21 Oct 2013 20:13:22 GMT", "d07abe941054d444a8200595040b8166e084a62d1bff"},
	{"gzip", "9bd9ab"},
	{"foo=ASDJKHQKBZXOQWEOPIUAXQWEOIU; max-age=3600; version=1",
		"94e7821dd7f2e6c7b335dfdfcd5b3960d5af27087f3672c1ab270fb5291f9587" +
			"316065c003ed4ee5b1063d5007"},
}

func TestHuffmanCompress(t *testing.T) {
	for _, v := range tests {
		var buffer bytes.Buffer
		compressor := minhq.NewHuffmanCompressor(&buffer)

		n, err := compressor.Write([]byte(v.text))
		assert.Nil(t, err)
		assert.Equal(t, len(v.text), n)
		err = compressor.Finalize()
		assert.Nil(t, err)

		expected, err := hex.DecodeString(v.hpack)
		assert.Nil(t, err)

		assert.Equal(t, expected, buffer.Bytes())
	}
}

func TestHuffmanDecompress(t *testing.T) {
	for _, v := range tests {
		compressed, err := hex.DecodeString(v.hpack)
		assert.Nil(t, err)
		reader := bytes.NewReader(compressed)
		decompressor := minhq.NewHuffmanDecompressor(reader)

		decompressed := make([]byte, len(compressed)*2)
		n, _ := decompressor.Read(decompressed)
		assert.True(t, n < len(decompressed))

		assert.Equal(t, decompressed[0:n], []byte(v.text))
	}
}
