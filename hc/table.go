package hc

import (
	"errors"
)

// Entry is a key-value pair for the HPACK table.
type Entry interface {
	Name() string
	Value() string
	Index(base int) int
}

// TableCapacity is the type of the HPACK table capacity.
type TableCapacity uint

// dynamicEntry is an entry in the dynamic table.
type dynamicEntry struct {
	name  string
	value string
	// The insert count at the time that this was added to the table.
	inserts int
}

func (hd dynamicEntry) Index(base int) int {
	delta := base - hd.inserts
	if delta < 0 {
		// If base < inserts, then this entry was added after the base and the index
		// will be invalid. Return 0.
		return 0
	}
	// If base > inserts, then this entry was added before the base was set. The
	// index is be valid.
	return len(staticTable) + 1 + delta
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

func (hd dynamicEntry) Base() int {
	return hd.inserts
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

	// TODO: Use this to ensure that reads and writes synchronize properly
	//
	// mutex sync.RWMutex
}

// ErrHpackEntryNotFound indicates that a reference was made outside of the
// table.
var ErrHpackEntryNotFound = errors.New("HPACK table entry not found")

// Len is the number of entries in the combined table. Note that because
// HPACK uses a 1-based index, this is the index of the oldest dynamic entry.
func (table *Table) Len() int {
	return len(staticTable) + len(table.dynamic)
}

// Get an entry from the table.
func (table *Table) Get(i int) Entry {
	return table.GetWithBase(i, table.Base())
}

// GetWithBase retrieves an entry relative to the specified base.
func (table *Table) GetWithBase(i int, base int) Entry {
	if i <= 0 {
		return nil
	}
	if i <= len(staticTable) {
		return staticTable[i-1]
	}
	dynIndex := i - len(staticTable) - 1 + table.Base() - base
	if dynIndex >= len(table.dynamic) {
		return nil
	}
	return table.dynamic[dynIndex]
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
	entry := dynamicEntry{name, value, table.inserts}
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

// Used returns the amount of capacity that is in use.
func (table *Table) Used() TableCapacity {
	return table.used
}

// Base returns the current base for the table, which is the number of inserts.
func (table *Table) Base() int {
	return table.inserts
}

// Lookup looks in the table for a matching name and value. This produces two
// return values: the first is match on both name and value, which is often nil.
// The second is a match on name only, which might also be nil.
func (table *Table) Lookup(name string, value string) (Entry, Entry) {
	var nameMatch Entry
	for _, entry := range staticTable {
		if entry.Name() == name {
			if entry.Value() == value {
				return entry, entry
			}
			if nameMatch == nil {
				nameMatch = entry
			}
		}
	}
	for _, entry := range table.dynamic {
		if entry.Name() == name {
			if entry.Value() == value {
				return entry, entry
			}
			if nameMatch == nil {
				nameMatch = entry
			}
		}
	}
	return nil, nameMatch
}
