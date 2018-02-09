package hc

import (
	"container/list"
	"errors"
	"fmt"
	"io"
	"sync"
)

// TODO increase the overhead to something more than 32 to account for the need to store the base.
const qcramOverhead = TableCapacity(32)

// ErrTableUpdateInHeaderBlock shouldn't exist, but this is an early version of QCRAM.
var ErrTableUpdateInHeaderBlock = errors.New("header table update in header block")

// ErrHeaderInTableUpdate shouldn't exist, but this is an early version of QCRAM.
var ErrHeaderInTableUpdate = errors.New("header emission in table update")

func setCapacity(table *Table, c TableCapacity) {
	if table.Base() > 0 {
		panic("SetCapacity called when table isn't empty")
	}
	table.SetCapacity(c)
}

// qcramEntry is an entry in the QCRAM table.
type qcramEntry struct {
	BasicDynamicEntry
}

func (e *qcramEntry) Size() TableCapacity {
	return qcramOverhead + TableCapacity(len(e.Name())+len(e.Value()))
}

// QcramDecoder is the top-level class for header decompression.
// This is intended to be concurrency-safe for reading of header blocks
// (ReadHeaderBlock), but the reading of table updates (ReadTableChanges) can
// only run on one thread at a time.
type QcramDecoder struct {
	decoderCommon
	// This is used to notify any waiting readers that new table entries are
	// availa	ble.
	insertCondition *sync.Cond
}

func makeQcramEntry(name string, value string) DynamicEntry {
	return &qcramEntry{BasicDynamicEntry{name, value, 0}}
}

// NewQcramDecoder makes and sets up a QcramDecoder.
func NewQcramDecoder(c TableCapacity) *QcramDecoder {
	decoder := new(QcramDecoder)
	decoder.insertCondition = sync.NewCond(new(sync.Mutex))
	setCapacity(&decoder.Table, c)
	return decoder
}

// Insert changes and notify any waiting readers.
func (decoder *QcramDecoder) insert(name string, value string) {
	fmt.Println("insert", name, value)
	entry := makeQcramEntry(name, value)
	decoder.Table.Insert(entry, nil)
	fmt.Println("broadcast", decoder.Table.Base())
	decoder.insertCondition.Broadcast()
}

func (decoder *QcramDecoder) readIncremental(reader *Reader, base int) error {
	name, value, err := decoder.readNameValue(reader, 6, base)
	if err != nil {
		return err
	}
	decoder.insert(name, value)
	return nil
}

func (decoder *QcramDecoder) readDuplicate(reader *Reader, base int) error {
	index, err := reader.ReadIndex(5)
	if err != nil {
		return err
	}
	entry := decoder.Table.GetWithBase(index, base)
	if entry == nil {
		return ErrIndexError
	}
	decoder.insert(entry.Name(), entry.Value())
	return nil
}

// ReadTableUpdates reads inserts to the table.
func (decoder *QcramDecoder) ReadTableUpdates(r io.Reader) error {
	reader := NewReader(r)

	base := decoder.Table.Base()
	for {
		b, err := reader.ReadBits(2)
		if err == io.EOF {
			break // Success
		}
		if err != nil {
			return err
		}

		if b > 1 {
			return ErrHeaderInTableUpdate
		}
		if b == 1 {
			err = decoder.readIncremental(reader, base)
			if err != nil {
				return err
			}
			continue
		}
		b, err = reader.ReadBits(1)
		if err != nil {
			return err
		}
		if b != 1 {
			return ErrHeaderInTableUpdate
		}
		err = decoder.readDuplicate(reader, base)
		if err != nil {
			return err
		}
	}
	return nil
}

func (decoder *QcramDecoder) readIndexed(reader *Reader, base int) (*HeaderField, error) {
	index, err := reader.ReadIndex(7)
	if err != nil {
		return nil, err
	}
	entry := decoder.Table.GetWithBase(index, base)
	if entry == nil {
		return nil, ErrIndexError
	}
	return &HeaderField{entry.Name(), entry.Value(), false}, nil
}

