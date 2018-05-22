package hc

import (
	"sync"
)

const qpackOverhead = TableCapacity(32)

// qpackEntry is an entry in the QPACK table.
type qpackEntry struct {
	BasicDynamicEntry
}

func (e *qpackEntry) Size() TableCapacity {
	return qpackOverhead + TableCapacity(len(e.Name())+len(e.Value()))
}

type qpackTableCommon struct {
	tableCommon
}

// Lookup finds an entry.
func (table *qpackTableCommon) Lookup(name string, value string) (Entry, Entry) {
	return table.lookupImpl(qpackStaticTable, name, value, table.Base())
}

// Index returns the index for the given entry.
func (table *qpackTableCommon) Index(e Entry) int {
	_, dynamic := e.(DynamicEntry)
	if dynamic {
		return table.Base() - e.Base()
	}
	return e.Base()
}

// GetStatic returns the static table entry at the index i.
func (table *qpackTableCommon) GetStatic(i int) Entry {
	if i < 0 || i >= len(qpackStaticTable) {
		return nil
	}
	return qpackStaticTable[i]
}

// QpackDecoderTable is a table for decoding QPACK header fields.
type QpackDecoderTable struct {
	table qpackTableCommon
	lock  sync.RWMutex
	// This is used to notify any waiting readers that new table entries are
	// available.
	insertCondition *sync.Cond
}

// NewQpackDecoderTable makes a new table of the specified capacity.
func NewQpackDecoderTable(capacity TableCapacity) *QpackDecoderTable {
	qt := &QpackDecoderTable{table: qpackTableCommon{tableCommon{capacity: capacity}}}
	qt.insertCondition = sync.NewCond(&qt.lock)
	return qt
}

// GetDynamic gets the entry at index i relative to the specified base.
func (qt *QpackDecoderTable) GetDynamic(i int, base int) Entry {
	defer qt.lock.RUnlock()
	qt.lock.RLock()
	return qt.table.GetDynamic(i, base)
}

// GetStatic is a direct forwarder because it references static information.
func (qt *QpackDecoderTable) GetStatic(i int) Entry {
	return qt.table.GetStatic(i)
}

// WaitForEntry waits until the table base reaches or exceeds the specified value.
func (qt *QpackDecoderTable) WaitForEntry(base int) {
	defer qt.lock.Unlock()
	qt.lock.Lock()
	for qt.table.Base() < base {
		qt.insertCondition.Wait()
	}
}

// Insert an entry into the table.
func (qt *QpackDecoderTable) Insert(name string, value string, evict evictionCheck) DynamicEntry {
	defer qt.lock.Unlock()
	qt.lock.Lock()
	entry := &qpackEntry{BasicDynamicEntry{name, value, 0}}
	inserted := qt.table.insert(entry, evict)
	if !inserted {
		return nil
	}
	qt.insertCondition.Broadcast()
	return entry
}

// Capacity wraps tableCommon.Capacity with a reader lock.
func (qt *QpackDecoderTable) Capacity() TableCapacity {
	defer qt.lock.RUnlock()
	qt.lock.RLock()
	return qt.table.Capacity()
}

// SetCapacity wraps tableCommon.SetCapacity with a writer lock.
func (qt *QpackDecoderTable) SetCapacity(capacity TableCapacity) {
	defer qt.lock.Unlock()
	qt.lock.Lock()
	qt.table.SetCapacity(capacity)
}

// Base wraps tableCommon.Base with a reader lock.
func (qt *QpackDecoderTable) Base() int {
	defer qt.lock.RUnlock()
	qt.lock.RLock()
	return qt.table.Base()
}

// Used wraps tableCommon.Used with a reader lock.
func (qt *QpackDecoderTable) Used() TableCapacity {
	defer qt.lock.RUnlock()
	qt.lock.RLock()
	return qt.table.Used()
}

// Lookup wraps tableCommon.Lookup with a reader lock.
func (qt *QpackDecoderTable) Lookup(name string, value string) (Entry, Entry) {
	defer qt.lock.RUnlock()
	qt.lock.RLock()
	return qt.table.Lookup(name, value)
}

// Index wraps tableCommon.Index with a reader lock.
func (qt *QpackDecoderTable) Index(e Entry) int {
	defer qt.lock.RUnlock()
	qt.lock.RLock()
	return qt.table.Index(e)
}

type qpackEncoderEntry struct {
	qpackEntry
	usageCount uint
}

func (qe *qpackEncoderEntry) inUse() bool {
	return qe.usageCount > 0
}

type qpackHeaderBlockUsage struct {
	entries []*qpackEncoderEntry
	max     int
}

