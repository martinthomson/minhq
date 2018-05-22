package hc

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"sync"
)

const intMax = int(^uint(0) >> 1)

// ErrTableUpdateInHeaderBlock shouldn't exist, but this is an early version of QPACK.
var ErrTableUpdateInHeaderBlock = errors.New("header table update in header block")

// ErrHeaderInTableUpdate shouldn't exist, but this is an early version of QPACK.
var ErrHeaderInTableUpdate = errors.New("header emission in table update")

// HasIdentity is used for tracking of outstanding header blocks.
type HasIdentity interface {
	Id() uint64
}

// QpackDecoder is the top-level class for header decompression.
// This is intended to be concurrency-safe for reading of header blocks
// (ReadHeaderBlock), but the reading of table updates (ReadTableChanges) can
// only run on one thread at a time.
type QpackDecoder struct {
	decoderCommon
	table        *QpackDecoderTable
	available    chan<- int
	acknowledged chan<- uint64
}

// NewQpackDecoder makes and sets up a QpackDecoder.
func NewQpackDecoder(aw io.Writer, capacity TableCapacity) *QpackDecoder {
	decoder := new(QpackDecoder)
	decoder.table = NewQpackDecoderTable(capacity)
	decoder.Table = decoder.table
	available := make(chan int)
	decoder.available = available
	acknowledged := make(chan uint64)
	decoder.acknowledged = acknowledged
	go decoder.writeAcknowledgements(aw, available, acknowledged)
	return decoder
}

func (decoder *QpackDecoder) writeAcknowledgements(aw io.Writer, entries <-chan int, acknowledged <-chan uint64) {
	w := NewWriter(aw)
	for {
		var v uint64
		var err error
		select {
		case entry := <-entries:
			err = w.WriteBit(1)
			v = uint64(entry)
		case ack := <-acknowledged:
			err = w.WriteBit(0)
			v = ack
		}
		if err != nil {
			// TODO: close the connection instead of panicking
			panic("unable to write acknowledgment")
			return
		}
		err = w.WriteInt(v, 7)
		if err != nil {
			panic("unable to write acknowledgment")
			return
		}
	}
}

// ServiceUpdates reads from the given reader, updating the header table as needed.
func (decoder *QpackDecoder) ServiceUpdates(hr io.Reader) {
	r := NewReader(hr)
	for {
		blockLen, err := r.ReadInt(8)
		if err != nil {
			// TODO report this error
			return
		}
		block := &io.LimitedReader{R: r, N: int64(blockLen)}
		err = decoder.ReadTableUpdates(block)
		if err != nil {
			// TODO report this error
			return
		}
	}
}

func (decoder *QpackDecoder) readInsertWithNameReference(reader *Reader, base int) error {
	static, err := reader.ReadBit()
	if err != nil {
		return err
	}
	nameIndex, err := reader.ReadIndex(6)
	if err != nil {
		return err
	}
	var nameEntry Entry
	if static != 0 {
		nameEntry = decoder.table.GetStatic(nameIndex)
	} else {
		nameEntry = decoder.table.GetDynamic(nameIndex, base)
	}
	if nameEntry == nil {
		return ErrIndexError
	}
	value, err := reader.ReadString(7)
	if err != nil {
		return err
	}
	decoder.table.Insert(nameEntry.Name(), value, nil)
	return nil
}

func (decoder *QpackDecoder) readInsertWithNameLiteral(reader *Reader, base int) error {
	name, err := reader.ReadString(5)
	if err != nil {
		return err
	}
	value, err := reader.ReadString(7)
	if err != nil {
		return err
	}
	decoder.table.Insert(name, value, nil)
	return nil
}

func (decoder *QpackDecoder) readDuplicate(reader *Reader, base int) error {
	index, err := reader.ReadIndex(5)
	if err != nil {
		return err
	}
	entry := decoder.Table.GetDynamic(index, base)
	if entry == nil {
		return ErrIndexError
	}
	decoder.table.Insert(entry.Name(), entry.Value(), nil)
	return nil
}