func (decoder *QcramDecoder) readLiteral(reader *Reader, base int) (*HeaderField, error) {
	neverIndex, err := reader.ReadBits(3)
	if err != nil {
		return nil, err
	}
	if neverIndex > 1 {
		return nil, ErrTableUpdateInHeaderBlock
	}

	name, value, err := decoder.readNameValue(reader, 4, base)
	if err != nil {
		return nil, err
	}
	return &HeaderField{name, value, neverIndex == 1}, nil
}

func (decoder *QcramDecoder) readBase(reader *Reader) (int, error) {
	base, err := reader.ReadIndex(8)
	if err != nil {
		return 0, err
	}

	for decoder.Table.Base() < base {
		decoder.insertCondition.L.Lock()
		decoder.insertCondition.Wait()
		decoder.insertCondition.L.Unlock()
	}
	fmt.Println("got base", base)
	return base, nil
}

// Sanity-check header ordering.
func validatePseudoHeaders(headers []HeaderField) error {
	pseudo := true
	for _, h := range headers {
		if h.Name[0] == ':' {
			if !pseudo {
				return ErrPseudoHeaderOrdering
			}
		} else {
			pseudo = false
		}
	}
	return nil
}

// ReadHeaderBlock decodes header fields as they arrive.
func (decoder *QcramDecoder) ReadHeaderBlock(r io.Reader) ([]HeaderField, error) {
	reader := NewReader(r)
	base, err := decoder.readBase(reader)
	if err != nil {
		return nil, err
	}

	headers := []HeaderField{}
	for {
		b, err := reader.ReadBits(1)
		if err == io.EOF {
			break // Success!
		}
		if err != nil {
			return nil, err
		}

		if b == 1 {
			h, err := decoder.readIndexed(reader, base)
			if err != nil {
				return nil, err
			}
			headers = append(headers, *h)
			continue
		}

		h, err := decoder.readLiteral(reader, base)
		if err != nil {
			return nil, err
		}
		headers = append(headers, *h)
	}

	err = validatePseudoHeaders(headers)
	if err != nil {
		return nil, err
	}
	return headers, nil
}

type qcramEncoderEntry struct {
	qcramEntry
	uses list.List
}

func makeQcramEncoderEntry(name string, value string) DynamicEntry {
	return &qcramEncoderEntry{qcramEntry{BasicDynamicEntry{name, value, 0}}, list.List{}}
}

func (qe *qcramEncoderEntry) addUse(token interface{}) {
	qe.uses.PushBack(token)
}

func (qe *qcramEncoderEntry) removeUse(token interface{}) {
	for e := qe.uses.Front(); e != nil; e = e.Next() {
		if e.Value == token {
			qe.uses.Remove(e)
			return
		}
	}
}

func (qe *qcramEncoderEntry) inUse() bool {
	return qe.uses.Len() > 0
}

// The number of referenceable entries in the dynamic table.
type referenceableEntries struct {
	// The amount of table capacity we will actively use.
	margin TableCapacity
	// The number of entries we can use right now.
	count int
	// The size of those usable entries.
	size TableCapacity
}

func (ref *referenceableEntries) added(dynamic []DynamicEntry, increase TableCapacity) {
	updatedSize := ref.size + increase
	i := ref.count + 1
	for updatedSize > ref.margin {
		i--
		updatedSize -= dynamic[i].Size()
	}
	ref.count = i
	ref.size = updatedSize
}

func (ref *referenceableEntries) removed(reduction TableCapacity) {
	ref.count--
	ref.size -= reduction
}

// This is used by the writer to track which table entries are needed to write
// out a particular header field.
type qcramWriterState struct {
	headers     []HeaderField
	matches     []Entry
	nameMatches []Entry

	// Track the largest and smallest base that we use. Largest so that we can set
	// the base on the header block; smallest so that we can prevent that from
	// being evicted.
	largestBase   int
	smallestBase  int
	referenceable *referenceableEntries

	token interface{}
}

