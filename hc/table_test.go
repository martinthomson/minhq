package hc_test

import (
	"testing"

	"github.com/martinthomson/minhq/hc"
	"github.com/stvp/assert"
)

func TestHpackInsertOverflow(t *testing.T) {
	var table hc.Table
	table.SetCapacity(32)
	entry := table.Insert("name", "value")
	assert.Nil(t, table.Get(entry.Index()))
}

func TestHpackInsertRetrieve(t *testing.T) {
	var table hc.Table
	table.SetCapacity(300)
	entry := table.Insert("name", "value")
	assert.Equal(t, entry, table.Get(entry.Index()))
	m, nm := table.Lookup("name", "value")
	assert.Equal(t, entry, m)
	assert.Equal(t, entry, nm)
	m, nm = table.Lookup("name", "foo")
	assert.Nil(t, m)
	assert.Equal(t, entry, nm)
}

func TestHpackInsertEvict(t *testing.T) {
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

func TestHpackLookupStatic(t *testing.T) {
	var table hc.Table
	m, nm := table.Lookup(":method", "GET")
	assert.Equal(t, 2, m.Index())
	assert.Equal(t, 2, nm.Index())

	m, nm = table.Lookup(":method", "PATCH")
	assert.Nil(t, m)
	assert.Equal(t, 2, nm.Index())
}

func TestHpackZeroIndex(t *testing.T) {
	var table hc.Table
	assert.Nil(t, table.Get(0))
}
