package minhq

import "errors"

// HpackEntry is a key-value pair for the HPACK table.
type HpackEntry interface {
	Name() string
	Value() string
	Index() uint
}

// HpackTableCapacity is the type of the HPACK table capacity.
type HpackTableCapacity uint

// hpackDynamicEntry is an entry in the dynamic table.
type hpackDynamicEntry struct {
	name  string
	value string
	table *HpackTable
	// The insert count at the time that this was added to the table.
	inserts uint
}

func (hd hpackDynamicEntry) Index() uint {
	return hd.table.inserts - hd.inserts + uint(len(hpackStaticTable))
}

func (hd hpackDynamicEntry) Name() string {
	return hd.name
}
func (hd hpackDynamicEntry) Value() string {
	return hd.value
}

func (hd hpackDynamicEntry) Size() HpackTableCapacity {
	return HpackTableCapacity(32 + len(hd.Name()) + len(hd.Value()))
}

// HpackTable holds table entries.
type HpackTable struct {
	dynamic []*hpackDynamicEntry
	// The total capacity (in HPACK bytes) of the table.  This is set by configuration.
	capacity HpackTableCapacity
	// The amount of used capacity.
	used HpackTableCapacity
	// The total number of inserts thus far.
	inserts uint
}

// ErrHpackEntryNotFound indicates that a reference was made outside of the table.
var ErrHpackEntryNotFound = errors.New("HPACK table entry not found")

// Get an entry from the table.
func (table HpackTable) Get(i int) (HpackEntry, error) {
	if (i <= 0) || (i > len(hpackStaticTable)+len(table.dynamic)) {
		return nil, ErrHpackEntryNotFound
	}
	if i <= len(hpackStaticTable) {
		return hpackStaticTable[i], nil
	}
	return table.dynamic[i-len(hpackStaticTable)], nil
}

// Evict entries until the used capacity is less than the reduced capacity.
func (table *HpackTable) evictTo(reduced HpackTableCapacity) {
	l := len(table.dynamic)
	for l > 0 && table.used > reduced {
		l--
		table.used -= table.dynamic[l].Size()
	}
	table.dynamic = table.dynamic[0:l]
}

// Evict for a new addition.  Includes logic for entries that overflow - and
// therefore flush - the table.
func (table *HpackTable) evictFor(entrySize HpackTableCapacity) {
	if entrySize > table.capacity {
		table.dynamic = table.dynamic[0:0]
		table.used = 0
	} else {
		table.evictTo(table.capacity - entrySize)
	}
}

// Insert an entry into the table.
func (table *HpackTable) Insert(name string, value string) HpackEntry {
	table.inserts++
	entry := hpackDynamicEntry{name, value, table, table.inserts}
	table.evictFor(entry.Size())
	table.dynamic = append(table.dynamic, &entry)
	table.used += entry.Size()
	return &entry
}

// SetCapacity increases or reduces capacity to the set target.
func (table *HpackTable) SetCapacity(capacity HpackTableCapacity) {
	table.evictTo(capacity)
	table.capacity = capacity
}