func (state *qcramWriterState) init(headers []HeaderField, referenceable *referenceableEntries, token interface{}) {
	state.headers = headers
	state.matches = make([]Entry, len(headers))
	state.nameMatches = make([]Entry, len(headers))
	state.smallestBase = int(^uint(0) >> 1)
	state.referenceable = referenceable
	state.token = token
}

func (state *qcramWriterState) updateBase(e Entry, match bool) {
	if e == nil {
		return
	}
	dyn, ok := e.(DynamicEntry)
	if !ok {
		return
	}
	if dyn.Base() > state.largestBase {
		state.largestBase = dyn.Base()
	}
	if match && dyn.Base() < state.smallestBase {
		state.smallestBase = dyn.Base()
	}
}

func (state *qcramWriterState) CanEvict(e DynamicEntry) bool {
	if e.Base() == state.smallestBase {
		return false
	}
	qe := e.(*qcramEncoderEntry)
	if qe.inUse() {
		return false
	}
	state.referenceable.removed(qe.Size())
	return true
}

func (state *qcramWriterState) addUse(i int) {
	e := state.matches[i]
	if e == nil {
		e = state.nameMatches[i]
	}
	if e == nil {
		return
	}
	qe, ok := e.(*qcramEncoderEntry)
	if ok {
		qe.addUse(state.token)
	}
}

// QcramEncoder performs header compression using QCRAM. This is not a
// thread-safe object, all writes need to be serialized.
type QcramEncoder struct {
	encoderCommon
	referenceable referenceableEntries
}

// NewQcramEncoder creates a new QcramEncoder and sets it up.
// `capacity` is the capacity of the table. `margin` is the amount of capacity
// that the encoder will actively use. Dynamic table entries inside of `margin`
// will be referenced, those outside will not be. Set `margin` to a value that
// is less than capacity. Setting `margin` too low can cause churn, where the
// encoder will duplicate entries rather than reference them.
func NewQcramEncoder(capacity TableCapacity, margin TableCapacity) *QcramEncoder {
	encoder := new(QcramEncoder)
	setCapacity(&encoder.Table, capacity)
	encoder.referenceable.margin = margin
	return encoder
}

// writeDuplicate duplicates the indicated entry.
func (encoder *QcramEncoder) writeDuplicate(writer *Writer, entry DynamicEntry, state *qcramWriterState, i int, base int) error {
	copy := makeQcramEncoderEntry(entry.Name(), entry.Value())
	inserted := encoder.Table.Insert(copy, state)
	if !inserted {
		// Leaving h unmodified causes a literal to be written.
		return nil
	}
	encoder.referenceable.added(encoder.Table.dynamic, copy.Size())

	err := writer.WriteBits(1, 3)
	if err != nil {
		return err
	}
	err = writer.WriteInt(uint64(entry.Index(base)), 5)
	if err != nil {
		return err
	}

	state.matches[i] = copy
	state.updateBase(copy, true)
	return nil
}

// writeInsert writes the entry at state.xxx[i] to the control stream.
// Note that nameMatch is only used for this insertion.
func (encoder *QcramEncoder) writeInsert(writer *Writer, state *qcramWriterState, i int,
	nameMatch Entry, base int) error {
	h := state.headers[i]
	entry := makeQcramEncoderEntry(h.Name, h.Value)
	inserted := encoder.Table.Insert(entry, state)
	if !inserted {
		// Leaving h unmodified causes a literal to be written.
		return nil
	}
	encoder.referenceable.added(encoder.Table.dynamic, entry.Size())

	err := writer.WriteBits(1, 2)
	if err != nil {
		return err
	}
	err = encoder.writeNameValue(writer, h, nameMatch, 6, base)
	if err != nil {
		return err
	}

	state.matches[i] = entry
	state.updateBase(entry, true)
	return nil
}

