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

func checkExpectedUpdates(t *testing.T, updateBuf *bytes.Buffer, expectedHex string) {
	expectedUpdates, err := hex.DecodeString(expectedHex)
	assert.Nil(t, err)
	if len(expectedUpdates) == 0 {
		// updateBuf.Bytes() returns nil if it hasn't been written to yet. meh.
		assert.Equal(t, 0, updateBuf.Len())
		return
	}

	// Remove and check the length that precedes the table updates.
	intReader := hc.NewReader(updateBuf)
	// Read the length.
	length, err := intReader.ReadInt(8)
	assert.Nil(t, err)
	// Pull that many octets from the buffer.
	contents := updateBuf.Next(int(length))
	assert.Equal(t, int(length), len(contents))
	// Check that there is no remaining data.
	_, err = updateBuf.ReadByte()
	assert.Equal(t, err, io.EOF)
	// Finally, check that the octets are correct.
	assert.Equal(t, expectedUpdates, contents)
}

func TestQpackEncoder(t *testing.T) {
	var encoder *hc.QpackEncoder
	id := uint64(3456789)
	var updateBuf bytes.Buffer

	for _, tc := range testCases {
		if tc.resetTable {
			t.Log("Reset encoder")
			encoder = hc.NewQpackEncoder(&updateBuf, 256, 0)
			encoder.SetMaxBlockedStreams(100)
			// The examples in RFC 7541 index date, which is of questionable utility.
			encoder.SetIndexPreference("date", true)
		} else {
			// We can use the same id here because always acknowledge before encoding
			// the next block.
			encoder.AcknowledgeHeader(id)
		}
		updateBuf.Reset()

		if tc.huffman {
			encoder.HuffmanPreference = hc.HuffmanCodingAlways
		} else {
			encoder.HuffmanPreference = hc.HuffmanCodingNever
		}

		t.Log("Encoding:")
		for _, h := range tc.headers {
			t.Logf("  %v", h)
		}

		var headerBuf bytes.Buffer
		err := encoder.WriteHeaderBlock(&headerBuf, id, tc.headers...)
		assert.Nil(t, err)
		t.Logf("Inserts:  %x", updateBuf.Bytes())
		t.Logf("Expected: %v", tc.qpackUpdates)
		t.Logf("Header Block: %x", headerBuf.Bytes())
		t.Logf("Expected:     %v", tc.qpackHeader)

		checkExpectedUpdates(t, &updateBuf, tc.qpackUpdates)

		expectedHeader, err := hex.DecodeString(tc.qpackHeader)
		assert.Nil(t, err)
		assert.Equal(t, expectedHeader, headerBuf.Bytes())

		var dynamicTable = &tc.hpackTable
		if tc.qpackTable != nil {
			dynamicTable = tc.qpackTable
		}
		checkDynamicTable(t, encoder.Table, dynamicTable)
	}
}

const setupToken = uint64(53709)

// This writes two simple header fields to the provided encoder. Note that this
// doesn't acknowledge that header block, so these will be pinned in the table
// until that can happen.
func setupEncoder(t *testing.T, encoder *hc.QpackEncoder, updateBuf *bytes.Buffer) {
	encoder.SetMaxBlockedStreams(100)

	var headerBuf bytes.Buffer
	err := encoder.WriteHeaderBlock(&headerBuf, setupToken,
		hc.HeaderField{Name: "name1", Value: "value1"},
		hc.HeaderField{Name: "name2", Value: "value2"})
	assert.Nil(t, err)
	t.Logf("Setup Table: %x %x", updateBuf.Bytes(), headerBuf.Bytes())

	// We should see inserts here.
	expectedUpdate, err := hex.DecodeString("1664a874943f85ee3a2d287f64a874945f85ee3a2d28bf")
	assert.Nil(t, err)
	assert.Equal(t, expectedUpdate, updateBuf.Bytes())
	// And two references.
	assert.Equal(t, []byte{0x02, 0x00, 0x81, 0x80}, headerBuf.Bytes())

	checkDynamicTable(t, encoder.Table, &[]dynamicTableEntry{
		{"name2", "value2"},
		{"name1", "value1"},
	})

	updateBuf.Reset()
}

