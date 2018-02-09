package io_test

import (
	"io"
	"testing"

	minhqio "github.com/martinthomson/minhq/io"
	"github.com/stvp/assert"
)

func TestAsyncReader(t *testing.T) {
	ar := minhqio.NewAsyncReader()
	done := make(chan struct{})
	go func() {
		ar.Send([]byte{1, 2})

		ar.Send([]byte{3})

		ar.Send([]byte{4})
		ar.Send([]byte{5})

		ar.Send([]byte{6})
		ar.Close()

		done <- struct{}{}
	}()

	p := [2]byte{}

	n, err := ar.Read(p[:])
	assert.Nil(t, err)
	assert.Equal(t, []byte{1, 2}, p[0:n])

	n, err = ar.Read(p[:])
	assert.Nil(t, err)
	assert.Equal(t, []byte{3}, p[0:n])

	n, err = io.ReadFull(ar, p[:])
	assert.Nil(t, err)
	assert.Equal(t, []byte{4, 5}, p[0:n])

	n, err = io.ReadFull(ar, p[:])
	assert.Equal(t, io.ErrUnexpectedEOF, err)
	assert.Equal(t, []byte{6}, p[0:n])

	<-done
}
