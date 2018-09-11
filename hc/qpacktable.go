package hc

import (
	"sync"
)

const tableOverhead = TableCapacity(32)

// qpackEntry is an entry in the QPACK table.
type qpackEntry struct {
	BasicDynamicEntry
}

type qpackTableCommon struct {
	tableCommon
}

const useQpackStaticTable = true

// Lookup finds an entry.
func (table *qpackTableCommon) Lookup(name string, value string) (Entry, Entry) {
	if useQpackStaticTable {
		return table.lookupImpl(qpackStaticTable, name, value, 0, len(table.dynamic))
	}
	return table.lookupImpl(hpackStaticTable, name, value, 0, len(table.dynamic))
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
	if useQpackStaticTable {
		if i < 0 || i >= len(qpackStaticTable) {
			return nil
		}
		return qpackStaticTable[i]
	}

	// Use the HPACK table temporarily.
	i-- // one-based indexing, gross.
	if i < 0 || i >= len(hpackStaticTable) {
		return nil
	}
	return hpackStaticTable[i]
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

func (hbu *qpackHeaderBlockUsage) add(qe *qpackEncoderEntry) {
	hbu.entries = append(hbu.entries, qe)
	qe.usageCount++
	if qe.Base() > hbu.max {
		hbu.max = qe.Base()
	}
}

func (hbu *qpackHeaderBlockUsage) ack() {
	for _, qe := range hbu.entries {
		qe.usageCount--
	}
	hbu.entries = nil
}

type qpackStreamUsage []*qpackHeaderBlockUsage

func (su *qpackStreamUsage) add(block *qpackHeaderBlockUsage) {
	*su = append(*su, block)
}

// ack removes the oldest header block usage,
// returns the largest reference in that blocks.
func (su *qpackStreamUsage) ack() int {
	if len(*su) == 0 {
		return 0
	}
	z := (*su)[0]
	z.ack()
	*su = (*su)[1:]
	return z.max
}

// cancel removes all header block usage,
// returns the largest reference across all blocks.
func (su *qpackStreamUsage) cancel() int {
	m := 0
	for _, z := range *su {
		z.ack()
		if z.max > m {
			m = z.max
		}
	}
	return m
}

func (su *qpackStreamUsage) count() int {
	return len(*su)
}

func (su *qpackStreamUsage) max() int {
	m := 0
	for _, hbu := range *su {
		if hbu.max > m {
			m = hbu.max
		}
	}
	return m
}

type qpackUsageTracker map[uint64]*qpackStreamUsage

func (ut *qpackUsageTracker) get(id uint64) *qpackStreamUsage {
	su := (*ut)[id]
	if su == nil {
		su = &qpackStreamUsage{}
		(*ut)[id] = su
	}
	return su
}

// ack removes one header block from the given id.  This returns the largest
// reference from the acknowledged block and the new largest reference for
// the given id so that the calling code can account for the number of blocked
// streams correctly and efficiently.
func (ut *qpackUsageTracker) ack(id uint64) (int, int) {
	su := (*ut)[id]
	if su == nil {
		// TODO report an error rather than just passing this silently
		return 0, 0
	}
	oldLargest := su.ack()
	if su.count() == 0 {
		delete(*ut, id)
	}
	return oldLargest, su.max()
}

func (ut *qpackUsageTracker) cancel(id uint64) int {
	su := (*ut)[id]
	if su == nil {
		return -1
	}
	largest := su.cancel()
	delete(*ut, id)
	return largest
}

func (ut *qpackUsageTracker) countBlockedStreams(largestAcknowledged int) (count int) {
	for _, su := range *ut {
		if su.max() > largestAcknowledged {
			count++
		}
	}
	return count
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
func NewQpackEncoderTable(capacity TableCapacity, referenceable TableCapacity) *QpackEncoderTable {
	var qt QpackEncoderTable
	qt.SetCapacity(capacity)
	qt.SetReferenceableLimit(referenceable)
	return &qt
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
func (qt *QpackEncoderTable) LookupReferenceable(name string, value string, maxBase int) (Entry, Entry) {
	start := 0
	if maxBase < qt.Base() {
		start = qt.Base() - maxBase
	}
	end := qt.referenceable
	if end <= start {
		end = start // i.e., don't search the dynamic table at all.
	}
	if useQpackStaticTable {
		return qt.lookupImpl(qpackStaticTable, name, value, start, end)
	}
	return qt.lookupImpl(hpackStaticTable, name, value, start, end)
}

// LookupBlocked looks in the portion of the table that we're blocked from looking at
// and returns true if there is a matching entry.  This is used to prevent inserting
// too many duplicates of the same entry when the encoder is blocked.
func (qt *QpackEncoderTable) LookupBlocked(name string, value string, maxBase int) bool {
	if maxBase >= qt.Base() {
		return false
	}
	end := qt.Base() - maxBase
	if end > qt.referenceable {
		end = qt.referenceable
	}
	for _, entry := range qt.dynamic[:end] {
		if entry.Name() == name && entry.Value() == value {
			return true
		}
	}
	return false
}

// LookupExtra looks in the table for a dynamic entry after the provided
// offset. It is designed for use after LookupReferenceable() fails.
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
		// TODO implement something sensible here
		panic("Can't change encoder table size after inserting anything")
	}
	qt.capacity = c
	if qt.referenceableLimit > c {
		qt.SetReferenceableLimit(qt.referenceableLimit)
	}
}

// SetReferenceableLimit limits the space in the table that can be used.
// This value is set to the minimum of the provided value and the capacity.
func (qt *QpackEncoderTable) SetReferenceableLimit(limit TableCapacity) {
	if limit > qt.capacity {
		limit = qt.capacity
	}

	qt.referenceableLimit = limit
	qt.referenceableSize = 0
	qt.referenceable = 0

	remainingSpace := qt.referenceableLimit
	for i := range qt.dynamic {
		sz := qt.dynamic[i].Size()
		if sz < remainingSpace {
			qt.referenceable++
			qt.referenceableSize += sz
		} else {
			break
		}
	}
}