// Attempt to write to the table.  Only literals should be produced.
func assertQpackTableFull(t *testing.T, encoder *hc.QpackEncoder, updateBuf *bytes.Buffer) {
	updateBuf.Reset()

	fullToken := uint64(890346979)
	var headerBuf bytes.Buffer
	err := encoder.WriteHeaderBlock(&headerBuf, fullToken,
		hc.HeaderField{Name: "namef", Value: "valuef"})
	assert.Nil(t, err)
	t.Logf("Table Full: [%x] %x", updateBuf.Bytes(), headerBuf.Bytes())
	assert.Equal(t, 0, updateBuf.Len())

	expectedHeader, err := hex.DecodeString("00006ca874965f85ee3a2d2cbf")
	assert.Nil(t, err)
	assert.Equal(t, expectedHeader, headerBuf.Bytes())

	encoder.AcknowledgeHeader(fullToken)
}

const defaultToken = uint64(12345)

func TestQpackDuplicate(t *testing.T) {
	var updateBuf bytes.Buffer
	encoder := hc.NewQpackEncoder(&updateBuf, 200, 100)
	setupEncoder(t, encoder, &updateBuf)

	// Allow the encoder to know that we got the inserts from the setup.
	encoder.AcknowledgeInsert(encoder.Table.Base())

	var headerBuf bytes.Buffer
	err := encoder.WriteHeaderBlock(&headerBuf, defaultToken,
		hc.HeaderField{Name: "name0", Value: "value0"},
		hc.HeaderField{Name: "name1", Value: "value1"})
	assert.Nil(t, err)
	t.Logf("Force Duplicate: %x %x", updateBuf.Bytes(), headerBuf.Bytes())

	// This should include a duplication (that's the 02 on the end).
	expectedUpdates, err := hex.DecodeString("0c64a874941f85ee3a2d283f02")
	assert.Nil(t, err)
	assert.Equal(t, expectedUpdates, updateBuf.Bytes())

	assert.Equal(t, []byte{0x04, 0x00, 0x81, 0x80}, headerBuf.Bytes())

	checkDynamicTable(t, encoder.Table, &[]dynamicTableEntry{
		{"name1", "value1"},
		{"name0", "value0"},
		{"name2", "value2"},
		{"name1", "value1"},
	})

	assertQpackTableFull(t, encoder, &updateBuf)
}

// TestQpackDuplicateLiteral sets up the conditions for a duplication, but the
// table is too small to allow it.
func TestQpackDuplicateLiteral(t *testing.T) {
	var updateBuf bytes.Buffer
	encoder := hc.NewQpackEncoder(&updateBuf, 150, 50)
	setupEncoder(t, encoder, &updateBuf)

	var headerBuf bytes.Buffer
	err := encoder.WriteHeaderBlock(&headerBuf, defaultToken,
		hc.HeaderField{Name: "name0", Value: "value0"},
		hc.HeaderField{Name: "name1", Value: "value1"})
	assert.Nil(t, err)
	t.Logf("Force Duplicate: %x %x", updateBuf.Bytes(), headerBuf.Bytes())

	// name0:value0 can be added, but there isn't enough room to duplicate
	// name1:value1, so it uses a literal.
	expectedUpdates, err := hex.DecodeString("0b64a874941f85ee3a2d283f")
	assert.Nil(t, err)
	assert.Equal(t, expectedUpdates, updateBuf.Bytes())

	expectedHeader, err := hex.DecodeString("0300806ca874943f85ee3a2d287f")
	assert.Nil(t, err)
	assert.Equal(t, expectedHeader, headerBuf.Bytes())

	checkDynamicTable(t, encoder.Table, &[]dynamicTableEntry{
		{"name0", "value0"},
		{"name2", "value2"},
		{"name1", "value1"},
	})

	assertQpackTableFull(t, encoder, &updateBuf)
}

