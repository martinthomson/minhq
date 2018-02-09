package hc_test

import (
	"testing"

	"github.com/martinthomson/minhq/hc"
	"github.com/stvp/assert"
)

type tableStateEntry struct {
	name  string
	value string
}

type tableState struct {
	size    hc.TableCapacity
	entries []tableStateEntry
}

func checkDynamicTable(t *testing.T, table hc.Table, ts *tableState) {
	assert.Equal(t, ts.size, table.Used())
	for i, e := range ts.entries {
		// The initial offset for dynamic entries is 62 in HPACK.
		entry := table.Get(i + 62)
		assert.NotNil(t, entry)
		assert.Equal(t, e.name, entry.Name())
		assert.Equal(t, e.value, entry.Value())
	}
}

type testCase struct {
	resetTable   bool
	headers      []hc.HeaderField
	huffman      bool
	hpack        string
	hpackTable   tableState
	qcramControl string
	qcramHeader  string
	qcramTable   *tableState
}

var testCases = []testCase{
	{
		resetTable: true,
		headers: []hc.HeaderField{
			{Name: "custom-key", Value: "custom-header"},
		},
		huffman: false,
		hpack:   "400a637573746f6d2d6b65790d637573746f6d2d686561646572",
		hpackTable: tableState{
			size: 55,
			entries: []tableStateEntry{
				{"custom-key", "custom-header"},
			},
		},
		qcramControl: "400a637573746f6d2d6b65790d637573746f6d2d686561646572",
		qcramHeader:  "01be",
	},
	{
		resetTable: true,
		headers: []hc.HeaderField{
			{Name: ":path", Value: "/sample/path"},
		},
		huffman: false,
		hpack:   "040c2f73616d706c652f70617468",
		hpackTable: tableState{
			size: 0,
		},
		qcramControl: "",
		qcramHeader:  "00040c2f73616d706c652f70617468",
	},
	{
		resetTable: true,
		headers: []hc.HeaderField{
			{Name: "password", Value: "secret", Sensitive: true},
		},
		huffman: false,
		hpack:   "100870617373776f726406736563726574",
		hpackTable: tableState{
			size: 0,
		},
		qcramControl: "",
		qcramHeader:  "00100870617373776f726406736563726574",
	},
	{
		resetTable: true,
		headers: []hc.HeaderField{
			{Name: ":method", Value: "GET"},
		},
		huffman: false,
		hpack:   "82",
		hpackTable: tableState{
			size: 0,
		},
		qcramControl: "",
		qcramHeader:  "0082",
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
		hpackTable: tableState{
			size: 57,
			entries: []tableStateEntry{
				{":authority", "www.example.com"},
			},
		},
		qcramControl: "410f7777772e6578616d706c652e636f6d",
		qcramHeader:  "01828684be",
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
		hpackTable: tableState{
			size: 110,
			entries: []tableStateEntry{
				{"cache-control", "no-cache"},
				{":authority", "www.example.com"},
			},
		},
		qcramControl: "58086e6f2d6361636865",
		qcramHeader:  "02828684bfbe",
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
		hpackTable: tableState{
			size: 164,
			entries: []tableStateEntry{
				{"custom-key", "custom-value"},
				{"cache-control", "no-cache"},
				{":authority", "www.example.com"},
			},
		},
		qcramControl: "400a637573746f6d2d6b65790c637573746f6d2d76616c7565",
		qcramHeader:  "03828785c0be",
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
		hpackTable: tableState{
			size: 57,
			entries: []tableStateEntry{
				{":authority", "www.example.com"},
			},
		},
		qcramControl: "418cf1e3c2e5f23a6ba0ab90f4ff",
		qcramHeader:  "01828684be",
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
		hpackTable: tableState{
			size: 110,
			entries: []tableStateEntry{
				{"cache-control", "no-cache"},
				{":authority", "www.example.com"},
			},
		},
		qcramControl: "5886a8eb10649cbf",
		qcramHeader:  "02828684bfbe",
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
		hpackTable: tableState{
			size: 164,
			entries: []tableStateEntry{
				{"custom-key", "custom-value"},
				{"cache-control", "no-cache"},
				{":authority", "www.example.com"},
			},
		},
		qcramControl: "408825a849e95ba97d7f8925a849e95bb8e8b4bf",
		qcramHeader:  "03828785c0be",
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
		hpackTable: tableState{
			size: 222,
			entries: []tableStateEntry{
				{"location", "https://www.example.com"},
				{"date", "Mon, 21 Oct 2013 20:13:21 GMT"},
				{"cache-control", "private"},
				{":status", "302"},
			},
		},
		qcramControl: "4803333032580770726976617465611d4d6f6e2c203231204f63742032303133" +
			"2032303a31333a323120474d546e1768747470733a2f2f7777772e6578616d70" +
			"6c652e636f6d",
		qcramHeader: "04c1c0bfbe",
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
		hpackTable: tableState{
			size: 222,
			entries: []tableStateEntry{
				{":status", "307"},
				{"location", "https://www.example.com"},
				{"date", "Mon, 21 Oct 2013 20:13:21 GMT"},
				{"cache-control", "private"},
			},
		},
		qcramControl: "4803333037",
		qcramHeader:  "05bec1c0bf",
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
		hpackTable: tableState{
			size: 215,
			entries: []tableStateEntry{
				{"set-cookie", "foo=ASDJKHQKBZXOQWEOPIUAXQWEOIU; max-age=3600; version=1"},
				{"content-encoding", "gzip"},
				{"date", "Mon, 21 Oct 2013 20:13:22 GMT"},
			},
		},
		qcramControl: "",
		qcramHeader: "0488c00f121d4d6f6e2c203231204f637420323031332032303a31333a323220474d" +
			"54be0f0b04677a69700f2838666f6f3d4153444a4b48514b425a584f5157454f5049" +
			"5541585157454f49553b206d61782d6167653d333630303b2076657273696f6e" +
			"3d31",
		// 0488c00004646174651d4d6f6e2c203231204f637420323031332032303a31333a323220474d54be0010636f6e74656e742d656e636f64696e6704677a6970000a7365742d636f6f6b696538666f6f3d4153444a4b48514b425a584f5157454f50495541585157454f49553b206d61782d6167653d333630303b2076657273696f6e3d31
		qcramTable: &tableState{
			size: 222,
			entries: []tableStateEntry{
				{":status", "307"},
				{"location", "https://www.example.com"},
				{"date", "Mon, 21 Oct 2013 20:13:21 GMT"},
				{"cache-control", "private"},
			},
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
		hpackTable: tableState{
			size: 222,
			entries: []tableStateEntry{
				{"location", "https://www.example.com"},
				{"date", "Mon, 21 Oct 2013 20:13:21 GMT"},
				{"cache-control", "private"},
				{":status", "302"},
			},
		},
		qcramControl: "488264025885aec3771a4b6196d07abe941054d444a8200595040b8166e082a6" +
			"2d1bff6e919d29ad171863c78f0b97c8e9ae82ae43d3",
		qcramHeader: "04c1c0bfbe",
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
		hpackTable: tableState{
			size: 222,
			entries: []tableStateEntry{
				{":status", "307"},
				{"location", "https://www.example.com"},
				{"date", "Mon, 21 Oct 2013 20:13:21 GMT"},
				{"cache-control", "private"},
			},
		},
		qcramControl: "4883640eff",
		qcramHeader:  "05bec1c0bf",
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
		hpackTable: tableState{
			size: 215,
			entries: []tableStateEntry{
				{"set-cookie", "foo=ASDJKHQKBZXOQWEOPIUAXQWEOIU; max-age=3600; version=1"},
				{"content-encoding", "gzip"},
				{"date", "Mon, 21 Oct 2013 20:13:22 GMT"},
			},
		},
		qcramControl: "",
		qcramHeader: "04" + "88c0" + "0f1296d07abe941054d444a8200595040b8166e084a62d1bff" +
			"be" + "0f0b839bd9ab" + "0f28ad94e7821dd7f2e6c7b335dfdfcd5b3960d5af27087f3672c1ab27" +
			"0fb5291f9587316065c003ed4ee5b1063d5007",
		qcramTable: &tableState{
			size: 222,
			entries: []tableStateEntry{
				{":status", "307"},
				{"location", "https://www.example.com"},
				{"date", "Mon, 21 Oct 2013 20:13:21 GMT"},
				{"cache-control", "private"},
			},
		},
	},
	// Using existing values in the dynamic table revealed a bug in QCRAM.
	{
		resetTable: true,
		headers: []hc.HeaderField{
			{Name: ":status", Value: "200"},
			{Name: "date", Value: "Mon, 21 Oct 2013 20:13:22 GMT"},
			{Name: "content-encoding", Value: "gzip"},
		},
		huffman: true,
		hpack:   "886196d07abe941054d444a8200595040b8166e084a62d1bff5a839bd9ab",
		hpackTable: tableState{
			size: 117,
			entries: []tableStateEntry{
				{"content-encoding", "gzip"},
				{"date", "Mon, 21 Oct 2013 20:13:22 GMT"},
			},
		},
		qcramControl: "6196d07abe941054d444a8200595040b8166e084a62d1bff5a839bd9ab",
		qcramHeader:  "0288bfbe",
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
		hpackTable: tableState{
			size: 117,
			entries: []tableStateEntry{
				{"content-encoding", "gzip"},
				{"date", "Mon, 21 Oct 2013 20:13:22 GMT"},
			},
		},
		qcramControl: "",
		qcramHeader:  "0288bfbe",
	},
}
