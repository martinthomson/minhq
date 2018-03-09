package hc_test

import (
	"bytes"
	"encoding/hex"
	"io"
	"sync"
	"testing"

	"github.com/martinthomson/minhq/hc"
	"github.com/stvp/assert"
)

func TestQcramEncoder(t *testing.T) {
	var encoder *hc.QcramEncoder
	token := "k"

	for _, tc := range testCases {
		if tc.resetTable {
			encoder = hc.NewQcramEncoder(256, 0)
			// The examples in RFC 7541 index date, which is of questionable utility.
			encoder.SetIndexPreference("date", true)
		} else {
			// We can use the same token here because always acknowledge before encoding
			// the next block.
			encoder.Acknowledge(token)
		}

		if tc.huffman {
			encoder.HuffmanPreference = hc.HuffmanCodingAlways
		} else {
			encoder.HuffmanPreference = hc.HuffmanCodingNever
		}

		var controlBuf bytes.Buffer
		var headerBuf bytes.Buffer
		err := encoder.WriteHeaderBlock(&controlBuf, &headerBuf, token, tc.headers...)
		assert.Nil(t, err)

		expectedControl, err := hex.DecodeString(tc.qcramControl)
		assert.Nil(t, err)
		if len(expectedControl) == 0 {
			// controlBuf.Bytes() returns nil if it hasn't been written to yet. meh.
			assert.Equal(t, 0, controlBuf.Len())
		} else {
			assert.Equal(t, expectedControl, controlBuf.Bytes())
		}

		expectedHeader, err := hex.DecodeString(tc.qcramHeader)
		assert.Nil(t, err)
		assert.Equal(t, expectedHeader, headerBuf.Bytes())

		var dynamicTable = &tc.hpackTable
		if tc.qcramTable != nil {
			dynamicTable = tc.qcramTable
		}
		checkDynamicTable(t, encoder.Table, dynamicTable)
	}
}

// This writes two simple header fields to the provided encoder. Note that this
// doesn't acknowledge that header block, so these will be pinned in the table
// until that can happen.
func setupEncoder(t *testing.T, encoder *hc.QcramEncoder) {
	var controlBuf bytes.Buffer
	var headerBuf bytes.Buffer
	err := encoder.WriteHeaderBlock(&controlBuf, &headerBuf, "setup",
		hc.HeaderField{Name: "name1", Value: "value1"},
		hc.HeaderField{Name: "name2", Value: "value2"})
	assert.Nil(t, err)

	// We should see inserts here.
	expectedControl, err := hex.DecodeString("4084a874943f85ee3a2d287f4084a874945f85ee3a2d28bf")
	assert.Nil(t, err)
	assert.Equal(t, expectedControl, controlBuf.Bytes())
	// And two references.
	assert.Equal(t, []byte{0x02, 0xbf, 0xbe}, headerBuf.Bytes())

	checkDynamicTable(t, encoder.Table, &tableState{
		size: 86,
		entries: []tableStateEntry{
			{"name2", "value2"},
			{"name1", "value1"},
		},
	})
}

// Attempt to write to the table.  Only literals should be produced.
func assertQcramTableFull(t *testing.T, encoder *hc.QcramEncoder) {
	var controlBuf bytes.Buffer
	var headerBuf bytes.Buffer

	token := "full"
	err := encoder.WriteHeaderBlock(&controlBuf, &headerBuf, token,
		hc.HeaderField{Name: "namef", Value: "valuef"})
	assert.Nil(t, err)
	assert.Equal(t, 0, controlBuf.Len())

	expectedHeader, err := hex.DecodeString("000084a874965f85ee3a2d2cbf")
	assert.Nil(t, err)
	assert.Equal(t, expectedHeader, headerBuf.Bytes())

	encoder.Acknowledge(token)
}

func TestQcramDuplicate(t *testing.T) {
	encoder := hc.NewQcramEncoder(200, 100)

	setupEncoder(t, encoder)

	var controlBuf bytes.Buffer
	var headerBuf bytes.Buffer
	err := encoder.WriteHeaderBlock(&controlBuf, &headerBuf, "token",
		hc.HeaderField{Name: "name0", Value: "value0"},
		hc.HeaderField{Name: "name1", Value: "value1"})
	assert.Nil(t, err)

	// This should include a duplication (that's the 3f21 on the end).
	expectedControl, err := hex.DecodeString("4084a874941f85ee3a2d283f3f21")
	assert.Nil(t, err)
	assert.Equal(t, expectedControl, controlBuf.Bytes())

	assert.Equal(t, []byte{0x04, 0xbf, 0xbe}, headerBuf.Bytes())

	checkDynamicTable(t, encoder.Table, &tableState{
		size: 172,
		entries: []tableStateEntry{
			{"name1", "value1"},
			{"name0", "value0"},
			{"name2", "value2"},
			{"name1", "value1"},
		},
	})

	assertQcramTableFull(t, encoder)
}

