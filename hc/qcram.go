package hc

import (
	"errors"
	"io"
	"sync"
)

// ErrTableUpdateInHeaderBlock shouldn't exist, but this is an early version of QCRAM.
var ErrTableUpdateInHeaderBlock = errors.New("header table update in header block")

// ErrHeaderInTableUpdate shouldn't exist, but this is an early version of QCRAM.
var ErrHeaderInTableUpdate = errors.New("header emission in table update")

// QcramDecoder is the top-level class for header decompression.
// This is intended to be concurrency-safe for reading of header blocks
// (ReadHeaderBlock), but the reading of table updates (ReadTableChanges) can
// only run on one thread at a time.
type QcramDecoder struct {
	decoderCommon
	table *QcramDecoderTable
}

// NewQcramDecoder makes and sets up a QcramDecoder.
func NewQcramDecoder(capacity TableCapacity) *QcramDecoder {
	decoder := new(QcramDecoder)
	decoder.table = NewQcramDecoderTable(capacity)
	decoder.Table = decoder.table
	return decoder
}

func (decoder *QcramDecoder) readIncremental(reader *Reader, base int) error {
	name, value, err := decoder.readNameValue(reader, 6, decoder.Table.Base())
	if err != nil {
		return err
	}
	decoder.table.Insert(name, value, nil)
	return nil
}

func (decoder *QcramDecoder) readDuplicate(reader *Reader, base int) error {
	index, err := reader.ReadIndex(5)
	if err != nil {
		return err
	}
	entry := decoder.Table.Get(index)
	if entry == nil {
		return ErrIndexError
	}
	decoder.table.Insert(entry.Name(), entry.Value(), nil)
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

	decoder.table.WaitForBase(base)
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

	err = validatePseudoHeaders(headers)
	if err != nil {
		return nil, err
	}
	return headers, nil
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

	token interface{}
}

func (state *qcramWriterState) init(headers []HeaderField, token interface{}) {
	state.headers = headers
	state.matches = make([]Entry, len(headers))
	state.nameMatches = make([]Entry, len(headers))
	state.smallestBase = int(^uint(0) >> 1)
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
	return e.Base() != state.smallestBase
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

// QcramEncoder performs header compression using QCRAM.
type QcramEncoder struct {
	encoderCommon
	table *QcramEncoderTable
	mutex sync.RWMutex
}

// NewQcramEncoder creates a new QcramEncoder and sets it up.
// `capacity` is the capacity of the table. `margin` is the amount of capacity
// that the encoder will actively use. Dynamic table entries inside of `margin`
// will be referenced, those outside will not be. Set `margin` to a value that
// is less than capacity. Setting `margin` too low can cause churn, where the
// encoder will duplicate entries rather than reference them.
func NewQcramEncoder(capacity TableCapacity, margin TableCapacity) *QcramEncoder {
	encoder := new(QcramEncoder)
	encoder.table = NewQcramEncoderTable(capacity, margin)
	encoder.Table = encoder.table
	return encoder
}

// writeDuplicate duplicates the indicated entry.
func (encoder *QcramEncoder) writeDuplicate(writer *Writer, entry DynamicEntry, state *qcramWriterState, i int) error {
	base := encoder.Table.Base()
	inserted := encoder.Table.Insert(entry.Name(), entry.Value(), state)
	if inserted == nil {
		// Leaving h unmodified causes a literal to be written.
		return nil
	}

	err := writer.WriteBits(1, 3)
	if err != nil {
		return err
	}
	err = writer.WriteInt(uint64(entry.Index(base)), 5)
	if err != nil {
		return err
	}

	state.matches[i] = inserted
	state.updateBase(inserted, true)
	return nil
}

// writeInsert writes the entry at state.xxx[i] to the control stream.
// Note that nameMatch is only used for this insertion.
func (encoder *QcramEncoder) writeInsert(writer *Writer, state *qcramWriterState, i int,
	nameMatch Entry) error {
	h := state.headers[i]
	base := encoder.Table.Base()
	inserted := encoder.Table.Insert(h.Name, h.Value, state)
	if inserted == nil {
		// Leaving h unmodified causes a literal to be written.
		return nil
	}

	err := writer.WriteBits(1, 2)
	if err != nil {
		return err
	}
	err = encoder.writeNameValue(writer, h, nameMatch, 6, base)
	if err != nil {
		return err
	}

	state.matches[i] = inserted
	state.updateBase(inserted, true)
	return nil
}

// writeTableChanges writes out the changes to the header table. It returns the
// largest value of base that can be used for this to work.
func (encoder *QcramEncoder) writeTableChanges(controlWriter io.Writer, state *qcramWriterState) error {
	w := NewWriter(controlWriter)

	// Only one goroutine can update the table at once.
	defer encoder.mutex.Unlock()
	encoder.mutex.Lock()

	for i := range state.headers {
		// Make sure to write into the slice rather than use a copy of each header.
		h := state.headers[i]
		if h.Sensitive {
			continue
		}
		match, nameMatch := encoder.table.LookupReferenceable(h.Name, h.Value)
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
		duplicate, insertNameMatch := encoder.table.LookupExtra(h.Name, h.Value)
		if duplicate != nil {
			err := encoder.writeDuplicate(w, duplicate, state, i)
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
			err := encoder.writeInsert(w, state, i, insertNameMatch)
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

func (encoder *QcramEncoder) writeLiteral(writer *Writer, state *qcramWriterState, i int) error {
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

func (encoder *QcramEncoder) writeHeaderBlock(headerWriter io.Writer, state *qcramWriterState) (int64, error) {
	w := NewWriter(headerWriter)
	err := w.WriteInt(uint64(state.largestBase), 8)
	if err != nil {
		return 0, err
	}

	// Make sure that we don't read over writes.
	defer encoder.mutex.RUnlock()
	encoder.mutex.RLock()

	for i := range state.headers {
		var err error
		if state.matches[i] != nil {
			err = encoder.writeIndexed(w, state, i)
		} else {
			err = encoder.writeLiteral(w, state, i)
		}
		if err != nil {
			return 0, err
		}
	}
	return w.Written(), nil
}

// WriteHeaderBlock writes out a header block.  controlWriter is the control stream writer
// `token` is a token that is used to identify this header block. The caller
// needs to pick a token that is unique among the tokens that are currently
// unacknowledged. Using the same token twice without first acknowledging it can
// result in errors.
func (encoder *QcramEncoder) WriteHeaderBlock(controlWriter io.Writer, headerWriter io.Writer,
	token interface{}, headers ...HeaderField) (int64, error) {
	err := validatePseudoHeaders(headers)
	if err != nil {
		return 0, err
	}

	var state qcramWriterState
	state.init(headers, token)
	err = encoder.writeTableChanges(controlWriter, &state)
	if err != nil {
		return 0, err
	}

	return encoder.writeHeaderBlock(headerWriter, &state)
}

// Acknowledge is called when a header block has been acknowledged by the peer.
// This allows dynamic table entries to be evicted as necessary on the next
// call.
func (encoder *QcramEncoder) Acknowledge(token interface{}) {
	defer encoder.mutex.Unlock()
	encoder.mutex.Lock()
	encoder.table.Acknowledge(token)
}

// SetCapacity sets the table capacity. This panics if it is called when the
// capacity has already been set to a non-zero value.
func (encoder *QcramEncoder) SetCapacity(c TableCapacity) {
	defer encoder.mutex.Unlock()
	encoder.mutex.Lock()
	encoder.table.SetCapacity(c)
}
