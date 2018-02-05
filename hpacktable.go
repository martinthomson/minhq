package minhq

import (
	"errors"
)

// HpackEntry is a key-value pair for the HPACK table.
type HpackEntry interface {
	Name() string
	Value() string
	Index() int
}

// HpackTableCapacity is the type of the HPACK table capacity.
type HpackTableCapacity uint

// hpackDynamicEntry is an entry in the dynamic table.
type hpackDynamicEntry struct {
	name  string
	value string
	table *HpackTable
	// The insert count at the time that this was added to the table.
	inserts int
}

func (hd hpackDynamicEntry) Index() int {
	return hd.table.inserts - hd.inserts + len(hpackStaticTable) + 1
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
	inserts int
}

// ErrHpackEntryNotFound indicates that a reference was made outside of the table.
var ErrHpackEntryNotFound = errors.New("HPACK table entry not found")

// Len is the number of entries in the combined table.  Note that because
// HPACK uses a 1-based index, this is the index of the oldest dynamic entry.
func (table HpackTable) Len() int {
	return len(hpackStaticTable) + len(table.dynamic)
}

// Get an entry from the table.
func (table HpackTable) Get(i int) HpackEntry {
	if (i <= 0) || (i > table.Len()) {
		return nil
	}
	if i <= len(hpackStaticTable) {
		return hpackStaticTable[i-1]
	}
	return table.dynamic[i-len(hpackStaticTable)-1]
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

// Insert an entry into the table.
func (table *HpackTable) Insert(name string, value string) HpackEntry {
	table.inserts++
	entry := hpackDynamicEntry{name, value, table, table.inserts}
	if entry.Size() > table.capacity {
		table.dynamic = table.dynamic[0:0]
		table.used = 0
	} else {
		table.evictTo(table.capacity - entry.Size())
		tmp := make([]*hpackDynamicEntry, len(table.dynamic)+1)
		copy(tmp[1:], table.dynamic)
		tmp[0] = &entry
		table.dynamic = tmp
		table.used += entry.Size()
	}
	return &entry
}

// SetCapacity increases or reduces capacity to the set target.
func (table *HpackTable) SetCapacity(capacity HpackTableCapacity) {
	table.evictTo(capacity)
	table.capacity = capacity
}

// Lookup looks in the table for a matching name and value. This produces two
// return values: the first is match on both name and value, which is often nil.
// The second is a match on name only, which might also be nil.
func (table HpackTable) Lookup(name string, value string) (HpackEntry, HpackEntry) {
	var nameOnly HpackEntry
	for i := 1; i <= table.Len(); i++ {
		entry := table.Get(i)
		if entry.Name() == name {
			if entry.Value() == value {
				return entry, entry
			}
			if nameOnly == nil {
				nameOnly = entry
			}
		}
	}
	return nil, nameOnly
}
