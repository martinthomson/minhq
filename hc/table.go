package hc

// Entry is a key-value pair for the HPACK table.
type Entry interface {
	Name() string
	Value() string
	Index(base int) int
}

// DynamicEntry is an entry in the dynamic table.
type DynamicEntry interface {
	Entry
	Base() int
	Size() TableCapacity
}

// TableCapacity is the type of the HPACK table capacity.
type TableCapacity uint

// Table holds dynamic entries and accounting for space.
type Table struct {
	dynamic []DynamicEntry
	// The total capacity (in HPACK bytes) of the table. This is set by
	// configuration.
	capacity TableCapacity
	// The amount of used capacity.
	used TableCapacity
	// The total number of inserts thus far.
	inserts int

	// This is used to make new entries.
	dynamicMaker func(string, string, int) DynamicEntry
}

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

func defaultDynamicMaker(name string, value string, base int) DynamicEntry {
	return &hpackEntry{dynamicEntry{name, value, base}}
}

// Insert an entry into the table.
func (table *Table) Insert(name string, value string) Entry {
	table.inserts++
	if table.dynamicMaker == nil {
		table.dynamicMaker = defaultDynamicMaker
	}
	entry := table.dynamicMaker(name, value, table.Base())
	if entry.Size() > table.capacity {
		table.dynamic = table.dynamic[0:0]
		table.used = 0
	} else {
		table.evictTo(table.capacity - entry.Size())
		tmp := make([]DynamicEntry, len(table.dynamic)+1)
		copy(tmp[1:], table.dynamic)
		tmp[0] = entry
		table.dynamic = tmp
		table.used += entry.Size()
	}
	return entry
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

func (table *Table) lookupStatic(name string, value string) (Entry, Entry) {
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
	return nil, nameMatch
}

// Lookup looks in the table for a matching name and value. This produces two
// return values: the first is match on both name and value, which is often nil.
// The second is a match on name only, which might also be nil.
func (table *Table) Lookup(name string, value string) (Entry, Entry) {
	match, nameMatch := table.lookupStatic(name, value)
	if match != nil {
		return match, nameMatch
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
