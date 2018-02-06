package hc_test

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/martinthomson/minhq/hc"
	"github.com/stvp/assert"
)

type dynamicTableEntry struct {
	name  string
	value string
}

var testCases = []struct {
	resetTable   bool
	headers      []hc.HeaderField
	huffman      bool
	encoded      string
	tableSize    hc.TableCapacity
	dynamicTable []dynamicTableEntry
}{
	{
		resetTable: true,
		headers: []hc.HeaderField{
			{Name: "custom-key", Value: "custom-header", Sensitive: false},
		},
		huffman:   false,
		encoded:   "400a637573746f6d2d6b65790d637573746f6d2d686561646572",
		tableSize: 55,
		dynamicTable: []dynamicTableEntry{
			{"custom-key", "custom-header"},
		},
	},
	{
		resetTable: true,
		headers: []hc.HeaderField{
			{Name: ":path", Value: "/sample/path", Sensitive: false},
		},
		huffman:      false,
		encoded:      "040c2f73616d706c652f70617468",
		tableSize:    0,
		dynamicTable: []dynamicTableEntry{},
	},
	{
		resetTable: true,
		headers: []hc.HeaderField{
			{Name: "password", Value: "secret", Sensitive: true},
		},
		huffman:      false,
		encoded:      "100870617373776f726406736563726574",
		tableSize:    0,
		dynamicTable: []dynamicTableEntry{},
	},
	{
		resetTable: true,
		headers: []hc.HeaderField{
			{Name: ":method", Value: "GET", Sensitive: false},
		},
		huffman:      false,
		encoded:      "82",
		tableSize:    0,
		dynamicTable: []dynamicTableEntry{},
	},
	{
		resetTable: true,
		headers: []hc.HeaderField{
			{Name: ":method", Value: "GET", Sensitive: false},
			{Name: ":scheme", Value: "http", Sensitive: false},
			{Name: ":path", Value: "/", Sensitive: false},
			{Name: ":authority", Value: "www.example.com", Sensitive: false},
		},
		huffman:   false,
		encoded:   "828684410f7777772e6578616d706c652e636f6d",
		tableSize: 57,
		dynamicTable: []dynamicTableEntry{
			{":authority", "www.example.com"},
		},
	},
	{
		resetTable: false,
		headers: []hc.HeaderField{
			{Name: ":method", Value: "GET", Sensitive: false},
			{Name: ":scheme", Value: "http", Sensitive: false},
			{Name: ":path", Value: "/", Sensitive: false},
			{Name: ":authority", Value: "www.example.com", Sensitive: false},
			{Name: "cache-control", Value: "no-cache", Sensitive: false},
		},
		huffman:   false,
		encoded:   "828684be58086e6f2d6361636865",
		tableSize: 110,
		dynamicTable: []dynamicTableEntry{
			{"cache-control", "no-cache"},
			{":authority", "www.example.com"},
		},
	},
	{
		resetTable: false,
		headers: []hc.HeaderField{
			{Name: ":method", Value: "GET", Sensitive: false},
			{Name: ":scheme", Value: "https", Sensitive: false},
			{Name: ":path", Value: "/index.html", Sensitive: false},
			{Name: ":authority", Value: "www.example.com", Sensitive: false},
			{Name: "custom-key", Value: "custom-value", Sensitive: false},
		},
		huffman:   false,
		encoded:   "828785bf400a637573746f6d2d6b65790c637573746f6d2d76616c7565",
		tableSize: 164,
		dynamicTable: []dynamicTableEntry{
			{"custom-key", "custom-value"},
			{"cache-control", "no-cache"},
			{":authority", "www.example.com"},
		},
	},
	{
		resetTable: true,
		headers: []hc.HeaderField{
			{Name: ":method", Value: "GET", Sensitive: false},
			{Name: ":scheme", Value: "http", Sensitive: false},
			{Name: ":path", Value: "/", Sensitive: false},
			{Name: ":authority", Value: "www.example.com", Sensitive: false},
		},
		huffman:   true,
		encoded:   "828684418cf1e3c2e5f23a6ba0ab90f4ff",
		tableSize: 57,
		dynamicTable: []dynamicTableEntry{
			{":authority", "www.example.com"},
		},
	},
	{
		resetTable: false,
		headers: []hc.HeaderField{
			{Name: ":method", Value: "GET", Sensitive: false},
			{Name: ":scheme", Value: "http", Sensitive: false},
			{Name: ":path", Value: "/", Sensitive: false},
			{Name: ":authority", Value: "www.example.com", Sensitive: false},
			{Name: "cache-control", Value: "no-cache", Sensitive: false},
		},
		huffman:   true,
		encoded:   "828684be5886a8eb10649cbf",
		tableSize: 110,
		dynamicTable: []dynamicTableEntry{
			{"cache-control", "no-cache"},
			{":authority", "www.example.com"},
		},
	},
	{
		resetTable: false,
		headers: []hc.HeaderField{
			{Name: ":method", Value: "GET", Sensitive: false},
			{Name: ":scheme", Value: "https", Sensitive: false},
			{Name: ":path", Value: "/index.html", Sensitive: false},
			{Name: ":authority", Value: "www.example.com", Sensitive: false},
			{Name: "custom-key", Value: "custom-value", Sensitive: false},
		},
		huffman:   true,
		encoded:   "828785bf408825a849e95ba97d7f8925a849e95bb8e8b4bf",
		tableSize: 164,
		dynamicTable: []dynamicTableEntry{
			{"custom-key", "custom-value"},
			{"cache-control", "no-cache"},
			{":authority", "www.example.com"},
		},
	},
	{
		resetTable: true,
		headers: []hc.HeaderField{
			{Name: ":status", Value: "302", Sensitive: false},
			{Name: "cache-control", Value: "private", Sensitive: false},
			{Name: "date", Value: "Mon, 21 Oct 2013 20:13:21 GMT", Sensitive: false},
			{Name: "location", Value: "https://www.example.com", Sensitive: false},
		},
		huffman: false,
		encoded: "4803333032580770726976617465611d4d6f6e2c203231204f63742032303133" +
			"2032303a31333a323120474d546e1768747470733a2f2f7777772e6578616d70" +
			"6c652e636f6d",
		tableSize: 222,
		dynamicTable: []dynamicTableEntry{
			{"location", "https://www.example.com"},
			{"date", "Mon, 21 Oct 2013 20:13:21 GMT"},
			{"cache-control", "private"},
			{":status", "302"},
		},
	},
	{
		resetTable: false,
		headers: []hc.HeaderField{
			{Name: ":status", Value: "307", Sensitive: false},
			{Name: "cache-control", Value: "private", Sensitive: false},
			{Name: "date", Value: "Mon, 21 Oct 2013 20:13:21 GMT", Sensitive: false},
			{Name: "location", Value: "https://www.example.com", Sensitive: false},
		},
		huffman:   false,
		encoded:   "4803333037c1c0bf",
		tableSize: 222,
		dynamicTable: []dynamicTableEntry{
			{":status", "307"},
			{"location", "https://www.example.com"},
			{"date", "Mon, 21 Oct 2013 20:13:21 GMT"},
			{"cache-control", "private"},
		},
	},
	{
		resetTable: false,
		headers: []hc.HeaderField{
			{Name: ":status", Value: "200", Sensitive: false},
			{Name: "cache-control", Value: "private", Sensitive: false},
			{Name: "date", Value: "Mon, 21 Oct 2013 20:13:22 GMT", Sensitive: false},
			{Name: "location", Value: "https://www.example.com", Sensitive: false},
			{Name: "content-encoding", Value: "gzip", Sensitive: false},
			{Name: "set-cookie", Value: "foo=ASDJKHQKBZXOQWEOPIUAXQWEOIU; max-age=3600; version=1", Sensitive: false},
		},
		huffman: false,
		encoded: "88c1611d4d6f6e2c203231204f637420323031332032303a31333a323220474d" +
			"54c05a04677a69707738666f6f3d4153444a4b48514b425a584f5157454f5049" +
			"5541585157454f49553b206d61782d6167653d333630303b2076657273696f6e" +
			"3d31",
		tableSize: 215,
		dynamicTable: []dynamicTableEntry{
			{"set-cookie", "foo=ASDJKHQKBZXOQWEOPIUAXQWEOIU; max-age=3600; version=1"},
			{"content-encoding", "gzip"},
			{"date", "Mon, 21 Oct 2013 20:13:22 GMT"},
		},
	},
	{
		resetTable: true,
		headers: []hc.HeaderField{
			{Name: ":status", Value: "302", Sensitive: false},
			{Name: "cache-control", Value: "private", Sensitive: false},
			{Name: "date", Value: "Mon, 21 Oct 2013 20:13:21 GMT", Sensitive: false},
			{Name: "location", Value: "https://www.example.com", Sensitive: false},
		},
		huffman: true,
		encoded: "488264025885aec3771a4b6196d07abe941054d444a8200595040b8166e082a6" +
			"2d1bff6e919d29ad171863c78f0b97c8e9ae82ae43d3",
		tableSize: 222,
		dynamicTable: []dynamicTableEntry{
			{"location", "https://www.example.com"},
			{"date", "Mon, 21 Oct 2013 20:13:21 GMT"},
			{"cache-control", "private"},
			{":status", "302"},
		},
	},
	{
		resetTable: false,
		headers: []hc.HeaderField{
			{Name: ":status", Value: "307", Sensitive: false},
			{Name: "cache-control", Value: "private", Sensitive: false},
			{Name: "date", Value: "Mon, 21 Oct 2013 20:13:21 GMT", Sensitive: false},
			{Name: "location", Value: "https://www.example.com", Sensitive: false},
		},
		huffman:   true,
		encoded:   "4883640effc1c0bf",
		tableSize: 222,
		dynamicTable: []dynamicTableEntry{
			{":status", "307"},
			{"location", "https://www.example.com"},
			{"date", "Mon, 21 Oct 2013 20:13:21 GMT"},
			{"cache-control", "private"},
		},
	},
	{
		resetTable: false,
		headers: []hc.HeaderField{
			{Name: ":status", Value: "200", Sensitive: false},
			{Name: "cache-control", Value: "private", Sensitive: false},
			{Name: "date", Value: "Mon, 21 Oct 2013 20:13:22 GMT", Sensitive: false},
			{Name: "location", Value: "https://www.example.com", Sensitive: false},
			{Name: "content-encoding", Value: "gzip", Sensitive: false},
			{Name: "set-cookie", Value: "foo=ASDJKHQKBZXOQWEOPIUAXQWEOIU; max-age=3600; version=1", Sensitive: false},
		},
		huffman: true,
		encoded: "88c16196d07abe941054d444a8200595040b8166e084a62d1bffc05a839bd9ab" +
			"77ad94e7821dd7f2e6c7b335dfdfcd5b3960d5af27087f3672c1ab270fb5291f" +
			"9587316065c003ed4ee5b1063d5007",
		tableSize: 215,
		dynamicTable: []dynamicTableEntry{
			{"set-cookie", "foo=ASDJKHQKBZXOQWEOPIUAXQWEOIU; max-age=3600; version=1"},
			{"content-encoding", "gzip"},
			{"date", "Mon, 21 Oct 2013 20:13:22 GMT"},
		},
	},
}

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

