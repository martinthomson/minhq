package mw_test

import (
	"testing"

	"github.com/martinthomson/minhq/mw"
	"github.com/martinthomson/minhq/mw/test"
	"github.com/stvp/assert"
)

func TestConnect(t *testing.T) {
	cs := test.NewClientServerPair(mw.RunServer)
	defer cs.Close()

	cstr := cs.ClientConnection.CreateStream()
	out := []byte{1, 2, 3}
	n, err := cstr.Write(out)
	assert.Nil(t, err)
	assert.Equal(t, 3, n)

	sstr := <-cs.ServerConnection.RemoteStreams
	assert.Equal(t, cstr.Id(), sstr.Id())

	in := make([]byte, len(out))
	n, err = sstr.Read(in)
	assert.Nil(t, err)
	assert.Equal(t, 3, n)
	assert.Equal(t, out, in)
}
