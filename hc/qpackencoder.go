package hc

import (
	"io"
	"strings"
	"sync"
)

const intMax = int(^uint(0) >> 1)

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

	// wasBlocked records if the record was originally blocked.
	wasBlocked bool
	// maxBase is the maximum base that can be referenced by this state.
	maxBase int
	// uses is the usage tracker for the header block we're producing.
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

// setupUsage configures the state with a usage tracker.
func (state *qpackWriterState) setupUsage(streamUsage *qpackStreamUsage, highestAcknowledged int, blockingAllowed bool) {
	state.maxBase = intMax
	if streamUsage.max() > highestAcknowledged {
		state.wasBlocked = true
	} else {
		// If we are already at the limit of blocking streams, then we have to avoid
		// adding another blocked stream.  Cap the base to the highest acknowledged.
		if !blockingAllowed {
			state.maxBase = highestAcknowledged
		}
	}
	state.uses = &qpackHeaderBlockUsage{}
}

// recordUsage registers the usage tracker as necessary.
// Usage isn't tracked if the header block doesn't use the dynamic table.
func (state *qpackWriterState) recordUsage(streamUsage *qpackStreamUsage) {
	if state.largestBase > 0 {
		streamUsage.add(state.uses)
	}
}

func dynBase(e Entry) int {
	dyn, ok := e.(DynamicEntry)
	if !ok {
		return 0
	}
	return dyn.Base()
}

func (state *qpackWriterState) recordMatch(i int, full Entry, name Entry) {
	if full != nil && dynBase(full) <= state.maxBase {
		state.matches[i] = full
		state.updateBase(full, true)
		return
	}
	if name != nil && dynBase(name) <= state.maxBase {
		state.nameMatches[i] = name
		state.updateBase(name, false)
	}
}