func checkDynamicTable(t *testing.T, table *hc.Table, entries []dynamicTableEntry) {
	for i, e := range entries {
		// The initial offset for dynamic entries is 62 in HPACK.
		entry := table.Get(i + 62)
		assert.NotNil(t, entry)
		assert.Equal(t, e.name, entry.Name())
		assert.Equal(t, e.value, entry.Value())
	}
}

func TestHpackEncoder(t *testing.T) {
	var encoder hc.HpackEncoder
	resetEncoderCapacity(t, &encoder, true)
	// The examples in RFC 7541 index date, which is of questionable utility.
	encoder.SetIndexPreference("date", true)

	for _, tc := range testCases {
		if tc.resetTable {
			resetEncoderCapacity(t, &encoder, false)
		}
		if tc.huffman {
			encoder.HuffmanPreference = hc.HuffmanCodingAlways
		} else {
			encoder.HuffmanPreference = hc.HuffmanCodingNever
		}

		var buf bytes.Buffer
		err := encoder.WriteHeaderBlock(&buf, tc.headers...)
		assert.Nil(t, err)

		encoded, err := hex.DecodeString(tc.encoded)
		assert.Nil(t, err)
		fmt.Printf("expected: %v\n", tc.encoded)
		fmt.Printf("encoded:  %v\n", hex.EncodeToString(buf.Bytes()))
		assert.Equal(t, encoded, buf.Bytes())

		assert.Equal(t, tc.tableSize, encoder.Table.Used())
		checkDynamicTable(t, &encoder.Table, tc.dynamicTable)
	}
}

