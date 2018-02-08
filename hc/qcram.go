package hc

import (
	"container/list"
	"errors"
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
type QcramDecoder struct {
	decoderCommon
	// This is used to notify any waiting readers that new table entries are
	// available.
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
	entry := makeQcramEntry(name, value)
	decoder.Table.Insert(entry, nil)
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

// ReadTableChanges reads inserts to the table.
func (decoder *QcramDecoder) ReadTableChanges(r io.Reader) error {
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
		decoder.insertCondition.Wait()
	}
	return base, nil
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

	// Sanity-check header ordering.
	pseudo := true
	for _, h := range headers {
		if h.Name[0] == ':' {
			if !pseudo {
				return nil, ErrPseudoHeaderOrdering
			}
		} else {
			pseudo = false
		}
	}
	return headers, nil
}

type qcramEncoderEntry struct {
	qcramEntry
	unacknowledged list.List
}

func makeQcramEncoderEntry(name string, value string) DynamicEntry {
	return &qcramEncoderEntry{qcramEntry{BasicDynamicEntry{name, value, 0}}, list.List{}}
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
	largestBase  int
	smallestBase int
}

func (state *qcramWriterState) init(headers []HeaderField) {
	state.headers = headers
	state.matches = make([]Entry, len(headers))
	state.nameMatches = make([]Entry, len(headers))
	state.smallestBase = int(^uint(0) >> 1)
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
	dyn := e.(*qcramEncoderEntry)
	return dyn.unacknowledged.Len() == 0
}

// QcramEncoder is the top-level class for header compression.
type QcramEncoder struct {
	encoderCommon
}

// NewQcramEncoder creates a new QcramEncoder and sets it up.
func NewQcramEncoder(c TableCapacity) *QcramEncoder {
	encoder := new(QcramEncoder)
	setCapacity(&encoder.Table, c)
	return encoder
}

// writeInsert writes the entry at state.xxx[i] to the control stream.
func (encoder *QcramEncoder) writeInsert(writer *Writer, state *qcramWriterState, i int, base int) error {
	h := state.headers[i]
	entry := makeQcramEncoderEntry(h.Name, h.Value)
	inserted := encoder.Table.Insert(entry, state)
	if !inserted {
		// Leaving h unmodified causes a literal to be written.
		return nil
	}

	err := writer.WriteBits(1, 2)
	if err != nil {
		return err
	}

	err = encoder.writeNameValue(writer, h, state.nameMatches[i], 6, base)
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
		m, nm := encoder.Table.Lookup(h.Name, h.Value)
		// TODO decide what needs duplicating based on an eviction threshold.

		// TODO we should stop inserting and duplicating if our first insert is at
		// risk of eviction. Unlike HPACK we can't roll the entire table for every
		// header block.
		if m != nil {
			state.matches[i] = m
			state.updateBase(m, true)
		} else {
			state.nameMatches[i] = nm
			if encoder.shouldIndex(h) {
				err := encoder.writeInsert(w, state, i, base)
				if err != nil {
					return err
				}
				state.largestBase = encoder.Table.Base()
			} else {
				state.updateBase(nm, false)
			}
		}

	}
	return nil
}

func (encoder *QcramEncoder) writeIndexed(writer *Writer, state *qcramWriterState, i int) error {
	err := writer.WriteBit(1)
	if err != nil {
		return err
	}
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
func (encoder *QcramEncoder) WriteHeaderBlock(controlWriter io.Writer, headerWriter io.Writer, headers ...HeaderField) error {
	err := validatePseudoHeaders(headers)
	if err != nil {
		return err
	}

	var state qcramWriterState
	state.init(headers)
	err = encoder.writeTableChanges(controlWriter, &state)
	if err != nil {
		return err
	}

	encoder.clearEvictedMatches(state.matches)
	encoder.clearEvictedMatches(state.nameMatches)

	return encoder.writeHeaderBlock(headerWriter, &state)
}