func TestQpackBlockedEncode(t *testing.T) {
	var updateBuf bytes.Buffer
	encoder := hc.NewQpackEncoder(&updateBuf, 200, 50)
	setupEncoder(t, encoder, &updateBuf)

	// Limit to just one blocking stream.
	encoder.SetMaxBlockedStreams(1)

	// Initially, the setup stream will be the blocking stream,
	// so this should emit a literal only.
	var headerBuf bytes.Buffer
	err := encoder.WriteHeaderBlock(&headerBuf, defaultToken,
		hc.HeaderField{Name: "name1", Value: "value1"})
	assert.Nil(t, err)
	t.Logf("Blocked on setup: %x %x", updateBuf.Bytes(), headerBuf.Bytes())

	assert.Equal(t, []byte{}, updateBuf.Bytes())
	expectedHeader, err := hex.DecodeString("00006ca874943f85ee3a2d287f")
	assert.Nil(t, err)
	assert.Equal(t, expectedHeader, headerBuf.Bytes())

	checkDynamicTable(t, encoder.Table, &[]dynamicTableEntry{
		{"name2", "value2"},
		{"name1", "value1"},
	})

	// Acknowledging the setup stream should allow the header block to
	// reference the entries added during setup.  And it can block itself.
	encoder.AcknowledgeHeader(setupToken)

	headerBuf.Reset()
	err = encoder.WriteHeaderBlock(&headerBuf, defaultToken,
		hc.HeaderField{Name: "name1", Value: "value1"}, // this can index now
		hc.HeaderField{Name: "name3", Value: "value3"}, // this inserts fine
	)
	assert.Nil(t, err)
	t.Logf("Unblocked: %x %x", updateBuf.Bytes(), headerBuf.Bytes())

	expectedUpdates, err := hex.DecodeString("0b64a874959f85ee3a2d2b3f")
	assert.Nil(t, err)
	assert.Equal(t, expectedUpdates, updateBuf.Bytes())
	expectedHeader, err = hex.DecodeString("03008280")
	assert.Nil(t, err)
	assert.Equal(t, expectedHeader, headerBuf.Bytes())

	checkDynamicTable(t, encoder.Table, &[]dynamicTableEntry{
		{"name3", "value3"},
		{"name2", "value2"},
		{"name1", "value1"},
	})

	// Header blocks on the same stream can block more.
	headerBuf.Reset()
	updateBuf.Reset()
	err = encoder.WriteHeaderBlock(&headerBuf, defaultToken,
		hc.HeaderField{Name: "name3", Value: "value3"}, // this uses the index
		hc.HeaderField{Name: "name4", Value: "value4"}, // this inserts fine
	)
	assert.Nil(t, err)
	t.Logf("Same stream: %x %x", updateBuf.Bytes(), headerBuf.Bytes())

	expectedUpdates, err = hex.DecodeString("0b64a87495af85ee3a2d2b5f")
	assert.Nil(t, err)
	assert.Equal(t, expectedUpdates, updateBuf.Bytes())
	expectedHeader, err = hex.DecodeString("04008180")
	assert.Nil(t, err)
	assert.Equal(t, expectedHeader, headerBuf.Bytes())

	checkDynamicTable(t, encoder.Table, &[]dynamicTableEntry{
		{"name4", "value4"},
		{"name3", "value3"},
		{"name2", "value2"},
		{"name1", "value1"},
	})

	// While that stream is blocked, another stream won't insert or reference
	// the entries that aren't acknowledged.
	headerBuf.Reset()
	updateBuf.Reset()
	err = encoder.WriteHeaderBlock(&headerBuf, defaultToken+1,
		hc.HeaderField{Name: "name3", Value: "value3"}, // this produces a literal
	)
	assert.Nil(t, err)
	t.Logf("Other Stream: %x %x", updateBuf.Bytes(), headerBuf.Bytes())

	assert.Equal(t, []byte{}, updateBuf.Bytes())
	expectedHeader, err = hex.DecodeString("00006ca874959f85ee3a2d2b3f")
	assert.Nil(t, err)
	assert.Equal(t, expectedHeader, headerBuf.Bytes())

	checkDynamicTable(t, encoder.Table, &[]dynamicTableEntry{
		{"name4", "value4"},
		{"name3", "value3"},
		{"name2", "value2"},
		{"name1", "value1"},
	})
}

// Use a name reference and ensure that it can't be evicted.
func TestQpackNameReference(t *testing.T) {
	var updateBuf bytes.Buffer
	encoder := hc.NewQpackEncoder(&updateBuf, 150, 0)
	setupEncoder(t, encoder, &updateBuf)

	var headerBuf bytes.Buffer
	err := encoder.WriteHeaderBlock(&headerBuf, defaultToken,
		hc.HeaderField{Name: "name1", Value: "value9"})
	assert.Nil(t, err)
	t.Logf("Name Reference: %x %x", updateBuf.Bytes(), headerBuf.Bytes())

	// 81 is an insert with a name reference.
	expectedUpdates, err := hex.DecodeString("078185ee3a2d2bff")
	assert.Nil(t, err)
	assert.Equal(t, expectedUpdates, updateBuf.Bytes())

	expectedHeader, err := hex.DecodeString("030080")
	assert.Nil(t, err)
	assert.Equal(t, expectedHeader, headerBuf.Bytes())

	checkDynamicTable(t, encoder.Table, &[]dynamicTableEntry{
		{"name1", "value9"},
		{"name2", "value2"},
		{"name1", "value1"},
	})
}