func (qhbu *qpackHeaderBlockUsage) add(qe *qpackEncoderEntry) {
	qhbu.entries = append(qhbu.entries, qe)
	qe.usageCount++
	if qe.Base() > qhbu.max {
		qhbu.max = qe.Base()
	}
}

func (qhbu *qpackHeaderBlockUsage) ack() {
	for _, qe := range qhbu.entries {
		qe.usageCount--
	}
	qhbu.entries = nil
}

type qpackUsageTracker map[uint64][]*qpackHeaderBlockUsage

func (qut *qpackUsageTracker) make(id uint64) *qpackHeaderBlockUsage {
	block := &qpackHeaderBlockUsage{}
	(*qut)[id] = append((*qut)[id], block)
	return block
}

func (qut *qpackUsageTracker) ack(id uint64) int {
	allUses := (*qut)[id]
	if allUses == nil || len(allUses) == 0 {
		panic("shouldn't be acknowledging blocks that don't exist")
	}
	allUses[0].ack()
	remaining := len(allUses) - 1
	if remaining == 0 {
		delete(*qut, id)
	} else {
		(*qut)[id] = allUses[1:]
	}
	return remaining
}

// QpackEncoderTable is the table used by the QPACK encoder. It is enhanced to
// monitor inserts and ensure that references are properly tracked.  This is
// NOT safe for concurrent use in the same way that QpackDecoderTable is.
type QpackEncoderTable struct {
	qpackTableCommon
	// The amount of table capacity we will actively use.
	referenceableLimit TableCapacity
	// The number of entries we can use right now.
	referenceable int
	// The size of those usable entries.
	referenceableSize TableCapacity
}

// NewQpackEncoderTable makes a new encoder table. Note that margin is the
// amount of space we reserve. Entries that spill over into that space are not
// referenced by the encoder.
func NewQpackEncoderTable(capacity TableCapacity, margin TableCapacity) *QpackEncoderTable {
	return &QpackEncoderTable{
		qpackTableCommon{tableCommon{capacity: capacity}},
		capacity - margin, 0, 0,
	}
}

func (qt *QpackEncoderTable) added(increase TableCapacity) {
	updatedSize := qt.referenceableSize + increase
	i := qt.referenceable + 1
	for updatedSize > qt.referenceableLimit {
		i--
		updatedSize -= qt.dynamic[i].Size()
	}
	qt.referenceable = i
	qt.referenceableSize = updatedSize
}

func (qt *QpackEncoderTable) removed(reduction TableCapacity) {
	qt.referenceable--
	qt.referenceableSize -= reduction
}

type qpackEncoderEvictWrapper struct {
	wrapped evictionCheck
	table   *QpackEncoderTable
}

func (qevict *qpackEncoderEvictWrapper) CanEvict(e DynamicEntry) bool {
	if !qevict.wrapped.CanEvict(e) {
		return false
	}
	qe := e.(*qpackEncoderEntry)
	if qe.inUse() {
		return false
	}
	qevict.table.removed(qe.Size())
	return true
}

// Insert an entry. This monitors for both evictions and insertions so that a
// limit on referenceable entries can be maintained.
func (qt *QpackEncoderTable) Insert(name string, value string, evict evictionCheck) DynamicEntry {
	entry := &qpackEncoderEntry{qpackEntry{BasicDynamicEntry{name, value, 0}}, 0}
	inserted := qt.insert(entry, &qpackEncoderEvictWrapper{evict, qt})
	if inserted {
		qt.added(entry.Size())
		return entry
	}
	return nil
}

// LookupReferenceable looks in the table for a matching name and value. It only
// includes those entries that are below the configured margin.
func (qt *QpackEncoderTable) LookupReferenceable(name string, value string) (Entry, Entry) {
	return qt.lookupImpl(qpackStaticTable, name, value, qt.referenceable)
}

// LookupExtra looks in the table for a dynamic entry after the provided
// offset. It is design for use after lookupLimited() fails.
func (qt *QpackEncoderTable) LookupExtra(name string, value string) (DynamicEntry, DynamicEntry) {
	var nameMatch DynamicEntry
	for _, entry := range qt.dynamic[qt.referenceable:] {
		if entry.Name() == name {
			if entry.Value() == value {
				return entry, entry
			}
			if nameMatch != nil {
				nameMatch = entry
			}
		}
	}
	return nil, nameMatch
}

// SetCapacity sets the table capacity. This panics if it is called after an
// entry has been inserted. For safety, only set this to a non-zero value from a
// zero value.
func (qt *QpackEncoderTable) SetCapacity(c TableCapacity) {
	if qt.Base() > 0 {
		panic("Can't change encoder table size after inserting anything")
	}
	qt.capacity = c
}
