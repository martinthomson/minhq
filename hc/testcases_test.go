package hc_test

import (
	"testing"

	"github.com/martinthomson/minhq/hc"
	"github.com/stvp/assert"
)

type dynamicTableEntry struct {
	name  string
	value string
}

func checkDynamicTable(t *testing.T, table hc.Table, ts *[]dynamicTableEntry) {
	var size hc.TableCapacity
	for i, e := range *ts {
		entry := table.GetDynamic(i, table.Base())
		t.Logf("Dynamic entry: %v", entry)
		assert.NotNil(t, entry)
		assert.Equal(t, e.name, entry.Name())
		assert.Equal(t, e.value, entry.Value())
		size += hc.TableCapacity(32 + len(e.name) + len(e.value))
	}
	assert.Equal(t, size, table.Used())
}

type testCase struct {
	resetTable   bool
	headers      []hc.HeaderField
	huffman      bool
	hpack        string
	hpackTable   []dynamicTableEntry
	qpackUpdates string
	qpackHeader  string
	qpackTable   *[]dynamicTableEntry
}

var testCases = []testCase{
	{
		resetTable: true,
		headers: []hc.HeaderField{
			{Name: "custom-key", Value: "custom-header"},
		},
		huffman: false,
		hpack:   "400a637573746f6d2d6b65790d637573746f6d2d686561646572",
		hpackTable: []dynamicTableEntry{
			{"custom-key", "custom-header"},
		},
		qpackUpdates: "4a637573746f6d2d6b65790d637573746f6d2d686561646572",
		qpackHeader:  "010080",
	},
	{
		resetTable: true,
		headers: []hc.HeaderField{
			{Name: ":path", Value: "/sample/path"},
		},
		huffman:      false,
		hpack:        "040c2f73616d706c652f70617468",
		hpackTable:   nil,
		qpackUpdates: "",
		qpackHeader:  "0000500c2f73616d706c652f70617468",
	},
	{
		resetTable: true,
		headers: []hc.HeaderField{
			{Name: "password", Value: "secret", Sensitive: true},
		},
		huffman:      false,
		hpack:        "100870617373776f726406736563726574",
		hpackTable:   nil,
		qpackUpdates: "",
		qpackHeader:  "0000370170617373776f726406736563726574",
	},
	{
		resetTable: true,
		headers: []hc.HeaderField{
			{Name: ":method", Value: "GET"},
		},
		huffman:      false,
		hpack:        "82",
		hpackTable:   nil,
		qpackUpdates: "",
		qpackHeader:  "0000d8",
	},
	{
		resetTable: true,
		headers: []hc.HeaderField{
			{Name: ":method", Value: "GET"},
			{Name: ":scheme", Value: "http"},
			{Name: ":path", Value: "/"},
			{Name: ":authority", Value: "www.example.com"},
		},
		huffman: false,
		hpack:   "828684410f7777772e6578616d706c652e636f6d",
		hpackTable: []dynamicTableEntry{
			{":authority", "www.example.com"},
		},
		qpackUpdates: "c50f7777772e6578616d706c652e636f6d",
		qpackHeader:  "0100d8d4c080",
	},
	{
		resetTable: false,
		headers: []hc.HeaderField{
			{Name: ":method", Value: "GET"},
			{Name: ":scheme", Value: "http"},
			{Name: ":path", Value: "/"},
			{Name: ":authority", Value: "www.example.com"},
			{Name: "cache-control", Value: "no-cache"},
		},
		huffman: false,
		hpack:   "828684be58086e6f2d6361636865",
		hpackTable: []dynamicTableEntry{
			{"cache-control", "no-cache"},
			{":authority", "www.example.com"},
		},
		qpackUpdates: "",
		qpackHeader:  "0100d8d4c080f2",
		qpackTable: &[]dynamicTableEntry{
			{":authority", "www.example.com"},
		},
	},
	{
		resetTable: false,
		headers: []hc.HeaderField{
			{Name: ":method", Value: "GET"},
			{Name: ":scheme", Value: "https"},
			{Name: ":path", Value: "/index.html"},
			{Name: ":authority", Value: "www.example.com"},
			{Name: "custom-key", Value: "custom-value"},
		},
		huffman: false,
		hpack:   "828785bf400a637573746f6d2d6b65790c637573746f6d2d76616c7565",
		hpackTable: []dynamicTableEntry{
			{"custom-key", "custom-value"},
			{"cache-control", "no-cache"},
			{":authority", "www.example.com"},
		},
		qpackUpdates: "4a637573746f6d2d6b65790c637573746f6d2d76616c7565",
		qpackHeader:  "0200d8d3d78180",
		qpackTable: &[]dynamicTableEntry{
			{"custom-key", "custom-value"},
			{":authority", "www.example.com"},
		},
	},
	{
		resetTable: true,
		headers: []hc.HeaderField{
			{Name: ":method", Value: "GET"},
			{Name: ":scheme", Value: "http"},
			{Name: ":path", Value: "/"},
			{Name: ":authority", Value: "www.example.com"},
		},
		huffman: true,
		hpack:   "828684418cf1e3c2e5f23a6ba0ab90f4ff",
		hpackTable: []dynamicTableEntry{
			{":authority", "www.example.com"},
		},
		qpackUpdates: "c58cf1e3c2e5f23a6ba0ab90f4ff",
		qpackHeader:  "0100d8d4c080",
	},
	{
		resetTable: false,
		headers: []hc.HeaderField{
			{Name: ":method", Value: "GET"},
			{Name: ":scheme", Value: "http"},
			{Name: ":path", Value: "/"},
			{Name: ":authority", Value: "www.example.com"},
			{Name: "cache-control", Value: "no-cache"},
		},
		huffman: true,
		hpack:   "828684be5886a8eb10649cbf",
		hpackTable: []dynamicTableEntry{
			{"cache-control", "no-cache"},
			{":authority", "www.example.com"},
		},
		qpackUpdates: "",
		qpackHeader:  "0100d8d4c080f2",
		qpackTable: &[]dynamicTableEntry{
			{":authority", "www.example.com"},
		},
	},
	{
		resetTable: false,
		headers: []hc.HeaderField{
			{Name: ":method", Value: "GET"},
			{Name: ":scheme", Value: "https"},
			{Name: ":path", Value: "/index.html"},
			{Name: ":authority", Value: "www.example.com"},
			{Name: "custom-key", Value: "custom-value"},
		},
		huffman: true,
		hpack:   "828785bf408825a849e95ba97d7f8925a849e95bb8e8b4bf",
		hpackTable: []dynamicTableEntry{
			{"custom-key", "custom-value"},
			{"cache-control", "no-cache"},
			{":authority", "www.example.com"},
		},
		qpackUpdates: "6825a849e95ba97d7f8925a849e95bb8e8b4bf",
		qpackHeader:  "0200d8d3d78180",
		qpackTable: &[]dynamicTableEntry{
			{"custom-key", "custom-value"},
			{":authority", "www.example.com"},
		},
	},
	{
		resetTable: true,
		headers: []hc.HeaderField{
			{Name: ":status", Value: "302"},
			{Name: "cache-control", Value: "private"},
			{Name: "date", Value: "Mon, 21 Oct 2013 20:13:21 GMT"},
			{Name: "location", Value: "https://www.example.com"},
		},
		huffman: false,
		hpack: "4803333032580770726976617465611d4d6f6e2c203231204f63742032303133" +
			"2032303a31333a323120474d546e1768747470733a2f2f7777772e6578616d70" +
			"6c652e636f6d",
		hpackTable: []dynamicTableEntry{
			{"location", "https://www.example.com"},
			{"date", "Mon, 21 Oct 2013 20:13:21 GMT"},
			{"cache-control", "private"},
			{":status", "302"},
		},
		qpackUpdates: "f10770726976617465c31d4d6f6e2c203231204f6374203230313320" +
			"32303a31333a323120474d54c91768747470733a2f2f7777772e6578616d706c652e636f6d",
		qpackHeader: "0300ff07828180",
		qpackTable: &[]dynamicTableEntry{
			{"location", "https://www.example.com"},
			{"date", "Mon, 21 Oct 2013 20:13:21 GMT"},
			{"cache-control", "private"},
		},
	},
	{
		resetTable: false,
		headers: []hc.HeaderField{
			{Name: ":status", Value: "307"},
			{Name: "cache-control", Value: "private"},
			{Name: "date", Value: "Mon, 21 Oct 2013 20:13:21 GMT"},
			{Name: "location", Value: "https://www.example.com"},
		},
		huffman: false,
		hpack:   "4803333037c1c0bf",
		hpackTable: []dynamicTableEntry{
			{":status", "307"},
			{"location", "https://www.example.com"},
			{"date", "Mon, 21 Oct 2013 20:13:21 GMT"},
			{"cache-control", "private"},
		},
		qpackUpdates: "d503333037",
		qpackHeader:  "040080838281",
	},
	{
		resetTable: false,
		headers: []hc.HeaderField{
			{Name: ":status", Value: "200"},
			{Name: "cache-control", Value: "private"},
			{Name: "date", Value: "Mon, 21 Oct 2013 20:13:22 GMT"},
			{Name: "location", Value: "https://www.example.com"},
			{Name: "content-encoding", Value: "gzip"},
			{Name: "set-cookie",
				Value:     "foo=ASDJKHQKBZXOQWEOPIUAXQWEOIU; max-age=3600; version=1",
				Sensitive: false},
		},
		huffman: false,
		hpack: "88c1611d4d6f6e2c203231204f637420323031332032303a31333a323220474d" +
			"54c05a04677a69707738666f6f3d4153444a4b48514b425a584f5157454f5049" +
			"5541585157454f49553b206d61782d6167653d333630303b2076657273696f6e" +
			"3d31",
		hpackTable: []dynamicTableEntry{
			{"set-cookie", "foo=ASDJKHQKBZXOQWEOPIUAXQWEOIU; max-age=3600; version=1"},
			{"content-encoding", "gzip"},
			{"date", "Mon, 21 Oct 2013 20:13:22 GMT"},
		},
		qpackUpdates: "",
		qpackHeader: "0300d582531d4d6f6e2c203231204f637420323031332032303a31333a" +
			"323220474d5480ff0f5d38666f6f3d4153444a4b48514b425a584f5157454f50495" +
			"541585157454f49553b206d61782d6167653d333630303b2076657273696f6e3d31",
		qpackTable: &[]dynamicTableEntry{
			{":status", "307"},
			{"location", "https://www.example.com"},
			{"date", "Mon, 21 Oct 2013 20:13:21 GMT"},
			{"cache-control", "private"},
		},
	},
	{
		resetTable: true,
		headers: []hc.HeaderField{
			{Name: ":status", Value: "302"},
			{Name: "cache-control", Value: "private"},
			{Name: "date", Value: "Mon, 21 Oct 2013 20:13:21 GMT"},
			{Name: "location", Value: "https://www.example.com"},
		},
		huffman: true,
		hpack: "488264025885aec3771a4b6196d07abe941054d444a8200595040b8166e082a6" +
			"2d1bff6e919d29ad171863c78f0b97c8e9ae82ae43d3",
		hpackTable: []dynamicTableEntry{
			{"location", "https://www.example.com"},
			{"date", "Mon, 21 Oct 2013 20:13:21 GMT"},
			{"cache-control", "private"},
			{":status", "302"},
		},
		qpackUpdates: "f185aec3771a4bc396d07abe941054d444a8200595040b8166e082a6" +
			"2d1bffc9919d29ad171863c78f0b97c8e9ae82ae43d3",
		qpackHeader: "0300ff07828180",
		qpackTable: &[]dynamicTableEntry{
			{"location", "https://www.example.com"},
			{"date", "Mon, 21 Oct 2013 20:13:21 GMT"},
			{"cache-control", "private"},
		},
	},
	{
		resetTable: false,
		headers: []hc.HeaderField{
			{Name: ":status", Value: "307"},
			{Name: "cache-control", Value: "private"},
			{Name: "date", Value: "Mon, 21 Oct 2013 20:13:21 GMT"},
			{Name: "location", Value: "https://www.example.com"},
		},
		huffman: true,
		hpack:   "4883640effc1c0bf",
		hpackTable: []dynamicTableEntry{
			{":status", "307"},
			{"location", "https://www.example.com"},
			{"date", "Mon, 21 Oct 2013 20:13:21 GMT"},
			{"cache-control", "private"},
		},
		qpackUpdates: "d583640eff",
		qpackHeader:  "040080838281",
	},
	{
		resetTable: false,
		headers: []hc.HeaderField{
			{Name: ":status", Value: "200"},
			{Name: "cache-control", Value: "private"},
			{Name: "date", Value: "Mon, 21 Oct 2013 20:13:22 GMT"},
			{Name: "location", Value: "https://www.example.com"},
			{Name: "content-encoding", Value: "gzip"},
			{Name: "set-cookie",
				Value:     "foo=ASDJKHQKBZXOQWEOPIUAXQWEOIU; max-age=3600; version=1",
				Sensitive: false},
		},
		huffman: true,
		hpack: "88c16196d07abe941054d444a8200595040b8166e084a62d1bffc05a839bd9ab" +
			"77ad94e7821dd7f2e6c7b335dfdfcd5b3960d5af27087f3672c1ab270fb5291f" +
			"9587316065c003ed4ee5b1063d5007",
		hpackTable: []dynamicTableEntry{
			{"set-cookie", "foo=ASDJKHQKBZXOQWEOPIUAXQWEOIU; max-age=3600; version=1"},
			{"content-encoding", "gzip"},
			{"date", "Mon, 21 Oct 2013 20:13:22 GMT"},
		},
		qpackUpdates: "",
		qpackHeader: "0300" + "d5" + "82" + "5396d07abe941054d444a8200595040b8166e084a62d1bff" +
			"80" + "ff0f" + "5dad94e7821dd7f2e6c7b335dfdfcd5b3960d5af27087f3672c1" + "ab270fb5291f9587316065c003ed4ee5b1063d5007",
		qpackTable: &[]dynamicTableEntry{
			{":status", "307"},
			{"location", "https://www.example.com"},
			{"date", "Mon, 21 Oct 2013 20:13:21 GMT"},
			{"cache-control", "private"},
		},
	},
	// Using existing values in the dynamic table revealed a bug in QPACK.
	{
		resetTable: true,
		headers: []hc.HeaderField{
			{Name: ":status", Value: "200"},
			{Name: "date", Value: "Mon, 21 Oct 2013 20:13:22 GMT"},
			{Name: "content-encoding", Value: "gzip"},
		},
		huffman: true,
		hpack:   "886196d07abe941054d444a8200595040b8166e084a62d1bff5a839bd9ab",
		hpackTable: []dynamicTableEntry{
			{"content-encoding", "gzip"},
			{"date", "Mon, 21 Oct 2013 20:13:22 GMT"},
		},
		qpackUpdates: "c396d07abe941054d444a8200595040b8166e084a62d1bff",
		qpackHeader:  "0100d580ff0f",
		qpackTable: &[]dynamicTableEntry{
			{"date", "Mon, 21 Oct 2013 20:13:22 GMT"},
		},
	},
	{
		resetTable: false,
		headers: []hc.HeaderField{
			{Name: ":status", Value: "200"},
			{Name: "date", Value: "Mon, 21 Oct 2013 20:13:22 GMT"},
			{Name: "content-encoding", Value: "gzip"},
		},
		huffman: true,
		hpack:   "88bfbe",
		hpackTable: []dynamicTableEntry{
			{"content-encoding", "gzip"},
			{"date", "Mon, 21 Oct 2013 20:13:22 GMT"},
		},
		qpackUpdates: "",
		qpackHeader:  "0100d580ff0f",
		qpackTable: &[]dynamicTableEntry{
			{"date", "Mon, 21 Oct 2013 20:13:22 GMT"},
		},
	},
}
