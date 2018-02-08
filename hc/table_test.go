package hc_test

import (
	"testing"

	"github.com/martinthomson/minhq/hc"
	"github.com/stvp/assert"
)

type testEntry struct {
	hc.BasicDynamicEntry
}

func entry(name string, value string) hc.DynamicEntry {
	return &testEntry{hc.BasicDynamicEntry{N: name, V: value}}
}

// Use a fixed size to keep things simple.
func (e testEntry) Size() hc.TableCapacity {
	return 32
}

func TestInsertOverflow(t *testing.T) {
	var table hc.Table
	table.SetCapacity(10)
	assert.False(t, table.Insert(entry("name", "value"), nil))
}

func TestGetInvalid(t *testing.T) {
	var table hc.Table

	assert.Nil(t, table.Get(0))

	nextIdx := table.LastIndex(table.Base()) + 1
	assert.Nil(t, table.Get(nextIdx))

	table.SetCapacity(100)
	e := entry("name", "value")
	assert.True(t, table.Insert(e, nil))
	nextIdx = e.Index(table.Base()) + 1
	assert.Nil(t, table.Get(nextIdx))
}

func TestInsertRetrieve(t *testing.T) {
	var table hc.Table
	table.SetCapacity(300)
	e := entry("name", "value")
	assert.True(t, table.Insert(e, nil))

	assert.Equal(t, e, table.Get(e.Index(table.Base())))

	m, nm := table.Lookup("name", "value")
	assert.Equal(t, e, m)
	assert.Equal(t, e, nm)
	m, nm = table.Lookup("name", "foo")
	assert.Nil(t, m)
	assert.Equal(t, e, nm)
}

func TestBase(t *testing.T) {
	var table hc.Table
	table.SetCapacity(300)

	dynamicOffset := table.LastIndex(table.Base()) + 1
	assert.Equal(t, 62, dynamicOffset)

	e1 := entry("name1", "value1")
	assert.True(t, table.Insert(e1, nil))
	assert.Equal(t, 1, table.Base())
	e2 := entry("name2", "value2")
	assert.True(t, table.Insert(e2, nil))
	assert.Equal(t, 2, table.Base())

	// Check that the table is in a reasonable state.
	retrieved1 := table.Get(dynamicOffset + 1)
	assert.Equal(t, e1.Name(), retrieved1.Name())
	assert.Equal(t, e1.Value(), retrieved1.Value())
	retrieved2 := table.Get(dynamicOffset)
	assert.Equal(t, e2.Name(), retrieved2.Name())
	assert.Equal(t, e2.Value(), retrieved2.Value())

	// Getting an index from a 0 base is never valid.
	assert.Equal(t, 0, e1.Index(0))
	assert.Equal(t, 0, e2.Index(0))

	// entry1 was added first, so it will be valid for a base of 1 or 2.
	assert.Equal(t, dynamicOffset, e1.Index(1))
	assert.Equal(t, dynamicOffset+1, e1.Index(2))

	// entry2 was added second, so it will only be valid for base 2.
	assert.Equal(t, 0, e2.Index(1))
	assert.Equal(t, dynamicOffset, e2.Index(2))
}

func TestInsertEvict(t *testing.T) {
	var table hc.Table
	table.SetCapacity(64) // Enough room for two values exactly.
	assert.True(t, table.Insert(entry("name1", "value1"), nil))
	second := entry("name2", "value2")
	assert.True(t, table.Insert(second, nil))
	third := entry("name3", "value3")
	assert.True(t, table.Insert(third, nil))
	m, _ := table.Lookup("name1", "value1")
	assert.Nil(t, m)
	m, _ = table.Lookup(second.Name(), second.Value())
	assert.Equal(t, second, m)
	m, _ = table.Lookup(third.Name(), third.Value())
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
