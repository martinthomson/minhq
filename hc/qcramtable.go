package hc

import (
	"container/list"
	"sync"
)

// TODO increase the overhead to something more than 32 to account for the need to store the base.
const qcramOverhead = TableCapacity(32)

// qcramEntry is an entry in the QCRAM table.
type qcramEntry struct {
	BasicDynamicEntry
}

func (e *qcramEntry) Size() TableCapacity {
	return qcramOverhead + TableCapacity(len(e.Name())+len(e.Value()))
}

// QcramDecoderTable is a table for decoding QCRAM header fields.
type QcramDecoderTable struct {
	table tableCommon
	lock  sync.RWMutex
	// This is used to notify any waiting readers that new table entries are
	// available.
	insertCondition *sync.Cond
}

// NewQcramDecoderTable makes a new table of the specified capacity.
func NewQcramDecoderTable(capacity TableCapacity) *QcramDecoderTable {
	qt := &QcramDecoderTable{table: tableCommon{capacity: capacity}}
	qt.insertCondition = sync.NewCond(&qt.lock)
	return qt
}

// LastIndex returns the highest index in the table.
func (qt *QcramDecoderTable) LastIndex(base int) int {
	defer qt.lock.RUnlock()
	qt.lock.RLock()
	return qt.table.LastIndex(base)
}

// GetWithBase gets the entry at index i relative to the specified base.
func (qt *QcramDecoderTable) GetWithBase(i int, base int) Entry {
	defer qt.lock.RUnlock()
	qt.lock.RLock()
	return qt.table.GetWithBase(i, base)
}

// Get returns the entry at the index i relative to the current base.
func (qt *QcramDecoderTable) Get(i int) Entry {
	defer qt.lock.RUnlock()
	qt.lock.RLock()
	return qt.table.Get(i)
}

// WaitForBase waits until the table base reaches or exceeds the specified value.
func (qt *QcramDecoderTable) WaitForBase(base int) {
	defer qt.lock.Unlock()
	qt.lock.Lock()
	for qt.table.Base() < base {
		qt.insertCondition.Wait()
	}
}

// Insert an entry into the table.
func (qt *QcramDecoderTable) Insert(name string, value string, evict evictionCheck) DynamicEntry {
	defer qt.lock.Unlock()
	qt.lock.Lock()
	entry := &qcramEntry{BasicDynamicEntry{name, value, 0}}
	inserted := qt.table.insert(entry, evict)
	if !inserted {
		return nil
	}
	qt.insertCondition.Broadcast()
	return entry
}

// Capacity wraps tableCommon.Capacity with a reader lock.
func (qt *QcramDecoderTable) Capacity() TableCapacity {
	defer qt.lock.RUnlock()
	qt.lock.RLock()
	return qt.table.Capacity()
}

// Base wraps tableCommon.Base with a reader lock.
func (qt *QcramDecoderTable) Base() int {
	defer qt.lock.RUnlock()
	qt.lock.RLock()
	return qt.table.Base()
}

// Used wraps tableCommon.Used with a reader lock.
func (qt *QcramDecoderTable) Used() TableCapacity {
	defer qt.lock.RUnlock()
	qt.lock.RLock()
	return qt.table.Used()
}

// Lookup wraps tableCommon.Lookup with a reader lock.
func (qt *QcramDecoderTable) Lookup(name string, value string) (Entry, Entry) {
	defer qt.lock.RUnlock()
	qt.lock.RLock()
	return qt.table.Lookup(name, value)
}

type qcramEncoderEntry struct {
	qcramEntry
	uses list.List
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

// QcramEncoderTable is the table used by the QCRAM encoder. It is enhanced to
// monitor inserts and ensure that references are properly tracked.
type QcramEncoderTable struct {
	tableCommon
	// The amount of table capacity we will actively use.
	referenceableLimit TableCapacity
	// The number of entries we can use right now.
	referenceable int
	// The size of those usable entries.
	referenceableSize TableCapacity
}

// NewQcramEncoderTable makes a new encoder table. Note that margin is the
// amount of space we reserve. Entries that spill over into that space are not
// referenced by the encoder.
func NewQcramEncoderTable(capacity TableCapacity, margin TableCapacity) *QcramEncoderTable {
	return &QcramEncoderTable{tableCommon{capacity: capacity}, capacity - margin, 0, 0}
}

func (qt *QcramEncoderTable) added(increase TableCapacity) {
	updatedSize := qt.referenceableSize + increase
	i := qt.referenceable + 1
	for updatedSize > qt.referenceableLimit {
		i--
		updatedSize -= qt.dynamic[i].Size()
	}
	qt.referenceable = i
	qt.referenceableSize = updatedSize
}

func (qt *QcramEncoderTable) removed(reduction TableCapacity) {
	qt.referenceable--
	qt.referenceableSize -= reduction
}

type qcramEncoderEvictWrapper struct {
	wrapped evictionCheck
	table   *QcramEncoderTable
}

func (qevict *qcramEncoderEvictWrapper) CanEvict(e DynamicEntry) bool {
	if !qevict.wrapped.CanEvict(e) {
		return false
	}
	qe := e.(*qcramEncoderEntry)
	if qe.inUse() {
		return false
	}
	qevict.table.removed(qe.Size())
	return true
}

// Insert an entry. This monitors for both evictions and insertions so that a
// limit on referenceable entries can be maintained.
func (qt *QcramEncoderTable) Insert(name string, value string, evict evictionCheck) DynamicEntry {
	entry := &qcramEncoderEntry{qcramEntry{BasicDynamicEntry{name, value, 0}}, list.List{}}
	inserted := qt.insert(entry, &qcramEncoderEvictWrapper{evict, qt})
	if inserted {
		qt.added(entry.Size())
		return entry
	}
	return nil
}

// LookupReferenceable looks in the table for a matching name and value. It only
// includes those entries that are below the configured margin.
func (qt *QcramEncoderTable) LookupReferenceable(name string, value string) (Entry, Entry) {
	return qt.lookupImpl(name, value, qt.referenceable)
}

// LookupExtra looks in the table for a dynamic entry after the provided
// offset. It is design for use after lookupLimited() fails.
func (qt *QcramEncoderTable) LookupExtra(name string, value string) (DynamicEntry, DynamicEntry) {
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

// Acknowledge removes uses of dynamic entries attributed to `token`.
func (qt *QcramEncoderTable) Acknowledge(token interface{}) {
	for _, e := range qt.dynamic {
		qe := e.(*qcramEncoderEntry)
		qe.removeUse(token)
	}
}

// SetCapacity sets the table capacity. This panics if it is called after an
// entry has been inserted. For safety, only set this to a non-zero value from a
// zero value.
func (qt *QcramEncoderTable) SetCapacity(c TableCapacity) {
	if qt.Base() > 0 {
		panic("Can't change encoder table size after inserting anything")
	}
	qt.capacity = c
}
