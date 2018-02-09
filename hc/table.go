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
	setBase(int)
	Base() int
	Size() TableCapacity
}

// BasicDynamicEntry is a skeleton implementation of DynamicEntry.
type BasicDynamicEntry struct {
	N string // name
	V string // value
	B int    // base
}

// Index provides the table index relative to the specified base.
func (hd BasicDynamicEntry) Index(base int) int {
	delta := base - hd.B
	if delta < 0 {
		// If base < inserts, then this entry was added after the base and the index
		// will be invalid. Return 0.
		return 0
	}
	// If base > inserts, then this entry was added before the base was set. The
	// index is be valid.
	return len(staticTable) + 1 + delta
}

// Name is self-explanatory.
func (hd BasicDynamicEntry) Name() string {
	return hd.N
}

// Value is self-explanatory.
func (hd BasicDynamicEntry) Value() string {
	return hd.V
}

// setBase sets the base for this entry.  This is set at the point of insertion.
func (hd *BasicDynamicEntry) setBase(base int) {
	hd.B = base
}

// Base is the number of inserts at the point that this entry was inserted in the table.
func (hd BasicDynamicEntry) Base() int {
	return hd.B
}

// TableCapacity is the type of the HPACK table capacity.
type TableCapacity uint

type evictionCheck interface {
	CanEvict(DynamicEntry) bool
}

// Table is the basic interface to a header compression table.
type Table interface {
	LastIndex(base int) int
	GetWithBase(i int, base int) Entry
	Get(i int) Entry
	Insert(name string, value string, evict evictionCheck) DynamicEntry
	Capacity() TableCapacity
	Base() int
	Used() TableCapacity
	Lookup(name string, value string) (Entry, Entry)
}

// Table holds dynamic entries and accounting for space.
type tableCommon struct {
	dynamic []DynamicEntry
	// The total capacity (in HPACK bytes) of the table. This is set by
	// configuration.
	capacity TableCapacity
	// The amount of used capacity.
	used TableCapacity
	// The total number of base thus far.
	base int
}

// LastIndex returns the index of the last entry in the table. Indices greater
// than this have been evicted.
func (table *tableCommon) LastIndex(base int) int {
	if table.dynamic == nil {
		return len(staticTable)
	}
	return table.dynamic[len(table.dynamic)-1].Index(base)
}

// GetWithBase retrieves an entry relative to the specified base.
func (table *tableCommon) GetWithBase(i int, base int) Entry {
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

// Get an entry from the table.
func (table *tableCommon) Get(i int) Entry {
	return table.GetWithBase(i, table.base)
}

// Evict entries until the used capacity is less than the reduced capacity.
func (table *tableCommon) evictTo(reduced TableCapacity, evict evictionCheck) bool {
	l := len(table.dynamic)
	used := table.used
	for l > 0 && used > reduced {
		l--
		if evict != nil && !evict.CanEvict(table.dynamic[l]) {
			return false
		}
		used -= table.dynamic[l].Size()
	}
	table.dynamic = table.dynamic[0:l]
	table.used = used
	return true
}

// Insert an entry into the table.  Return nil if the entry couldn't be added.
func (table *tableCommon) insert(entry DynamicEntry, evict evictionCheck) bool {
	if entry.Size() > table.capacity {
		if table.evictTo(0, evict) {
			table.dynamic = table.dynamic[0:0]
			table.used = 0
		}
		return false
	}

	if !table.evictTo(table.capacity-entry.Size(), evict) {
		return false
	}

	table.base++
	entry.setBase(table.base)

	// TODO This is grossly inefficient. Indexing from the other end might be less
	// bad, especially if the underlying array is made a little bigger than needed
	// when resizing.
	tmp := make([]DynamicEntry, len(table.dynamic)+1)
	copy(tmp[1:], table.dynamic)
	tmp[0] = entry
	table.dynamic = tmp
	table.used += entry.Size()
	return true
}

// Capacity returns the maximum capacity of the table.
func (table *tableCommon) Capacity() TableCapacity {
	return table.capacity
}

// Used returns the amount of capacity that is in use.
func (table *tableCommon) Used() TableCapacity {
	return table.used
}

// Base returns the current base for the table, which is the number of inserts.
func (table *tableCommon) Base() int {
	return table.base
}

func (table *tableCommon) lookupImpl(name string, value string, dynamicLimit int) (Entry, Entry) {
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
	for _, entry := range table.dynamic[0:dynamicLimit] {
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
func (table *tableCommon) Lookup(name string, value string) (Entry, Entry) {
	return table.lookupImpl(name, value, len(table.dynamic))
}