func (decoder *QpackDecoder) readDynamicUpdate(reader *Reader) error {
	capacity, err := reader.ReadInt(5)
	if err != nil {
		return err
	}
	decoder.Table.SetCapacity(TableCapacity(capacity))
	return nil
}

// ReadTableUpdates reads a single block of table updates.  If you use ServiceUpdates,
// this function should need to be used at all.
func (decoder *QpackDecoder) ReadTableUpdates(r io.Reader) error {
	reader := NewReader(r)

	for {
		base := decoder.Table.Base()
		b, err := reader.ReadBit()
		if err == io.EOF {
			decoder.available <- base
			break // Success
		}
		if err != nil {
			return err
		}

		if b == 1 {
			err = decoder.readInsertWithNameReference(reader, base)
			if err != nil {
				return err
			}
			continue
		}
		b, err = reader.ReadBit()
		if err != nil {
			return err
		}
		if b == 1 {
			err = decoder.readInsertWithNameLiteral(reader, base)
			if err != nil {
				return err
			}
			continue
		}
		b, err = reader.ReadBit()
		if err != nil {
			return err
		}
		if b == 0 {
			err = decoder.readDuplicate(reader, base)
		} else {
			err = decoder.readDynamicUpdate(reader)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (decoder *QpackDecoder) readIndexed(reader *Reader, base int) (*HeaderField, error) {
	static, err := reader.ReadBit()
	if err != nil {
		return nil, err
	}
	index, err := reader.ReadIndex(6)
	if err != nil {
		return nil, err
	}
	var entry Entry
	if static == 1 {
		entry = decoder.Table.GetStatic(index)
	} else {
		entry = decoder.Table.GetDynamic(index, base)
	}
	if entry == nil {
		return nil, ErrIndexError
	}
	return &HeaderField{entry.Name(), entry.Value(), false}, nil
}

func (decoder *QpackDecoder) readPostBaseIndexed(reader *Reader, base int) (*HeaderField, error) {
	postBase, err := reader.ReadIndex(4)
	if err != nil {
		return nil, err
	}
	entry := decoder.Table.GetDynamic(-1*postBase, base)
	if entry == nil {
		return nil, ErrIndexError
	}
	return &HeaderField{entry.Name(), entry.Value(), false}, nil
}

func (decoder *QpackDecoder) readLiteralWithNameReference(reader *Reader, base int) (*HeaderField, error) {
	neverIndex, err := reader.ReadBit()
	if err != nil {
		return nil, err
	}
	static, err := reader.ReadBit()
	if err != nil {
		return nil, err
	}
	nameIndex, err := reader.ReadIndex(4)
	if err != nil {
		return nil, err
	}
	var nameEntry Entry
	if static == 1 {
		nameEntry = decoder.Table.GetStatic(nameIndex)
	} else {
		nameEntry = decoder.Table.GetDynamic(nameIndex, base)
	}
	if nameEntry == nil {
		return nil, ErrIndexError
	}

	value, err := reader.ReadString(7)
	if err != nil {
		return nil, err
	}
	return &HeaderField{nameEntry.Name(), value, neverIndex == 1}, nil
}

func (decoder *QpackDecoder) readLiteralWithPostBaseNameReference(reader *Reader, base int) (*HeaderField, error) {
	neverIndex, err := reader.ReadBit()
	if err != nil {
		return nil, err
	}
	postBase, err := reader.ReadIndex(3)
	if err != nil {
		return nil, err
	}
	nameEntry := decoder.Table.GetDynamic(-1*postBase, base)
	if nameEntry == nil {
		return nil, ErrIndexError
	}

	value, err := reader.ReadString(7)
	if err != nil {
		return nil, err
	}
	return &HeaderField{nameEntry.Name(), value, neverIndex == 1}, nil
}

func (decoder *QpackDecoder) readLiteralWithNameLiteral(reader *Reader, base int) (*HeaderField, error) {
	neverIndex, err := reader.ReadBit()
	if err != nil {
		return nil, err
	}
	name, err := reader.ReadString(3)
	if err != nil {
		return nil, err
	}
	value, err := reader.ReadString(7)
	if err != nil {
		return nil, err
	}
	return &HeaderField{name, value, neverIndex == 1}, nil
}

// readBase reads the header block header and blocks until the decoder is
// ready to process the remainder of the block.
func (decoder *QpackDecoder) readBase(reader *Reader) (int, error) {
	base, err := reader.ReadIndex(8)
	if err != nil {
		return 0, err
	}
	sign, err := reader.ReadBit()
	if err != nil {
		return 0, err
	}
	delta, err := reader.ReadIndex(7)
	if err != nil {
		return 0, err
	}
	// Sign == 1 means negative.
	largestReference := base + (delta * int(1-2*sign))
	decoder.table.WaitForEntry(largestReference)
	return base, nil
}

// ReadHeaderBlock decodes header fields as they arrive.
func (decoder *QpackDecoder) ReadHeaderBlock(r io.Reader, id uint64) ([]HeaderField, error) {
	reader := NewReader(r)
	base, err := decoder.readBase(reader)
	if err != nil {
		return nil, err
	}

	headers := []HeaderField{}
	for {
		b, err := reader.ReadBit()
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

		b, err = reader.ReadBit()
		if err != nil {
			return nil, err
		}
		if b == 0 {
			h, err := decoder.readLiteralWithNameReference(reader, base)
			if err != nil {
				return nil, err
			}
			headers = append(headers, *h)
			continue
		}

		b, err = reader.ReadBit()
		if err != nil {
			return nil, err
		}
		if b == 1 {
			h, err := decoder.readLiteralWithNameLiteral(reader, base)
			if err != nil {
				return nil, err
			}
			headers = append(headers, *h)
			continue
		}

		b, err = reader.ReadBit()
		if err != nil {
			return nil, err
		}
		var h *HeaderField
		if b == 0 {
			h, err = decoder.readPostBaseIndexed(reader, base)
		} else {
			h, err = decoder.readLiteralWithPostBaseNameReference(reader, base)
		}
		if err != nil {
			return nil, err
		}
		headers = append(headers, *h)
	}

	err = validatePseudoHeaders(headers)
	if err != nil {
		return nil, err
	}
	decoder.acknowledged <- id
	return headers, nil
}

// This is used by the writer to track which table entries are needed to write
// out a particular header field.
type qpackWriterState struct {
	headers     []HeaderField
	matches     []Entry
	nameMatches []Entry

	// Track the largest and smallest base that we use. Largest so that we can set
	// the base on the header block; smallest so that we can prevent that from
	// being evicted.
	largestBase  int
	smallestBase int

	uses *qpackHeaderBlockUsage
}

func (state *qpackWriterState) initHeaders(headers []HeaderField) {
	state.headers = make([]HeaderField, len(headers))
	for i, h := range headers {
		state.headers[i] = HeaderField{strings.ToLower(h.Name), h.Value, h.Sensitive}
	}
	state.matches = make([]Entry, len(headers))
	state.nameMatches = make([]Entry, len(headers))
	state.smallestBase = int(^uint(0) >> 1)
}

func (state *qpackWriterState) updateBase(e Entry, match bool) {
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

func (state *qpackWriterState) CanEvict(e DynamicEntry) bool {
	return e.Base() != state.smallestBase
}

func (state *qpackWriterState) addUse(i int) {
	e := state.matches[i]
	if e == nil {
		e = state.nameMatches[i]
	}
	if e == nil {
		return
	}
	qe, ok := e.(*qpackEncoderEntry)
	if ok {
		state.uses.add(qe)
	}
}

// QpackEncoder performs header compression using QPACK.
type QpackEncoder struct {
	encoderCommon
	table *QpackEncoderTable
	mutex sync.RWMutex

	// updatesWriter is where header table updates are written
	updatesWriter *Writer

	// usage tracks the use of the header table.
	usage qpackUsageTracker

	// maxBlockedStreams is the peer's setting for the number of
	// streams that can be blocked.
	maxBlockedStreams int
	// highestAcknowledged is the highest dynamic table entry base
	// that the peer has acknowledged.
	highestAcknowledged int
	// blockedStreams is the number of streams that are currently
	// potentially blocked.
	blockedStreams int
}

// NewQpackEncoder creates a new QpackEncoder and sets it up.
// `capacity` is the capacity of the table. `margin` is the amount of capacity
// that the encoder will actively use. Dynamic table entries inside of `margin`
// will be referenced, those outside will not be. Set `margin` to a value that
// is less than capacity. Setting `margin` too low can cause churn, where the
// encoder will duplicate entries rather than reference them.
func NewQpackEncoder(hw io.Writer, capacity TableCapacity, margin TableCapacity) *QpackEncoder {
	encoder := new(QpackEncoder)
	encoder.table = NewQpackEncoderTable(capacity, margin)
	encoder.Table = encoder.table
	encoder.updatesWriter = NewWriter(hw)
	encoder.usage = make(map[uint64]*qpackStreamUsage)
	return encoder
}

// ServiceAcknowledgments reads from the stream of acknowledgments and feeds those to the encoder.
func (encoder *QpackEncoder) ServiceAcknowledgments(ar io.Reader) {
	r := NewReader(ar)
	for {
		b, err := r.ReadBit()
		if err != nil {
			panic("unable to read acknowledgment")
			return
		}
		v, err := r.ReadInt(7)
		if err != nil {
			panic("unable to read acknowledgment")
			return
		}
		switch b {
		case 0:
			encoder.AcknowledgeHeader(v)
		case 1:
			encoder.AcknowledgeInsert(int(v))
		}
	}
}

// writeDuplicate duplicates the indicated entry.
func (encoder *QpackEncoder) writeDuplicate(w *Writer, entry DynamicEntry, state *qpackWriterState, i int) error {
	inserted := encoder.Table.Insert(entry.Name(), entry.Value(), state)
	if inserted == nil {
		// Leaving h unmodified causes a literal to be written.
		return nil
	}

	err := w.WriteBits(0, 3)
	if err != nil {
		return err
	}
	// Note: subtract 1 from the index to account for the insertion above.
	err = w.WriteInt(uint64(encoder.Table.Index(entry)-1), 5)
	if err != nil {
		return err
	}

	state.matches[i] = inserted
	state.updateBase(inserted, true)
	return nil
}

// writeInsert writes the entry at state.xxx[i] to the control stream.
// Note that only nameMatch is used for this insertion.
func (encoder *QpackEncoder) writeInsert(w *Writer, state *qpackWriterState, i int,
	nameMatch Entry) error {
	h := state.headers[i]
	inserted := encoder.Table.Insert(h.Name, h.Value, state)
	if inserted == nil {
		// Leaving h unmodified causes a literal to be written.
		return nil
	}

	var instruction uint64
	if nameMatch != nil {
		_, dynamic := nameMatch.(DynamicEntry)
		if dynamic {
			instruction = 2
		} else {
			instruction = 3
		}
	} else {
		instruction = 1
	}
	err := w.WriteBits(instruction, 2)
	if err != nil {
		return err
	}

	switch instruction {
	case 1:
		err = w.WriteStringRaw(h.Name, 5, encoder.HuffmanPreference)
	case 2:
		// Dynamic: subtract 1 from the index to account for the insertion above.
		err = w.WriteInt(uint64(encoder.Table.Index(nameMatch)-1), 6)
	case 3:
		// Static: unmodified index.
		err = w.WriteInt(uint64(encoder.Table.Index(nameMatch)), 6)
	}
	if err != nil {
		return err
	}

	err = w.WriteStringRaw(h.Value, 7, encoder.HuffmanPreference)
	if err != nil {
		return err
	}

	state.matches[i] = inserted
	state.updateBase(inserted, true)
	return nil
}

// writeTableChanges writes out the changes to the header table. It returns the
// largest value of base that can be used for this to work.
func (encoder *QpackEncoder) writeTableChanges(state *qpackWriterState, id uint64) error {
	// We have to buffer everything here.
	var buf bytes.Buffer
	w := NewWriter(&buf)

	// Only one goroutine can update the table at once.
	defer encoder.mutex.Unlock()
	encoder.mutex.Lock()

	// wasntBlocking tracks wheter this id was blocking previously.
	wasntBlocking := false
	maxBase := intMax
	usage := encoder.usage.get(id)
	state.uses = usage.next()
	// If this stream wasn't already blocking, record that.
	if usage.max() <= encoder.highestAcknowledged {
		wasntBlocking = true
		// If we are already at the limit of blocking streams, then we have to avoid
		// adding another blocked stream.  Cap the base to the highest acknowledged.
		if encoder.blockedStreams >= encoder.maxBlockedStreams {
			maxBase = encoder.highestAcknowledged
		}
	}

	for i := range state.headers {
		// Make sure to write into the slice rather than use a copy of each header.
		h := state.headers[i]
		if h.Sensitive {
			continue
		}
		match, nameMatch := encoder.table.LookupReferenceable(h.Name, h.Value, maxBase)
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
		duplicate, insertNameMatch := encoder.table.LookupExtra(h.Name, h.Value, maxBase)
		if duplicate != nil {
			err := encoder.writeDuplicate(w, duplicate, state, i)
			if err != nil {
				return err
			}
			continue
		}

		if maxBase <= encoder.highestAcknowledged {
			// Can't insert another entry, so force this to be a literal.
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

	if wasntBlocking && state.largestBase > encoder.highestAcknowledged {
		// If this wasn't blocking before, it is now.
		encoder.blockedStreams++
	}

	if buf.Len() > 0 {
		err := encoder.updatesWriter.WriteInt(uint64(buf.Len()), 8)
		if err != nil {
			return err
		}
		_, err = io.Copy(encoder.updatesWriter, &buf)
		if err != nil {
			return err
		}
	}
	return nil
}

func (encoder *QpackEncoder) writeIndexed(writer *Writer, state *qpackWriterState, i int) error {
	entry := state.matches[i]
	var err error
	var index int
	var prefix byte
	dynamicEntry, ok := entry.(DynamicEntry)
	if ok {
		index = dynamicEntry.Index(state.largestBase)
		if index < 0 {
			// This is a post-base index.
			prefix = 4
			index *= -1
			err = writer.WriteBits(4, 4)
		} else {
			prefix = 6
			err = writer.WriteBits(2, 2)
		}
	} else {
		prefix = 6
		index = encoder.Table.Index(entry)
		err = writer.WriteBits(3, 2)
	}
	if err != nil {
		return err
	}

	err = writer.WriteInt(uint64(index), prefix)
	if err != nil {
		return err
	}

	state.addUse(i)
	return nil
}

func (encoder *QpackEncoder) writeLiteralNameReference(writer *Writer, state *qpackWriterState,
	sensitive uint64, nameMatch Entry) error {
	var index int
	var prefix byte
	var err error
	dynamicEntry, ok := nameMatch.(DynamicEntry)
	if ok {
		index = dynamicEntry.Index(state.largestBase)
		if index < 0 {
			// Post-base index
			prefix = 3
			err = writer.WriteBits(10|sensitive, 5)
		} else {
			prefix = 4
			err = writer.WriteBits(sensitive<<1, 4)
		}
	} else {
		index = encoder.Table.Index(nameMatch)
		prefix = 4
		err = writer.WriteBits(sensitive<<1|1, 4)
	}
	if err != nil {
		return err
	}

	return writer.WriteInt(uint64(index), prefix)
}

func (encoder *QpackEncoder) writeLiteral(writer *Writer, state *qpackWriterState, i int) error {
	h := state.headers[i]
	var sensitive uint64
	if h.Sensitive {
		sensitive = 1
	}

	var err error
	nameMatch := state.nameMatches[i]
	if nameMatch != nil {
		err = encoder.writeLiteralNameReference(writer, state, sensitive, nameMatch)
	} else {
		err = writer.WriteBits(6|sensitive, 4)
		if err != nil {
			return err
		}
		err = writer.WriteStringRaw(h.Name, 3, encoder.HuffmanPreference)
	}
	if err != nil {
		return err
	}

	err = writer.WriteStringRaw(h.Value, 7, encoder.HuffmanPreference)
	if err != nil {
		return err
	}

	state.addUse(i)
	return nil
}

func (encoder *QpackEncoder) writeHeaderBlock(headerWriter io.Writer, state *qpackWriterState) error {
	w := NewWriter(headerWriter)
	err := w.WriteInt(uint64(state.largestBase), 8)
	if err != nil {
		return err
	}

	// This is the largest reference delta, which this code doesn't use.
	err = w.WriteInt(0, 8)
	if err != nil {
		return err
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
			return err
		}
	}
	return nil
}

// WriteHeaderBlock writes out a header block.  controlWriter is the control stream writer
// `id` is an ID for the stream that this header block relates to. The caller
// needs to pick a token that is unique among the tokens that are currently
// unacknowledged. Using the same token twice without first acknowledging it can
// result in errors.
func (encoder *QpackEncoder) WriteHeaderBlock(headerWriter io.Writer,
	id uint64, headers ...HeaderField) error {
	err := validatePseudoHeaders(headers)
	if err != nil {
		return err
	}

	var state qpackWriterState
	state.initHeaders(headers)
	err = encoder.writeTableChanges(&state, id)
	if err != nil {
		return err
	}

	return encoder.writeHeaderBlock(headerWriter, &state)
}

// Acknowledge is called when a header block has been acknowledged by the peer.
// This allows dynamic table entries to be evicted as necessary on the next
// call.
func (encoder *QpackEncoder) AcknowledgeHeader(id uint64) {
	defer encoder.mutex.Unlock()
	encoder.mutex.Lock()
	reducedBlocks := encoder.usage.ack(id, encoder.blockedStreams)
	if reducedBlocks {
		encoder.blockedStreams--
	}
}

// AcknowledgeInsert acknowledges that the remote decoder has received a
// insert or duplicate instructions up to the specified base.
func (encoder *QpackEncoder) AcknowledgeInsert(base int) {
	defer encoder.mutex.Unlock()
	encoder.mutex.Lock()
	if base > encoder.highestAcknowledged {
		encoder.blockedStreams = encoder.usage.countBlockedStreams(base)
		encoder.highestAcknowledged = base
	}
}

// SetCapacity sets the table capacity. This panics if it is called when the
// capacity has already been set to a non-zero value.
func (encoder *QpackEncoder) SetCapacity(c TableCapacity) {
	defer encoder.mutex.Unlock()
	encoder.mutex.Lock()
	encoder.table.SetCapacity(c)
}

// SetMaxBlockedStreams sets the number of streams that this can encode without blocking.
func (encoder *QpackEncoder) SetMaxBlockedStreams(m int) {
	defer encoder.mutex.Unlock()
	encoder.mutex.Lock()
	if m < encoder.blockedStreams {
		panic("can't reduce the max blocked streams below the actual blocked streams")
	}
	encoder.maxBlockedStreams = m
}
