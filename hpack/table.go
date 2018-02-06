package hpack

import (
	"errors"
)

// Entry is a key-value pair for the HPACK table.
type Entry interface {
	Name() string
	Value() string
	Index() int
}

// TableCapacity is the type of the HPACK table capacity.
type TableCapacity uint

// dynamicEntry is an entry in the dynamic table.
type dynamicEntry struct {
	name  string
	value string
	table *Table
	// The insert count at the time that this was added to the table.
	inserts int
}

func (hd dynamicEntry) Index() int {
	return hd.table.inserts - hd.inserts + len(hpackStaticTable) + 1
}

func (hd dynamicEntry) Name() string {
	return hd.name
}
func (hd dynamicEntry) Value() string {
	return hd.value
}

func (hd dynamicEntry) Size() TableCapacity {
	return TableCapacity(32 + len(hd.Name()) + len(hd.Value()))
}

// Table holds dynamic entries and accounting for space.
type Table struct {
	dynamic []*dynamicEntry
	// The total capacity (in HPACK bytes) of the table. This is set by
	// configuration.
	capacity TableCapacity
	// The amount of used capacity.
	used TableCapacity
	// The total number of inserts thus far.
	inserts int
}

// ErrHpackEntryNotFound indicates that a reference was made outside of the
// table.
var ErrHpackEntryNotFound = errors.New("HPACK table entry not found")

// Len is the number of entries in the combined table. Note that because
// HPACK uses a 1-based index, this is the index of the oldest dynamic entry.
func (table Table) Len() int {
	return len(hpackStaticTable) + len(table.dynamic)
}

// Get an entry from the table.
func (table Table) Get(i int) Entry {
	if (i <= 0) || (i > table.Len()) {
		return nil
	}
	if i <= len(hpackStaticTable) {
		return hpackStaticTable[i-1]
	}
	return table.dynamic[i-len(hpackStaticTable)-1]
}

// Evict entries until the used capacity is less than the reduced capacity.
func (table *Table) evictTo(reduced TableCapacity) {
	l := len(table.dynamic)
	for l > 0 && table.used > reduced {
		l--
		table.used -= table.dynamic[l].Size()
	}
	table.dynamic = table.dynamic[0:l]
}

// Insert an entry into the table.
func (table *Table) Insert(name string, value string) Entry {
	table.inserts++
	entry := dynamicEntry{name, value, table, table.inserts}
	if entry.Size() > table.capacity {
		table.dynamic = table.dynamic[0:0]
		table.used = 0
	} else {
		table.evictTo(table.capacity - entry.Size())
		tmp := make([]*dynamicEntry, len(table.dynamic)+1)
		copy(tmp[1:], table.dynamic)
		tmp[0] = &entry
		table.dynamic = tmp
		table.used += entry.Size()
	}
	return &entry
}

// SetCapacity increases or reduces capacity to the set target.
func (table *Table) SetCapacity(capacity TableCapacity) {
	table.evictTo(capacity)
	table.capacity = capacity
}

// Lookup looks in the table for a matching name and value. This produces two
// return values: the first is match on both name and value, which is often nil.
// The second is a match on name only, which might also be nil.
func (table Table) Lookup(name string, value string) (Entry, Entry) {
	var nameOnly Entry
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
