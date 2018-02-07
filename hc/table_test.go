package hc_test

import (
	"testing"

	"github.com/martinthomson/minhq/hc"
	"github.com/stvp/assert"
)

func TestInsertOverflow(t *testing.T) {
	var table hc.Table
	table.SetCapacity(32)
	entry := table.Insert("name", "value")
	assert.Nil(t, table.Get(entry.Index(1)))
	assert.Equal(t, 1, table.Base())
}

func TestGetInvalid(t *testing.T) {
	var table hc.Table
	assert.Nil(t, table.Get(0))
	entry := table.Insert("name", "value")
	assert.NotNil(t, entry)
	nextIdx := entry.Index(table.Base()) + 1
	assert.Nil(t, table.Get(nextIdx))

	table.SetCapacity(100)
	entry = table.Insert("name", "value")
	assert.NotNil(t, entry)
	nextIdx = entry.Index(table.Base()) + 1
	assert.Nil(t, table.Get(nextIdx))
}

func TestInsertRetrieve(t *testing.T) {
	var table hc.Table
	table.SetCapacity(300)
	entry := table.Insert("name", "value")
	assert.Equal(t, entry, table.Get(entry.Index(table.Base())))
	m, nm := table.Lookup("name", "value")
	assert.Equal(t, entry, m)
	assert.Equal(t, entry, nm)
	m, nm = table.Lookup("name", "foo")
	assert.Nil(t, m)
	assert.Equal(t, entry, nm)
}

func TestBase(t *testing.T) {
	var table hc.Table
	table.SetCapacity(300)
	entry1 := table.Insert("name1", "value1")
	assert.Equal(t, 1, table.Base())
	dynamicOffset := entry1.Index(table.Base())
	assert.Equal(t, 62, dynamicOffset)
	entry2 := table.Insert("name2", "value2")
	assert.Equal(t, 2, table.Base())

	// Check that the table is in a reasonable state.
	retrieved1 := table.Get(dynamicOffset + 1)
	assert.Equal(t, entry1.Name(), retrieved1.Name())
	assert.Equal(t, entry1.Value(), retrieved1.Value())
	retrieved2 := table.Get(dynamicOffset)
	assert.Equal(t, entry2.Name(), retrieved2.Name())
	assert.Equal(t, entry2.Value(), retrieved2.Value())

	// Getting an index from a 0 base is never valid.
	assert.Equal(t, 0, entry1.Index(0))
	assert.Equal(t, 0, entry2.Index(0))

	// entry1 was added first, so it will be valid for a base of 1 or 2.
	assert.Equal(t, dynamicOffset, entry1.Index(1))
	assert.Equal(t, dynamicOffset+1, entry1.Index(2))

	// entry2 was added second, so it will only be valid for base 2.
	assert.Equal(t, 0, entry2.Index(1))
	assert.Equal(t, dynamicOffset, entry2.Index(2))
}

func TestInsertEvict(t *testing.T) {
	var table hc.Table
	table.SetCapacity(88) // Enough room for two values exactly.
	_ = table.Insert("name1", "value1")
	second := table.Insert("name2", "value2")
	third := table.Insert("name3", "value3")
	m, _ := table.Lookup("name1", "value1")
	assert.Nil(t, m)
	m, _ = table.Lookup("name2", "value2")
	assert.Equal(t, second, m)
	m, _ = table.Lookup("name3", "value3")
	assert.Equal(t, third, m)
}

func TestLookupStatic(t *testing.T) {
	var table hc.Table
	m, nm := table.Lookup(":method", "GET")
	assert.Equal(t, 2, m.Index(0))
	assert.Equal(t, 2, nm.Index(77))

	m, nm = table.Lookup(":method", "PATCH")
	assert.Nil(t, m)
	assert.Equal(t, 2, nm.Index(0))
}

func TestHpackZeroIndex(t *testing.T) {
	var table hc.Table
	assert.Nil(t, table.Get(0))
}