// writeTableChanges writes out the changes to the header table. It returns the
// largest value of base that can be used for this to work.
func (encoder *QcramEncoder) writeTableChanges(controlWriter io.Writer, state *qcramWriterState) error {
	w := NewWriter(controlWriter)

	base := encoder.Table.Base()

	for i := range state.headers {
		// Make sure to write into the slice rather than use a copy of each header.
		h := state.headers[i]
		if h.Sensitive {
			continue
		}
		match, nameMatch := encoder.Table.LookupLimited(h.Name, h.Value, encoder.referenceable.count)
		if match != nil {
			state.matches[i] = match
			state.updateBase(match, true)
			continue
		}

		// Now look for a duplicate, and maybe a name match that we can use for
		// insertion if duplication isn't possible. Don't use either of these when
		// encoding the header block to avoid holding down references to entries that
		// we might want to evict.
		var insertNameMatch Entry
		duplicate, insertNameMatch := encoder.Table.LookupDynamic(h.Name, h.Value, encoder.referenceable.count)
		if duplicate != nil {
			err := encoder.writeDuplicate(w, duplicate, state, i, base)
			if err != nil {
				return err
			}
			continue
		}

		if nameMatch != nil {
			insertNameMatch = nameMatch
			state.nameMatches[i] = nameMatch
		}
		if encoder.shouldIndex(h) {
			err := encoder.writeInsert(w, state, i, insertNameMatch, base)
			if err != nil {
				return err
			}
		} else {
			state.updateBase(nameMatch, false)
		}
	}
	return nil
}

func (encoder *QcramEncoder) writeIndexed(writer *Writer, state *qcramWriterState, i int) error {
	err := writer.WriteBit(1)
	if err != nil {
		return err
	}
	state.addUse(i)
	return writer.WriteInt(uint64(state.matches[i].Index(state.largestBase)), 7)
}

func (encoder QcramEncoder) writeLiteral(writer *Writer, state *qcramWriterState, i int) error {
	h := state.headers[i]
	var code uint64
	if h.Sensitive {
		code = 1
	}
	err := writer.WriteBits(code, 4)
	if err != nil {
		return err
	}

	state.addUse(i)
	return encoder.writeNameValue(writer, h, state.nameMatches[i], 4, state.largestBase)
}

func (encoder *QcramEncoder) writeHeaderBlock(headerWriter io.Writer, state *qcramWriterState) error {
	w := NewWriter(headerWriter)
	err := w.WriteInt(uint64(state.largestBase), 8)
	if err != nil {
		return err
	}

	for i := range state.headers {
		var err error
		if state.matches[i] != nil {
			err = encoder.writeIndexed(w, state, i)
		} else {
			err = encoder.writeLiteral(w, state, i)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// clearEvictedMatches ensures that we don't retain any references to entries
// that were evicted while inserting header fields.
func (encoder *QcramEncoder) clearEvictedMatches(entries []Entry) {
	base := encoder.Table.Base()
	lastIndex := encoder.Table.LastIndex(base)
	for i := range entries {
		if entries[i] != nil && entries[i].Index(base) > lastIndex {
			entries[i] = nil
		}
	}
}

// WriteHeaderBlock writes out a header block.  controlWriter is the control stream writer
// `token` is a token that is used to identify this header block. The caller
// needs to pick a token that is unique among the tokens that are currently
// unacknowledged. Using the same token twice without first acknowledging it can
// result in errors.
func (encoder *QcramEncoder) WriteHeaderBlock(controlWriter io.Writer, headerWriter io.Writer,
	token interface{}, headers ...HeaderField) error {
	err := validatePseudoHeaders(headers)
	if err != nil {
		return err
	}

	var state qcramWriterState
	state.init(headers, &encoder.referenceable, token)
	err = encoder.writeTableChanges(controlWriter, &state)
	if err != nil {
		return err
	}

	encoder.clearEvictedMatches(state.matches)
	encoder.clearEvictedMatches(state.nameMatches)

	return encoder.writeHeaderBlock(headerWriter, &state)
}

// Acknowledge is called when a header block has been acknowledged by the peer.
// This allows dynamic table entries to be evicted as necessary on the next
// call.
func (encoder *QcramEncoder) Acknowledge(token interface{}) {
	for _, e := range encoder.Table.dynamic {
		qe := e.(*qcramEncoderEntry)
		qe.removeUse(token)
	}
}