func TestHpackEncoderPseudoHeaderOrder(t *testing.T) {
	var encoder hc.HpackEncoder
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
	var decoder hc.HpackDecoder
	// Avoid an extra reset.
	assert.True(t, testCases[0].resetTable)

	for _, tc := range testCases {
		if tc.resetTable {
			resetDecoderCapacity(t, &decoder)
		}

		input, err := hex.DecodeString(tc.encoded)
		assert.Nil(t, err)
		h, err := decoder.ReadHeaderBlock(bytes.NewReader(input))
		assert.Nil(t, err)
		assert.Equal(t, tc.headers, h)

		assert.Equal(t, tc.tableSize, decoder.Table.Used())
		checkDynamicTable(t, &decoder.Table, tc.dynamicTable)
	}
}

func TestHpackDecoderPseudoHeaderOrder(t *testing.T) {
	var decoder hc.HpackDecoder
	_, err := decoder.ReadHeaderBlock(bytes.NewReader([]byte{0x90, 0x81}))
	assert.Equal(t, hc.ErrPseudoHeaderOrdering, err)
}

func TestHpackEviction(t *testing.T) {
	headers := []hc.HeaderField{
		{Name: "one", Value: "1", Sensitive: false},
		{Name: "two", Value: "2", Sensitive: false},
	}
	dynamicTable := []dynamicTableEntry{
		{"two", "2"},
	}

	var encoder hc.HpackEncoder
	encoder.SetCapacity(64)
	var buf bytes.Buffer
	err := encoder.WriteHeaderBlock(&buf, headers...)
	assert.Nil(t, err)
	checkDynamicTable(t, &encoder.Table, dynamicTable)

	var decoder hc.HpackDecoder
	h, err := decoder.ReadHeaderBlock(&buf)
	assert.Nil(t, err)
	assert.Equal(t, headers, h)
	checkDynamicTable(t, &decoder.Table, dynamicTable)
}