// This tests that a name reference can be created on a literal.
func TestNotIndexedNameReference(t *testing.T) {
	var updateBuf bytes.Buffer
	encoder := hc.NewQpackEncoder(&updateBuf, 100, 0)
	setupEncoder(t, encoder, &updateBuf)

	// Block new table insertions for this key.
	encoder.SetIndexPreference("name1", false)
	var headerBuf bytes.Buffer
	err := encoder.WriteHeaderBlock(&headerBuf, defaultToken,
		hc.HeaderField{Name: "name1", Value: "value9"})
	assert.Nil(t, err)
	t.Logf("Non-Indexed Name Reference: [%x] %x", updateBuf.Bytes(), headerBuf.Bytes())

	assert.Equal(t, 0, updateBuf.Len())

	expectedHeader, err := hex.DecodeString("01000085ee3a2d2bff")
	assert.Nil(t, err)
	assert.Equal(t, expectedHeader, headerBuf.Bytes())

	checkDynamicTable(t, encoder.Table, &[]dynamicTableEntry{
		{"name2", "value2"},
		{"name1", "value1"},
	})

	// Even after acknowledging the header block from setup, the reference to the
	// initial name1 entry remains outstanding and blocks eviction.
	encoder.AcknowledgeHeader(setupToken)
	assertQpackTableFull(t, encoder, &updateBuf)
}

func checkAck(t *testing.T, ackReader *hc.Reader, eb byte, ev uint64) {
	b, err := ackReader.ReadBit()
	assert.Nil(t, err)
	assert.Equal(t, b, eb)
	v, err := ackReader.ReadInt(7)
	assert.Equal(t, v, ev)
}

func TestQpackDecoderOrdered(t *testing.T) {
	var ackReader *hc.Reader
	var decoder *hc.QpackDecoder
	tableEntries := 0
	var token uint64
	for _, tc := range testCases {
		if tc.resetTable {
			t.Log("Reset table")
			reader, writer := io.Pipe()
			ackReader = hc.NewReader(reader)
			decoder = hc.NewQpackDecoder(writer, 256)
			tableEntries = 0
		}
		t.Logf("Decode:")
		for _, h := range tc.headers {
			t.Logf("  %v", h)
		}

		if len(tc.qpackUpdates) > 0 {
			t.Logf("Control: %v", tc.qpackUpdates)
			control, err := hex.DecodeString(tc.qpackUpdates)
			assert.Nil(t, err)
			err = decoder.ReadTableUpdates(bytes.NewReader(control))
			assert.Nil(t, err)
		}

		var dynamicTable = &tc.hpackTable
		if tc.qpackTable != nil {
			dynamicTable = tc.qpackTable
		}
		checkDynamicTable(t, decoder.Table, dynamicTable)

		// There were new entries.  Check that they were acknowledged.
		if decoder.Table.Base() > tableEntries {
			checkAck(t, ackReader, 1, uint64(decoder.Table.Base()))
			tableEntries = decoder.Table.Base()
		}

		t.Logf("Header: %v", tc.qpackHeader)
		encoded, err := hex.DecodeString(tc.qpackHeader)
		assert.Nil(t, err)
		headers, err := decoder.ReadHeaderBlock(bytes.NewReader(encoded), token)
		assert.Nil(t, err)

		assert.Equal(t, tc.headers, headers)
		checkAck(t, ackReader, 0, token)
		token++
	}
}

type notifyingReader struct {
	reader io.Reader
	signal *sync.Cond
	done   chan struct{}
}

func NewNotifyingReader(p []byte) *notifyingReader {
	return &notifyingReader{bytes.NewReader(p),
		sync.NewCond(&sync.Mutex{}), make(chan struct{})}
}

func (nr *notifyingReader) Read(p []byte) (int, error) {
	nr.signal.Broadcast()
	select {
	case <-nr.done:
		// We're done here.
	default:
		close(nr.done)
	}
	return nr.reader.Read(p)
}

