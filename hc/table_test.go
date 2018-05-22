package hc_test

import (
	"testing"

	"github.com/martinthomson/minhq/hc"
	"github.com/stvp/assert"
)

func TestInsertOverflow(t *testing.T) {
	var table hc.HpackTable
	table.SetCapacity(10)
	assert.NotNil(t, table.Insert("name", "value", nil))
}

func TestGetInvalid(t *testing.T) {
	var table hc.HpackTable

	assert.Nil(t, table.Get(0))

	table.SetCapacity(100)
	e := table.Insert("name", "value", nil)
	assert.Equal(t, e, table.Get(table.Index(e)))
	assert.Equal(t, 1, table.Base())
	idx := table.Index(e)
	// asking for one more fails
	assert.Nil(t, table.Get(idx+1))
	// so does asking for the entry with a lower base
	assert.Nil(t, table.GetDynamic(idx, table.Base()-1))
}

func TestInsertRetrieve(t *testing.T) {
	var table hc.HpackTable
	table.SetCapacity(300)
	e := table.Insert("name", "value", nil)

	assert.Equal(t, e, table.Get(table.Index(e)))

	m, nm := table.Lookup("name", "value")
	assert.Equal(t, e, m)
	assert.Equal(t, e, nm)
	m, nm = table.Lookup("name", "foo")
	assert.Nil(t, m)
	assert.Equal(t, e, nm)
}

func TestBase(t *testing.T) {
	var table hc.HpackTable
	table.SetCapacity(300)

	e1 := table.Insert("name1", "value1", nil)
	assert.Equal(t, 1, table.Base())
	e2 := table.Insert("name2", "value2", nil)
	assert.Equal(t, 2, table.Base())

	// Check that the table is in a reasonable state.
	retrieved1 := table.GetDynamic(1, table.Base())
	assert.Equal(t, e1.Name(), retrieved1.Name())
	assert.Equal(t, e1.Value(), retrieved1.Value())
	assert.Equal(t, table.Index(e1), e1.Index(table.Base()))
	retrieved2 := table.GetDynamic(0, table.Base())
	assert.Equal(t, e2.Name(), retrieved2.Name())
	assert.Equal(t, e2.Value(), retrieved2.Value())
	assert.Equal(t, table.Index(e1), e1.Index(table.Base()))
	assert.Equal(t, table.Index(e2)+1, table.Index(e1))

	// entry1 was added first, so it will be valid for a base of 1 or 2.
	dynamicBase := e1.Index(e1.Base())
	assert.Equal(t, 62, dynamicBase)
	assert.Equal(t, dynamicBase+1, table.Index(e1))
	assert.Equal(t, dynamicBase+1, e1.Index(table.Base()))

	// entry2 was added second, so it will only be valid for base 2.
	assert.Equal(t, 0, e2.Index(1))
	assert.Equal(t, dynamicBase, e2.Index(2))
}

func TestInsertEvict(t *testing.T) {
	var table hc.HpackTable
	table.SetCapacity(86) // Enough room for two values.
	assert.NotNil(t, table.Insert("name1", "value1", nil))
	second := table.Insert("name2", "value2", nil)
	third := table.Insert("name3", "value3", nil)
	m, _ := table.Lookup("name1", "value1")
	assert.Nil(t, m)
	m, _ = table.Lookup(second.Name(), second.Value())
	assert.Equal(t, second, m)
	m, _ = table.Lookup(third.Name(), third.Value())
	assert.Equal(t, third, m)
}

func TestLookupStatic(t *testing.T) {
	var table hc.HpackTable
	m, nm := table.Lookup(":method", "GET")
	assert.Equal(t, 2, m.Base())
	assert.Equal(t, 2, nm.Base())

	m, nm = table.Lookup(":method", "PATCH")
	assert.Nil(t, m)
	assert.Equal(t, 2, nm.Base())
}