// TestQcramDuplicateLiteral sets up the conditions for a duplication, but the
// table is too small to allow it.
func TestQcramDuplicateLiteral(t *testing.T) {
	encoder := hc.NewQcramEncoder(150, 50)

	setupEncoder(t, encoder)

	var controlBuf bytes.Buffer
	var headerBuf bytes.Buffer
	err := encoder.WriteHeaderBlock(&controlBuf, &headerBuf, "token",
		hc.HeaderField{Name: "name0", Value: "value0"},
		hc.HeaderField{Name: "name1", Value: "value1"})
	assert.Nil(t, err)

	// name0:value0 can be added, but there isn't enough room to duplicate
	// name1:value1.
	expectedControl, err := hex.DecodeString("4084a874941f85ee3a2d283f")
	assert.Nil(t, err)
	assert.Equal(t, expectedControl, controlBuf.Bytes())

	expectedHeader, err := hex.DecodeString("03be0084a874943f85ee3a2d287f")
	assert.Nil(t, err)
	assert.Equal(t, expectedHeader, headerBuf.Bytes())

	checkDynamicTable(t, encoder.Table, &tableState{
		size: 129,
		entries: []tableStateEntry{
			{"name0", "value0"},
			{"name2", "value2"},
			{"name1", "value1"},
		},
	})

	assertQcramTableFull(t, encoder)
}

// Use a name reference and ensure that it can't be evicted.
func TestQcramNameReference(t *testing.T) {
	encoder := hc.NewQcramEncoder(150, 0)

	setupEncoder(t, encoder)

	var controlBuf bytes.Buffer
	var headerBuf bytes.Buffer
	err := encoder.WriteHeaderBlock(&controlBuf, &headerBuf, "token",
		hc.HeaderField{Name: "name1", Value: "value9"})
	assert.Nil(t, err)

	// 7f00 is an insert with a name reference.
	expectedControl, err := hex.DecodeString("7f0085ee3a2d2bff")
	assert.Nil(t, err)
	assert.Equal(t, expectedControl, controlBuf.Bytes())

	expectedHeader, err := hex.DecodeString("03be")
	assert.Nil(t, err)
	assert.Equal(t, expectedHeader, headerBuf.Bytes())

	checkDynamicTable(t, encoder.Table, &tableState{
		size: 129,
		entries: []tableStateEntry{
			{"name1", "value9"},
			{"name2", "value2"},
			{"name1", "value1"},
		},
	})
}

// This tests that a name reference can be created on a literal.
func TestNotIndexedNameReference(t *testing.T) {
	encoder := hc.NewQcramEncoder(100, 0)

	setupEncoder(t, encoder)

	// Block new table insertions for this key.
	encoder.SetIndexPreference("name1", false)
	var controlBuf bytes.Buffer
	var headerBuf bytes.Buffer
	err := encoder.WriteHeaderBlock(&controlBuf, &headerBuf, "token",
		hc.HeaderField{Name: "name1", Value: "value9"})
	assert.Nil(t, err)

	assert.Equal(t, 0, controlBuf.Len())

	expectedHeader, err := hex.DecodeString("010f2f85ee3a2d2bff")
	assert.Nil(t, err)
	assert.Equal(t, expectedHeader, headerBuf.Bytes())

	checkDynamicTable(t, encoder.Table, &tableState{
		size: 86,
		entries: []tableStateEntry{
			{"name2", "value2"},
			{"name1", "value1"},
		},
	})

	// Even after acknowledging the header block from setup, the reference to the
	// initial name1 entry remains outstanding and blocks eviction.
	encoder.Acknowledge("setup")
	assertQcramTableFull(t, encoder)
}

func TestQcramDecoderOrdered(t *testing.T) {
	var decoder *hc.QcramDecoder

	for _, tc := range testCases {
		if tc.resetTable {
			decoder = hc.NewQcramDecoder(256)
		}

		if len(tc.qcramControl) > 0 {
			control, err := hex.DecodeString(tc.qcramControl)
			assert.Nil(t, err)
			err = decoder.ReadTableUpdates(bytes.NewReader(control))
			assert.Nil(t, err)
		}

		var dynamicTable = &tc.hpackTable
		if tc.qcramTable != nil {
			dynamicTable = tc.qcramTable
		}
		checkDynamicTable(t, decoder.Table, dynamicTable)

		encoded, err := hex.DecodeString(tc.qcramHeader)
		assert.Nil(t, err)
		headers, err := decoder.ReadHeaderBlock(bytes.NewReader(encoded))
		assert.Nil(t, err)

		assert.Equal(t, tc.headers, headers)
	}
}

type notifyingReader struct {
	reader io.Reader
	signal *sync.Cond
	done   bool
}

