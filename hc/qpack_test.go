package hc_test

import (
	"bytes"
	"encoding/hex"
	"io"
	"sync"
	"testing"
	"time"

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
	assert.Equal(t, expectedUpdates, updateBuf.Bytes())
}

func TestQpackEncoder(t *testing.T) {
	var encoder *hc.QpackEncoder
	id := uint64(3456789)
	var updateBuf bytes.Buffer

	for _, tc := range testCases {
		if tc.resetTable {
			t.Log("Reset encoder")
			encoder = hc.NewQpackEncoder(&updateBuf, 256, 256)
			encoder.SetMaxBlockedStreams(100)
			// The examples in RFC 7541 index date, which is of questionable utility.
			encoder.SetIndexPreference("date", true)
		} else {
			// We can use the same id here because always acknowledge before encoding
			// the next block.
			assert.Nil(t, encoder.AcknowledgeHeader(id))
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
		assert.Equal(t, encoder.Table.Base(), tc.qpackTable.base)

		expectedHeader, err := hex.DecodeString(tc.qpackHeader)
		assert.Nil(t, err)
		assert.Equal(t, expectedHeader, headerBuf.Bytes())

		var dynamicTable = &tc.hpackTable
		if tc.qpackTable.entries != nil {
			dynamicTable = tc.qpackTable.entries
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
	expectedUpdate, err := hex.DecodeString("64a874943f85ee3a2d287f64a874945f85ee3a2d28bf")
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
func assertQpackTableFull(t *testing.T, encoder *hc.QpackEncoder) {
	fullToken := uint64(890346979)
	var headerBuf bytes.Buffer
	var updateBuf bytes.Buffer
	err := encoder.WriteHeaderBlock(&headerBuf, fullToken,
		hc.HeaderField{Name: "namef", Value: "valuef"})
	assert.Nil(t, err)
	t.Logf("Table Full: [%x] %x", updateBuf.Bytes(), headerBuf.Bytes())
	assert.Equal(t, 0, updateBuf.Len())

	expectedHeader, err := hex.DecodeString("00002ca874965f85ee3a2d2cbf")
	assert.Nil(t, err)
	assert.Equal(t, expectedHeader, headerBuf.Bytes())

	// No need to acknowledge this header block, because it didn't reference
	// the header table.
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
	expectedUpdates, err := hex.DecodeString("64a874941f85ee3a2d283f02")
	assert.Nil(t, err)
	assert.Equal(t, expectedUpdates, updateBuf.Bytes())

	assert.Equal(t, []byte{0x04, 0x00, 0x81, 0x80}, headerBuf.Bytes())

	checkDynamicTable(t, encoder.Table, &[]dynamicTableEntry{
		{"name1", "value1"},
		{"name0", "value0"},
		{"name2", "value2"},
		{"name1", "value1"},
	})

	assertQpackTableFull(t, encoder)
}

// TestQpackDuplicateLiteral sets up the conditions for a duplication, but the
// table is too small to allow it.
func TestQpackDuplicateLiteral(t *testing.T) {
	var updateBuf bytes.Buffer
	encoder := hc.NewQpackEncoder(&updateBuf, 150, 100)
	setupEncoder(t, encoder, &updateBuf)

	var headerBuf bytes.Buffer
	err := encoder.WriteHeaderBlock(&headerBuf, defaultToken,
		hc.HeaderField{Name: "name0", Value: "value0"},
		hc.HeaderField{Name: "name1", Value: "value1"})
	assert.Nil(t, err)
	t.Logf("Force Duplicate: %x %x", updateBuf.Bytes(), headerBuf.Bytes())

	// Neither entry can be added because that would increase the amount of
	// unacknowledged entries beyond the referenceable limit.  So we get a literal
	// for name0:value0 and a reference to name1:value1
	assert.Equal(t, 0, updateBuf.Len())

	expectedHeader, err := hex.DecodeString("01002ca874941f85ee3a2d283f80")
	assert.Nil(t, err)
	assert.Equal(t, expectedHeader, headerBuf.Bytes())

	checkDynamicTable(t, encoder.Table, &[]dynamicTableEntry{
		{"name2", "value2"},
		{"name1", "value1"},
	})

	assertQpackTableFull(t, encoder)

	// Now acknowledge the setup and these new additions.
	assert.Nil(t, encoder.AcknowledgeHeader(setupToken))
	assert.Nil(t, encoder.AcknowledgeHeader(defaultToken))
	updateBuf.Reset()
	headerBuf.Reset()

	// From here, the first entry can be added.
	// That pushes name1:value1 into being unreferenceable, so
	// it will be duplicated.
	// That prevents any further additions, so while name2:value2
	// could be duplicated - it's in the table - no new entries
	// can be added because there is already too much unacknowledged
	// So name2:value2 is sent as a literal.
	err = encoder.WriteHeaderBlock(&headerBuf, defaultToken+1,
		hc.HeaderField{Name: "name0", Value: "value0"},
		hc.HeaderField{Name: "name1", Value: "value1"},
		hc.HeaderField{Name: "name2", Value: "value2"})
	assert.Nil(t, err)
	t.Logf("Force Duplicate: %x %x", updateBuf.Bytes(), headerBuf.Bytes())

	expectedUpdates, err := hex.DecodeString("64a874941f85ee3a2d283f02")
	assert.Nil(t, err)
	assert.Equal(t, expectedUpdates, updateBuf.Bytes())

	expectedHeader, err = hex.DecodeString("040081802ca874945f85ee3a2d28bf")
	assert.Nil(t, err)
	assert.Equal(t, expectedHeader, headerBuf.Bytes())

	checkDynamicTable(t, encoder.Table, &[]dynamicTableEntry{
		{"name1", "value1"},
		{"name0", "value0"},
		{"name2", "value2"},
	})

	// No more inserts can be made.
	assertQpackTableFull(t, encoder)
}

func TestQpackBlockedEncode(t *testing.T) {
	var updateBuf bytes.Buffer
	encoder := hc.NewQpackEncoder(&updateBuf, 250, 200)
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
	expectedHeader, err := hex.DecodeString("00002ca874943f85ee3a2d287f")
	assert.Nil(t, err)
	assert.Equal(t, expectedHeader, headerBuf.Bytes())

	checkDynamicTable(t, encoder.Table, &[]dynamicTableEntry{
		{"name2", "value2"},
		{"name1", "value1"},
	})

	// Acknowledging the setup stream should allow the header block to
	// reference the entries added during setup.  And it can block itself.
	assert.Nil(t, encoder.AcknowledgeHeader(setupToken))

	headerBuf.Reset()
	err = encoder.WriteHeaderBlock(&headerBuf, defaultToken,
		hc.HeaderField{Name: "name1", Value: "value1"}, // this can index now
		hc.HeaderField{Name: "name3", Value: "value3"}, // this inserts fine
	)
	assert.Nil(t, err)
	t.Logf("Unblocked: %x %x", updateBuf.Bytes(), headerBuf.Bytes())

	expectedUpdates, err := hex.DecodeString("64a874959f85ee3a2d2b3f")
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

	expectedUpdates, err = hex.DecodeString("64a87495af85ee3a2d2b5f")
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

	// While that stream is blocked, another stream won't insert new entries
	// or reference entries that aren't acknowledged.  It will use entries
	// inserted during setup because the acknowledgment of that stream
	// also acknowledges the entries that it used.
	headerBuf.Reset()
	updateBuf.Reset()
	err = encoder.WriteHeaderBlock(&headerBuf, defaultToken+1,
		hc.HeaderField{Name: "name2", Value: "value2"}, // this uses the index
		hc.HeaderField{Name: "name3", Value: "value3"}, // this produces a literal
	)
	assert.Nil(t, err)
	t.Logf("Other Stream: %x %x", updateBuf.Bytes(), headerBuf.Bytes())

	assert.Equal(t, []byte{}, updateBuf.Bytes())
	expectedHeader, err = hex.DecodeString("0200802ca874959f85ee3a2d2b3f")
	assert.Nil(t, err)
	assert.Equal(t, expectedHeader, headerBuf.Bytes())

	checkDynamicTable(t, encoder.Table, &[]dynamicTableEntry{
		{"name4", "value4"},
		{"name3", "value3"},
		{"name2", "value2"},
		{"name1", "value1"},
	})

	// Pretending to reset the stream enables the use of those entries.
	encoder.AcknowledgeReset(defaultToken)

	headerBuf.Reset()
	updateBuf.Reset()
	err = encoder.WriteHeaderBlock(&headerBuf, defaultToken+1,
		hc.HeaderField{Name: "name4", Value: "value4"}, // indexed
		hc.HeaderField{Name: "name5", Value: "value5"}, // blocking
		hc.HeaderField{Name: "name6", Value: "value6"}, // causes eviction
	)
	assert.Nil(t, err)
	t.Logf("After Cancel: %x %x", updateBuf.Bytes(), headerBuf.Bytes())

	expectedUpdates, err = hex.DecodeString("64a87495bf85ee3a2d2b7f64a87495cf85ee3a2d2b9f")
	assert.Nil(t, err)
	assert.Equal(t, expectedUpdates, updateBuf.Bytes())
	expectedHeader, err = hex.DecodeString("0600828180")
	assert.Nil(t, err)
	assert.Equal(t, expectedHeader, headerBuf.Bytes())

	checkDynamicTable(t, encoder.Table, &[]dynamicTableEntry{
		{"name6", "value6"},
		{"name5", "value5"},
		{"name4", "value4"},
		{"name3", "value3"},
		{"name2", "value2"},
	})
}

// Use a name reference and ensure that it can't be evicted.
func TestQpackNameReference(t *testing.T) {
	var updateBuf bytes.Buffer
	encoder := hc.NewQpackEncoder(&updateBuf, 150, 150)
	setupEncoder(t, encoder, &updateBuf)

	var headerBuf bytes.Buffer
	err := encoder.WriteHeaderBlock(&headerBuf, defaultToken,
		hc.HeaderField{Name: "name1", Value: "value9"})
	assert.Nil(t, err)
	t.Logf("Name Reference: %x %x", updateBuf.Bytes(), headerBuf.Bytes())

	// 81 is an insert with a name reference.
	expectedUpdates, err := hex.DecodeString("8185ee3a2d2bff")
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
	encoder := hc.NewQpackEncoder(&updateBuf, 100, 100)
	setupEncoder(t, encoder, &updateBuf)

	// Block new table insertions for this key.
	encoder.SetIndexPreference("name1", false)
	var headerBuf bytes.Buffer
	err := encoder.WriteHeaderBlock(&headerBuf, defaultToken,
		hc.HeaderField{Name: "name1", Value: "value9"})
	assert.Nil(t, err)
	t.Logf("Non-Indexed Name Reference: [%x] %x", updateBuf.Bytes(), headerBuf.Bytes())

	assert.Equal(t, 0, updateBuf.Len())

	expectedHeader, err := hex.DecodeString("01004085ee3a2d2bff")
	assert.Nil(t, err)
	assert.Equal(t, expectedHeader, headerBuf.Bytes())

	checkDynamicTable(t, encoder.Table, &[]dynamicTableEntry{
		{"name2", "value2"},
		{"name1", "value1"},
	})

	// Even after acknowledging the header block from setup, the reference to the
	// initial name1 entry remains outstanding and blocks eviction.
	assert.Nil(t, encoder.AcknowledgeHeader(setupToken))
	assertQpackTableFull(t, encoder)
}

type ackCheckType byte

const (
	ackCheckTss            = ackCheckType(iota)
	ackCheckHeaderBlockAck = ackCheckType(iota)
	ackCheckDone           = ackCheckType(iota)
)

// ackCheckerMsg is used by ackChecker (below), internally
type ackCheckerMsg struct {
	t    ackCheckType
	v    uint64
	done chan struct{}
}

// ackChecker checks acknowledgements on a separate goroutine.
// This is necessary because a single operation might generate multiple
// acknowledgments and we don't want that operation to block.  This isn't
// a problem if the acknowledgements are being written to a socket, but
// using io.Pipe as this does, writing will block until the acknowledgment
// is read.  That causes the test to back up, so this sets up to expect
// certain acknowledgments (with ExpectTss and ExpectHeaderBlockAck) and
// it reads proactively.
type ackChecker struct {
	t *testing.T
	io.Writer
	r *hc.Reader

	cond         *sync.Cond
	headerBlocks map[uint64]bool
	base         int

	done chan struct{}
}

func newAckChecker(t *testing.T) *ackChecker {
	r, w := io.Pipe()
	ac := &ackChecker{
		t:            t,
		Writer:       w,
		r:            hc.NewReader(r),
		cond:         sync.NewCond(&sync.Mutex{}),
		headerBlocks: make(map[uint64]bool),
		base:         0,
		done:         make(chan struct{}),
	}
	go ac.read()
	return ac
}

func (ac *ackChecker) readHeaderBlock() {
	token, err := ac.r.ReadInt(7)
	assert.Nil(ac.t, err)

	ac.cond.L.Lock()
	defer ac.cond.L.Unlock()
	ac.headerBlocks[token] = true
	ac.cond.Broadcast()
}

func (ac *ackChecker) readTableStateSync() {
	b, err := ac.r.ReadBit()
	assert.Nil(ac.t, err)
	// 0b00 == Table State Synchronize and we support nothing else.
	assert.Equal(ac.t, byte(0), b)

	incr, err := ac.r.ReadInt(6)
	assert.Nil(ac.t, err)

	ac.cond.L.Lock()
	defer ac.cond.L.Unlock()
	ac.base += int(incr)
	ac.cond.Broadcast()
}

func (ac *ackChecker) isDone() bool {
	select {
	case <-ac.done:
		return true
	default:
		return false
	}
}

func (ac *ackChecker) read() {
	for !ac.isDone() {
		b, err := ac.r.ReadBit()
		assert.Nil(ac.t, err)
		if b == 1 {
			ac.readHeaderBlock()
		} else {
			ac.readTableStateSync()
		}
	}
}

func (ac *ackChecker) Close() error {
	close(ac.done)
	return nil
}

// wait wraps the condition variable Wait() function
// and checks that this is still running.
func (ac *ackChecker) wait() {
	if ac.isDone() {
		assert.True(ac.t, false, "stopped")
	}
	ac.cond.Wait()
}

// WaitForBase blocks until acknowledgments for the table increment to |base|.
func (ac *ackChecker) WaitForBase(base int) {
	ac.cond.L.Lock()
	defer ac.cond.L.Unlock()
	for ac.base < base {
		ac.wait()
	}
}

// WaitForHeaderBlock blocks until the given header block is acknowledged.
// If the header block doesn't need acknowledgment, exit early.
func (ac *ackChecker) WaitForHeaderBlock(token uint64, encoded []byte) {
	if !ac.needsAcknowledgment(encoded) {
		return
	}
	ac.cond.L.Lock()
	defer ac.cond.L.Unlock()
	for !ac.headerBlocks[token] {
		ac.wait()
	}
}

// needsAcknowledgment returns true of the largest reference is greater than 0.
// We only need to check first octet of the encoded header block to learn this.
func (ac *ackChecker) needsAcknowledgment(encoded []byte) bool {
	return encoded[0] > 0
}

func TestQpackDecoderOrdered(t *testing.T) {
	var ackChecker *ackChecker
	var decoder *hc.QpackDecoder
	var token uint64

	for _, tc := range testCases {
		if tc.resetTable {
			t.Log("Reset table")
			if decoder != nil {
				// The ackChecker gets closed by the decoder.
				decoder.Close()
			}

			ackChecker = newAckChecker(t)
			decoder = hc.NewQpackDecoder(ackChecker, 256)
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

			assert.Equal(t, tc.qpackTable.base, decoder.Table.Base())
			ackChecker.WaitForBase(tc.qpackTable.base)
		}

		var dynamicTable = &tc.hpackTable
		if tc.qpackTable.entries != nil {
			dynamicTable = tc.qpackTable.entries
		}
		checkDynamicTable(t, decoder.Table, dynamicTable)

		t.Logf("Header: %v", tc.qpackHeader)
		encoded, err := hex.DecodeString(tc.qpackHeader)
		assert.Nil(t, err)
		headers, err := decoder.ReadHeaderBlock(bytes.NewReader(encoded), token)
		assert.Nil(t, err)
		assert.Equal(t, tc.headers, headers)
		ackChecker.WaitForHeaderBlock(token, encoded)

		token++
	}
	decoder.Close()
}

// notifyingReader provides a signal when the first octet is read.
type notifyingReader struct {
	reader io.Reader
	signal *sync.Cond
	done   chan struct{}
}

func newNotifyingReader(p []byte) *notifyingReader {
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
	var ackChecker *ackChecker
	var decoder *hc.QpackDecoder
	var controlWriter io.WriteCloser
	var controlReader io.Reader
	controlDone := make(chan struct{})
	headerDone := new(sync.WaitGroup)

	cleanup := func() {
		// Wait for headers to be done, so that acknowledgements are read.
		// If you don't wait before closing, the decoder will choke.
		if batchRead {
			headerDone.Wait()
		}
		decoder.Close() // This closes the ackChecker
		controlWriter.Close()
		<-controlDone
	}

	for i, tc := range testData {
		// The first test always sets resetTable to true.
		if tc.resetTable {
			if decoder != nil {
				cleanup()
			}
			ackChecker = newAckChecker(t)
			decoder = hc.NewQpackDecoder(ackChecker, 256)
			decoder.SetAckDelay(time.Second)
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
		nr := newNotifyingReader(headerBytes)

		go func(i uint64, tc testCase, r io.Reader) {
			defer headerDone.Done()
			headers, err := decoder.ReadHeaderBlock(r, i)
			assert.Nil(t, err)
			ackChecker.WaitForHeaderBlock(i, headerBytes)
			assert.Equal(t, tc.headers, headers)
		}(uint64(i), tc, nr)

		// After setting up the header block to decode, dispense table updates.
		if len(tc.qpackUpdates) > 0 {
			// Wait for the header block reader to take a byte before giving out any
			// table updates.
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
	cleanup()
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
				{Name: ":method", Value: "GET"},
				{Name: ":scheme", Value: "http"},
				{Name: ":path", Value: "/"},
				{Name: ":authority", Value: "www.example.com"},
			},
			qpackUpdates: "", // updates for this header block ...
			qpackHeader:  "0100d1d6c180",
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
			// ... are moved to this one
			qpackUpdates: "c00f7777772e6578616d706c652e636f6d",
			qpackHeader:  "0100d1d6c180e7",
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
			qpackHeader:  "0200d98180",
		},
		{
			resetTable: false,
			headers: []hc.HeaderField{
				{Name: ":status", Value: "307"},
				{Name: "cache-control", Value: "private"},
				{Name: "location", Value: "https://www.example.com"},
			},
			qpackUpdates:
			// updates for the first header block:
			"e40770726976617465cc1768747470733a2f2f777777" +
				"2e6578616d706c652e636f6d" +
				// updates for the second header block:
				"d803333037" + "02", // 02 == duplicate "cache-control"
			qpackHeader: "0400818082",
		},
	})
}

// TestSingleRecordOverflow inserts into a table that is too small for
// even a single record to fit.
func TestSingleRecordOverflow(t *testing.T) {
	ackChecker := newAckChecker(t)
	decoder := hc.NewQpackDecoder(ackChecker, 20)
	defer decoder.Close()
	updates, err := hex.DecodeString("64a874943f85ee3a2d287f")
	assert.Nil(t, err)
	err = decoder.ReadTableUpdates(bytes.NewReader(updates))
	assert.Equal(t, err, hc.ErrTableOverflow)
}
