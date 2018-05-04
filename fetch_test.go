package minhq_test

import (
	"io"
	"io/ioutil"
	"strings"
	"testing"

	"github.com/ekr/minq"

	"github.com/martinthomson/minhq"
	"github.com/martinthomson/minhq/hc"
	"github.com/martinthomson/minhq/mw"
	"github.com/martinthomson/minhq/mw/test"
	"github.com/stvp/assert"
)

func TestFetch(t *testing.T) {
	config := &minhq.Config{DecoderTableCapacity: 4096}
	var server *minhq.Server
	cs := test.NewClientServerPair(func(ms *minq.Server) *mw.Server {
		server = minhq.RunServer(ms, config)
		return &server.Server
	}, func(ms *mw.Server) *mw.Connection {
		assert.Equal(t, &server.Server, ms)
		serverConnection := <-server.Connections
		return &serverConnection.Connection
	})

	clientConnection := minhq.NewClientConnection(cs.ClientConnection, config)
	clientRequest, err := clientConnection.Fetch("GET", "https://example.com/hello",
		hc.HeaderField{Name: "User-Agent", Value: "Test"})
	assert.Nil(t, err)
	assert.Nil(t, clientRequest.Close())

	println("request out")

	serverRequest := <-server.Requests
	assert.Equal(t, "Test", serverRequest.GetHeader("user-AGENT"))
	// TODO get a reconstructed URL from the request
	_, err = io.Copy(ioutil.Discard, serverRequest)
	assert.Nil(t, err)
	assert.Nil(t, <-serverRequest.Trailers)

	serverResponse, err := serverRequest.Respond(200,
		hc.HeaderField{Name: "Content-Type", Value: "text/plain"})
	assert.Nil(t, err)
	println("response headers out")
	_, err = io.Copy(serverResponse, strings.NewReader("Hello World"))
	assert.Nil(t, err)
	assert.Nil(t, serverResponse.Close())

	clientResponse := <-clientRequest.Response
	assert.Equal(t, 200, clientResponse.Status)
}