func NewNotifyingReader(p []byte) *notifyingReader {
	return &notifyingReader{bytes.NewReader(p),
		sync.NewCond(&sync.Mutex{}), false}
}

func (nr *notifyingReader) Read(p []byte) (int, error) {
	nr.signal.Broadcast()
	nr.done = true
	return nr.reader.Read(p)
}

func (nr *notifyingReader) Wait() {
	for !nr.done {
		nr.signal.L.Lock()
		nr.signal.Wait()
		nr.signal.L.Unlock()
	}
}

// This test runs table updates and header blocks in parallel.
// Table updates are delayed until the reader starts trying to process the
// corresponding header block.
// batchRead can be set to wait for all reads at once. This only works if the
// encoder has *not* received acknowledgments for header blocks as it produces
// the encoded data.
func testQcramDecoderAsync(t *testing.T, batchRead bool, testData []testCase) {
	var decoder *hc.QcramDecoder
	var controlWriter io.WriteCloser
	var controlReader io.Reader
	controlDone := make(chan struct{})
	headerDone := new(sync.WaitGroup)

	fin := func() {
		controlWriter.Close()
		<-controlDone
		if batchRead {
			headerDone.Wait()
		}
	}

	for _, tc := range testData {
		if tc.resetTable {
			if controlReader != nil {
				fin()
			}
			decoder = hc.NewQcramDecoder(256)
			controlReader, controlWriter = io.Pipe()
			go func() {
				err := decoder.ReadTableUpdates(controlReader)
				assert.Nil(t, err)
				controlDone <- struct{}{}
			}()
		}

		headerDone.Add(1)
		headerBytes, err := hex.DecodeString(tc.qcramHeader)
		assert.Nil(t, err)
		nr := NewNotifyingReader(headerBytes)

		go func(tc testCase, r io.Reader) {
			defer headerDone.Done()
			headers, err := decoder.ReadHeaderBlock(r)
			assert.Nil(t, err)

			assert.Equal(t, tc.headers, headers)
		}(tc, nr)

		// After setting up the header block to decode, feed the control stream to the
		// reader.  First, wait for the header block reader to take a byte.
		if len(tc.qcramControl) > 0 {
			nr.Wait()
			controlBytes, err := hex.DecodeString(tc.qcramControl)
			assert.Nil(t, err)
			n, err := controlWriter.Write(controlBytes)
			assert.Nil(t, err)
			assert.Equal(t, len(controlBytes), n)
		}
		if !batchRead {
			headerDone.Wait()
		}
	}
	fin()
}

// This uses the default arrangement, so that table updates appear immediately
// after the header block that needs them.
func TestQcramDecoderThreaded(t *testing.T) {
	testQcramDecoderAsync(t, false, testCases)
}

// This delays the arrival of table updates by an additional cycle.
func TestAsyncHeaderUpdate(t *testing.T) {
	testQcramDecoderAsync(t, true, []testCase{
		{
			resetTable: true,
			headers: []hc.HeaderField{
				{Name: ":status", Value: "200"},
				{Name: "cache-control", Value: "private"},
				{Name: "date", Value: "Mon, 21 Oct 2013 20:13:21 GMT"},
				{Name: "location", Value: "https://www.example.com"},
			},
			qcramControl: "",
			qcramHeader:  "0388c0bfbe",
		},
		{
			resetTable: false,
			headers: []hc.HeaderField{
				{Name: ":status", Value: "307"},
				{Name: "cache-control", Value: "private"},
				{Name: "date", Value: "Mon, 21 Oct 2013 20:13:21 GMT"},
				{Name: "location", Value: "https://www.example.com"},
			},
			qcramControl: "5885aec3771a4b" + "6196d07abe941054d444a8200595040b8166e082a62d1bff" +
				"6e919d29ad171863c78f0b97c8e9ae82ae43d3" + "4883640eff",
			qcramHeader: "04bec1c0bf",
		},
	})
}

func TestAsyncHeaderDuplicate(t *testing.T) {
	testQcramDecoderAsync(t, true, []testCase{
		{
			resetTable: true,
			headers: []hc.HeaderField{
				{Name: ":status", Value: "200"},
				{Name: "cache-control", Value: "private"},
				{Name: "location", Value: "https://www.example.com"},
			},
			qcramControl: "",
			qcramHeader:  "0288bfbe",
		},
		{
			resetTable: false,
			headers: []hc.HeaderField{
				{Name: ":status", Value: "307"},
				{Name: "cache-control", Value: "private"},
				{Name: "location", Value: "https://www.example.com"},
			},
			qcramControl: "5885aec3771a4b" + "6e919d29ad171863c78f0b97c8e9ae82ae43d3" +
				"3f20" + "4883640eff",
			qcramHeader: "04bebfc0",
		},
	})
}