func (nr *notifyingReader) Wait() {
	for {
		select {
		case <-nr.done:
			return
		default:
			nr.signal.L.Lock()
			nr.signal.Wait()
			nr.signal.L.Unlock()
		}
	}
}

// This test runs table updates and header blocks in parallel.
// Table updates are delayed until the reader starts trying to process the
// corresponding header block.
// batchRead can be set to wait for all reads at once. This only works if the
// encoder has *not* received acknowledgments for header blocks as it produces
// the encoded data.
func testQpackDecoderAsync(t *testing.T, batchRead bool, testData []testCase) {
	var decoder *hc.QpackDecoder
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

	for i, tc := range testData {
		if tc.resetTable {
			if controlReader != nil {
				fin()
			}
			var ackBuf bytes.Buffer
			decoder = hc.NewQpackDecoder(&ackBuf, 256)
			controlReader, controlWriter = io.Pipe()
			go func() {
				err := decoder.ReadTableUpdates(controlReader)
				assert.Nil(t, err)
				controlDone <- struct{}{}
			}()
		}

		headerDone.Add(1)
		headerBytes, err := hex.DecodeString(tc.qpackHeader)
		assert.Nil(t, err)
		nr := NewNotifyingReader(headerBytes)

		go func(i int, tc testCase, r io.Reader) {
			defer headerDone.Done()
			headers, err := decoder.ReadHeaderBlock(r, uint64(i))
			assert.Nil(t, err)

			assert.Equal(t, tc.headers, headers)
		}(i, tc, nr)

		// After setting up the header block to decode, feed the control stream to the
		// reader.  First, wait for the header block reader to take a byte.
		if len(tc.qpackUpdates) > 0 {
			nr.Wait()
			controlBytes, err := hex.DecodeString(tc.qpackUpdates)
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
func TestQpackDecoderThreaded(t *testing.T) {
	testQpackDecoderAsync(t, false, testCases)
}

// This delays the arrival of table updates by an additional cycle.
func TestAsyncHeaderUpdate(t *testing.T) {
	testQpackDecoderAsync(t, true, []testCase{
		{
			resetTable: true,
			headers: []hc.HeaderField{
				{Name: ":status", Value: "200"},
				{Name: "cache-control", Value: "private"},
				{Name: "date", Value: "Mon, 21 Oct 2013 20:13:21 GMT"},
				{Name: "location", Value: "https://www.example.com"},
			},
			qpackUpdates: "",
			qpackHeader:  "0300d5828180",
		},
		{
			resetTable: false,
			headers: []hc.HeaderField{
				{Name: ":status", Value: "307"},
				{Name: "cache-control", Value: "private"},
				{Name: "date", Value: "Mon, 21 Oct 2013 20:13:21 GMT"},
				{Name: "location", Value: "https://www.example.com"},
			},
			qpackUpdates: "f10770726976617465" +
				"c31d4d6f6e2c203231204f637420323031332032303a31333a323120474d54" + "c91768747470733a2f2f7777772e6578616d706c652e636f6d" +
				"d503333037",
			qpackHeader: "040080838281",
		},
	})
}

func TestAsyncHeaderDuplicate(t *testing.T) {
	testQpackDecoderAsync(t, true, []testCase{
		{
			resetTable: true,
			headers: []hc.HeaderField{
				{Name: ":status", Value: "200"},
				{Name: "cache-control", Value: "private"},
				{Name: "location", Value: "https://www.example.com"},
			},
			qpackUpdates: "",
			qpackHeader:  "0200d58180",
		},
		{
			resetTable: false,
			headers: []hc.HeaderField{
				{Name: ":status", Value: "307"},
				{Name: "cache-control", Value: "private"},
				{Name: "location", Value: "https://www.example.com"},
			},
			qpackUpdates: "f10770726976617465" +
				"c91768747470733a2f2f7777772e6578616d706c652e636f6d" +
				"d503333037" + "02",
			qpackHeader: "0400818082",
		},
	})
}

// TestSingleRecordOverflow inserts into a table that is too small for
// even a single record to fit.
func TestSingleRecordOverflow(t *testing.T) {
	var ackBuf bytes.Buffer
	decoder := hc.NewQpackDecoder(&ackBuf, 20)
	updates, err := hex.DecodeString("64a874943f85ee3a2d287f")
	assert.Nil(t, err)
	err = decoder.ReadTableUpdates(bytes.NewReader(updates))
	assert.Equal(t, err, hc.ErrTableOverflow)
}
