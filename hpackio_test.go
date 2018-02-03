package minhq_test

import (
	"bytes"
	"testing"

	"github.com/martinthomson/minhq"
	"github.com/stvp/assert"
)

func TestReadInt(t *testing.T) {
	buf := bytes.NewReader([]byte{0x0a})
	reader := minhq.NewHpackReader(buf)
	i, err := reader.ReadInt(8)
	assert.Nil(t, err)
	assert.Equal(t, uint64(10), i)
}
