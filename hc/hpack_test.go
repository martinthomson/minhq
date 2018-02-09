package hc_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/martinthomson/minhq/hc"
	"github.com/stvp/assert"
)

func resetEncoderCapacity(t *testing.T, encoder *hc.HpackEncoder, first bool) {
	encoder.SetCapacity(0)
	encoder.SetCapacity(256)
	var capacity bytes.Buffer
	err := encoder.WriteHeaderBlock(&capacity)
	assert.Nil(t, err)
	message := []byte{0x20, 0x3f, 0xe1, 0x01}
	if first {
		message = message[1:]
	}
	assert.Equal(t, message, capacity.Bytes())
}

func TestHpackEncoder(t *testing.T) {
	encoder := hc.NewHpackEncoder(0)
	resetEncoderCapacity(t, encoder, true)
	// The examples in RFC 7541 index date, which is of questionable utility.
	encoder.SetIndexPreference("date", true)

	for _, tc := range testCases {
		if tc.resetTable {
			resetEncoderCapacity(t, encoder, false)
		}
		if tc.huffman {
			encoder.HuffmanPreference = hc.HuffmanCodingAlways
		} else {
			encoder.HuffmanPreference = hc.HuffmanCodingNever
		}

		var buf bytes.Buffer
		err := encoder.WriteHeaderBlock(&buf, tc.headers...)
		assert.Nil(t, err)

		encoded, err := hex.DecodeString(tc.hpack)
		assert.Nil(t, err)
		assert.Equal(t, encoded, buf.Bytes())

		checkDynamicTable(t, encoder.Table, &tc.hpackTable)
	}
}

func TestHpackEncoderPseudoHeaderOrder(t *testing.T) {
	encoder := hc.NewHpackEncoder(0)
	var buf bytes.Buffer
	err := encoder.WriteHeaderBlock(&buf,
		hc.HeaderField{Name: "regular", Value: "1", Sensitive: false},
		hc.HeaderField{Name: ":pseudo", Value: "1", Sensitive: false})
	assert.Equal(t, hc.ErrPseudoHeaderOrdering, err)
}

func resetDecoderCapacity(t *testing.T, decoder *hc.HpackDecoder) {
	reader := bytes.NewReader([]byte{0x20, 0x3f, 0xe1, 0x01})
	h, err := decoder.ReadHeaderBlock(reader)
	assert.Nil(t, err)
	assert.Equal(t, 0, len(h))
}

func TestHpackDecoder(t *testing.T) {
	decoder := hc.NewHpackDecoder()
	// Avoid an extra reset.
	assert.True(t, testCases[0].resetTable)

	for _, tc := range testCases {
		if tc.resetTable {
			resetDecoderCapacity(t, decoder)
		}

		input, err := hex.DecodeString(tc.hpack)
		assert.Nil(t, err)
		h, err := decoder.ReadHeaderBlock(bytes.NewReader(input))
		assert.Nil(t, err)
		assert.Equal(t, tc.headers, h)

		checkDynamicTable(t, decoder.Table, &tc.hpackTable)
	}
}

func TestHpackDecoderPseudoHeaderOrder(t *testing.T) {
	decoder := hc.NewHpackDecoder()
	_, err := decoder.ReadHeaderBlock(bytes.NewReader([]byte{0x90, 0x81}))
	assert.Equal(t, hc.ErrPseudoHeaderOrdering, err)
}

func TestHpackEviction(t *testing.T) {
	headers := []hc.HeaderField{
		{Name: "one", Value: "1", Sensitive: false},
		{Name: "two", Value: "2", Sensitive: false},
	}
	dynamicTable := &tableState{
		size: 36,
		entries: []tableStateEntry{
			{"two", "2"},
		},
	}

	encoder := hc.NewHpackEncoder(0)
	encoder.SetCapacity(64)
	var buf bytes.Buffer
	err := encoder.WriteHeaderBlock(&buf, headers...)
	assert.Nil(t, err)
	checkDynamicTable(t, encoder.Table, dynamicTable)

	decoder := hc.NewHpackDecoder()
	h, err := decoder.ReadHeaderBlock(&buf)
	assert.Nil(t, err)
	assert.Equal(t, headers, h)
	checkDynamicTable(t, decoder.Table, dynamicTable)
}
