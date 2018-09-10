package hc

const entryOverhead = TableCapacity(32)

// Entry is a key-value pair for the HPACK table.
type Entry interface {
	Name() string
	Value() string
	Base() int
}

// DynamicEntry is an entry in the dynamic table.
type DynamicEntry interface {
	Entry
	setBase(int)
	Size() TableCapacity
	Index(base int) int
}

// BasicDynamicEntry is a skeleton implementation of DynamicEntry.
type BasicDynamicEntry struct {
	N string // name
	V string // value
	B int    // base
}

// Base provides the base index for when this entry was inserted.
func (hd BasicDynamicEntry) Base() int {
	return hd.B
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

// Index returns the index of this entry relative to the specified base.
func (hd *BasicDynamicEntry) Index(base int) int {
	return base - hd.B
}

// String turns this into something presentable.
func (hd *BasicDynamicEntry) String() string {
	return hd.N + ": " + hd.V
}

// Size returns the size of the dynamic entry.
func (hd *BasicDynamicEntry) Size() TableCapacity {
	return entryOverhead + TableCapacity(len(hd.N)+len(hd.V))
}

// TableCapacity is the type of the HPACK table capacity.
type TableCapacity uint

type evictionCheck interface {
	CanEvict(DynamicEntry) bool
}

// Table is the basic interface to a header compression table.
type Table interface {
	// Base returns the current table base index.
	Base() int
	// Index returns the index of the given entry relative to the current
	// base.  For tables with split spaces (QPACK), this returns the index
	// in the space that the entry is relevant to.  For tables with a
	// single space (HPACK), the index is unique.
	Index(Entry) int
	GetStatic(i int) Entry
	GetDynamic(i int, base int) Entry
	Insert(name string, value string, evict evictionCheck) DynamicEntry
	Capacity() TableCapacity
	SetCapacity(TableCapacity)
	Used() TableCapacity
	// Lookup looks in the table for a matching name and value. This produces two
	// return values: the first is match on both name and value, which is often nil.
	// The second is a match on name only, which might also be nil.
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
	// The total number of inserts thus far.
	base int
	// Retrieve a static table entry.
	getStatic func(int) Entry
}

// Base returns the current base for the table, which is the number of inserts.
func (table *tableCommon) Base() int {
	return table.base
}

// GetDynamic retrieves a dynamic table entry using zero-based indexing from the base.
func (table *tableCommon) GetDynamic(i int, base int) Entry {
	delta := table.Base() - base
	if delta < 0 {
		return nil
	}
	dynIndex := i + delta
	if dynIndex >= len(table.dynamic) || dynIndex < 0 {
		return nil
	}
	return table.dynamic[dynIndex]
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

// SetCapacity increases or reduces capacity to the set target.
func (table *tableCommon) SetCapacity(capacity TableCapacity) {
	table.evictTo(capacity, nil)
	table.capacity = capacity
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

func (table *tableCommon) lookupImpl(staticTable []staticTableEntry, name string, value string, dynamicMin int, dynamicMax int) (Entry, Entry) {
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
	for _, entry := range table.dynamic[dynamicMin:dynamicMax] {
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
