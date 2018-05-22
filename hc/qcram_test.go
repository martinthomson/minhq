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
			t.Log("Reset encoder")
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

		t.Log("Encoding:")
		for _, h := range tc.headers {
			t.Logf("  %v", h)
		}

		var controlBuf bytes.Buffer
		var headerBuf bytes.Buffer
		err := encoder.WriteHeaderBlock(&controlBuf, &headerBuf, token, tc.headers...)
		assert.Nil(t, err)
		t.Logf("Inserts:  %x", controlBuf.Bytes())
		t.Logf("Expected: %v", tc.qcramControl)
		t.Logf("Header Block: %x", headerBuf.Bytes())
		t.Logf("Expected:     %v", tc.qcramHeader)

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
	t.Logf("Setup Table: %x %x", controlBuf.Bytes(), headerBuf.Bytes())

	// We should see inserts here.
	expectedControl, err := hex.DecodeString("64a874943f85ee3a2d287f64a874945f85ee3a2d28bf")
	assert.Nil(t, err)
	assert.Equal(t, expectedControl, controlBuf.Bytes())
	// And two references.
	assert.Equal(t, []byte{0x02, 0x00, 0x81, 0x80}, headerBuf.Bytes())

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
	t.Logf("Table Full: [%x] %x", controlBuf.Bytes(), headerBuf.Bytes())
	assert.Equal(t, 0, controlBuf.Len())

	expectedHeader, err := hex.DecodeString("00006ca874965f85ee3a2d2cbf")
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
	t.Logf("Force Duplicate: %x %x", controlBuf.Bytes(), headerBuf.Bytes())

	// This should include a duplication (that's the 02 on the end).
	expectedControl, err := hex.DecodeString("64a874941f85ee3a2d283f02")
	assert.Nil(t, err)
	assert.Equal(t, expectedControl, controlBuf.Bytes())

	assert.Equal(t, []byte{0x04, 0x00, 0x81, 0x80}, headerBuf.Bytes())

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
	t.Logf("Force Duplicate: %x %x", controlBuf.Bytes(), headerBuf.Bytes())

	// name0:value0 can be added, but there isn't enough room to duplicate
	// name1:value1, so it uses a literal.
	expectedControl, err := hex.DecodeString("64a874941f85ee3a2d283f")
	assert.Nil(t, err)
	assert.Equal(t, expectedControl, controlBuf.Bytes())

	expectedHeader, err := hex.DecodeString("0300806ca874943f85ee3a2d287f")
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
	t.Logf("Name Reference: %x %x", controlBuf.Bytes(), headerBuf.Bytes())

	// 81 is an insert with a name reference.
	expectedControl, err := hex.DecodeString("8185ee3a2d2bff")
	assert.Nil(t, err)
	assert.Equal(t, expectedControl, controlBuf.Bytes())

	expectedHeader, err := hex.DecodeString("030080")
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
	t.Logf("Non-Indexed Name Reference: [%x] %x", controlBuf.Bytes(), headerBuf.Bytes())

	assert.Equal(t, 0, controlBuf.Len())

	expectedHeader, err := hex.DecodeString("01000085ee3a2d2bff")
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
			t.Log("Reset table")
			decoder = hc.NewQcramDecoder(256)
		}
		t.Logf("Decode:")
		for _, h := range tc.headers {
			t.Logf("  %v", h)
		}

		if len(tc.qcramControl) > 0 {
			t.Logf("Control: %v", tc.qcramControl)
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

		t.Logf("Header: %v", tc.qcramHeader)
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
			qcramHeader:  "0300d5828180",
		},
		{
			resetTable: false,
			headers: []hc.HeaderField{
				{Name: ":status", Value: "307"},
				{Name: "cache-control", Value: "private"},
				{Name: "date", Value: "Mon, 21 Oct 2013 20:13:21 GMT"},
				{Name: "location", Value: "https://www.example.com"},
			},
			qcramControl: "f10770726976617465" +
				"c31d4d6f6e2c203231204f637420323031332032303a31333a323120474d54" + "c91768747470733a2f2f7777772e6578616d706c652e636f6d" +
				"d503333037",
			qcramHeader: "040080838281",
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
			qcramHeader:  "0200d58180",
		},
		{
			resetTable: false,
			headers: []hc.HeaderField{
				{Name: ":status", Value: "307"},
				{Name: "cache-control", Value: "private"},
				{Name: "location", Value: "https://www.example.com"},
			},
			qcramControl: "f10770726976617465" +
				"c91768747470733a2f2f7777772e6578616d706c652e636f6d" +
				"d503333037" + "02",
			qcramHeader: "0400818082",
		},
	})
}