func (state *qpackWriterState) updateBase(e Entry, match bool) {
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

func (state *qpackWriterState) isNewlyBlocked(highestAcknowledged int) bool {
	// A stream that was already blocking can't cause more blocking.
	if state.wasBlocked {
		return false
	}
	return state.largestBase > highestAcknowledged
}

func (state *qpackWriterState) CanEvict(e DynamicEntry) bool {
	return e.Base() < state.smallestBase
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
func (encoder *QpackEncoder) ServiceAcknowledgments(ar io.Reader) error {
	r := NewReader(ar)
	for {
		b, err := r.ReadBit()
		if err != nil {
			return err
		}
		switch b {
		case 1:
			v, err := r.ReadInt(7)
			if err != nil {
				return err
			}
			err = encoder.AcknowledgeHeader(v)
		case 0:
			b, err = r.ReadBit()
			if err != nil {
				return err
			}
			v, err := r.ReadInt(6)
			if err != nil {
				return err
			}
			switch b {
			case 0:
				err = encoder.AcknowledgeInsert(int(v))
			case 1:
				err = encoder.AcknowledgeReset(v)
			}
		}
		if err != nil {
			return err
		}
	}
}

// writeDuplicate duplicates the indicated entry.
func (encoder *QpackEncoder) writeDuplicate(entry DynamicEntry, state *qpackWriterState, i int) error {
	inserted := encoder.Table.Insert(entry.Name(), entry.Value(), state)
	if inserted == nil {
		// Leaving h unmodified causes a literal to be written.
		return nil
	}

	err := encoder.updatesWriter.WriteBits(0, 3)
	if err != nil {
		return err
	}
	// Note: subtract 1 from the index to account for the insertion above.
	err = encoder.updatesWriter.WriteInt(uint64(encoder.Table.Index(entry)-1), 5)
	if err != nil {
		return err
	}
	state.recordMatch(i, inserted, nil)
	return nil
}

// writeInsert writes the entry at state.xxx[i] to the control stream.
// Note that only nameMatch is used for this insertion.
func (encoder *QpackEncoder) writeInsert(state *qpackWriterState, i int,
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
	w := encoder.updatesWriter
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

	state.recordMatch(i, inserted, nil)
	return nil
}

// writeTableChanges writes out the changes to the header table. It returns the
// largest value of base that can be used for this to work.
func (encoder *QpackEncoder) writeTableChanges(state *qpackWriterState, id uint64) error {
	// Only one goroutine can update the table at once.
	defer encoder.mutex.Unlock()
	encoder.mutex.Lock()

	// wasntBlocking tracks wheter this id was blocking previously.
	streamUsage := encoder.usage.get(id)
	blockingAllowed := encoder.blockedStreams < encoder.maxBlockedStreams
	state.setupUsage(streamUsage, encoder.highestAcknowledged, blockingAllowed)

	for i := range state.headers {
		// Make sure to write into the slice rather than use a copy of each header.
		h := state.headers[i]
		if h.Sensitive {
			continue
		}

		// Here we look for a match with the header that we can use.
		// We split potential matches into three categories:
		//   referenceable - these entries can be referenced safely.  This always
		//     includes the static table.  It includes anything in the dynamic table
		//     that is between maxBase and the marker we set near the end of the
		//     table (which marks things as being close to eviction).  There could
		//     be no referenceable entries.
		//   blocking - if we find a complete match in the table in the region
		//     that is blocked by maxBase, we stop.  We can neither reference that
		//     entry, nor add another with the same values.
		//   at risk - if we find a match in the table near the end, then
		//     referencing it might cause later attempts to insert other entries to
		//     block.  These entries are duplicated and the new entries referenced.
		//     If this turns up a name match, a name reference to that name is used
		//     when inserting a new entry.
		//     The at-risk lookup considers entries that are blocked by maxBase.
		//     However, to avoid churn on the table, unacknowledged entries are not
		//     duplicated.

		match, nameMatch := encoder.table.LookupReferenceable(h.Name, h.Value, state.maxBase)
		if match != nil {
			state.recordMatch(i, match, nameMatch)
			continue
		}

		if encoder.table.LookupBlocked(h.Name, h.Value, state.maxBase) {
			continue
		}

		// Now look for a duplicate, and maybe a name match that we can use for
		// insertion if duplication isn't possible. Don't use either of these when
		// encoding the header block to avoid holding down references to entries that
		// we might want to evict.
		var insertNameMatch Entry
		duplicate, insertNameMatch := encoder.table.LookupExtra(h.Name, h.Value)
		if duplicate != nil {
			// Only duplicate acknowledged entries.  Refreshing entries more than
			// once per round trip is going to churn the table too much.
			if duplicate.Base() <= encoder.highestAcknowledged {
				err := encoder.writeDuplicate(duplicate, state, i)
				if err != nil {
					return err
				}
			}
			continue
		}

		// Use nameMatch instead of insertNameMatch for inserting on the hope that
		// it has a shorter encoding.  Record the match on the name in case we
		// don't (or can't) insert this entry.
		if nameMatch != nil {
			state.recordMatch(i, nil, nameMatch)
			insertNameMatch = nameMatch
		}
		if encoder.shouldIndex(h) && h.size() <= encoder.Table.Capacity() {
			err := encoder.writeInsert(state, i, insertNameMatch)
			if err != nil {
				return err
			}
		}
	}

	if state.isNewlyBlocked(encoder.highestAcknowledged) {
		// If this wasn't blocking before, it is now.
		encoder.blockedStreams++
	}

	state.recordUsage(streamUsage)
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
			index = -1 - index // -1 --> 0, -2 --> 1
			err = writer.WriteBits(1, 4)
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
			err = writer.WriteBits(sensitive, 5)
			index = -1 - index
		} else {
			prefix = 4
			err = writer.WriteBits(4|sensitive<<1, 4)
		}
	} else {
		index = encoder.Table.Index(nameMatch)
		prefix = 4
		err = writer.WriteBits(5|sensitive<<1, 4)
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
		err = writer.WriteBits(2|sensitive, 4)
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

	// This is the base index delta, which this code doesn't use.
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

// AcknowledgeInsert acknowledges that the remote decoder has received a
// new insert or duplicate instructions.
func (encoder *QpackEncoder) AcknowledgeInsert(increment int) error {
	defer encoder.mutex.Unlock()
	encoder.mutex.Lock()
	if increment <= 0 {
		return ErrIndexError
	}
	base := encoder.highestAcknowledged + increment
	if base > encoder.Table.Base() {
		return ErrIndexError
	}
	encoder.blockedStreams = encoder.usage.countBlockedStreams(base)
	encoder.highestAcknowledged = base
	return nil
}

// AcknowledgeHeader is called when a header block has been acknowledged by the peer.
// This allows dynamic table entries to be evicted as necessary on the next call.
func (encoder *QpackEncoder) AcknowledgeHeader(id uint64) error {
	defer encoder.mutex.Unlock()
	encoder.mutex.Lock()
	removedLargest, newLargest := encoder.usage.ack(id)
	if removedLargest == 0 {
		return ErrIndexError
	}
	if removedLargest > encoder.highestAcknowledged && newLargest <= encoder.highestAcknowledged {
		encoder.blockedStreams--
		encoder.highestAcknowledged = removedLargest
	}
	return nil
}

// AcknowledgeReset is used when this side resets a stream.  When the decoder
// discovers that it might not be able to acknowledge all the header blocks,
// it sends a cancellation acknowledgment that we need to consume.
func (encoder *QpackEncoder) AcknowledgeReset(id uint64) error {
	defer encoder.mutex.Unlock()
	encoder.mutex.Lock()
	largest := encoder.usage.cancel(id)
	if largest < 0 {
		return ErrIndexError // unknown stream ID
	}
	if largest > encoder.highestAcknowledged {
		encoder.blockedStreams--
	}
	return nil
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
