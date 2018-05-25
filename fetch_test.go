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

type clientServer struct {
	cs     *test.ClientServer
	server *minhq.Server
	client *minhq.ClientConnection
}

func (cs *clientServer) Close() error {
	return cs.Close()
}

func newClientServerPair(t *testing.T) *clientServer {
	config := &minhq.Config{
		DecoderTableCapacity: 4096,
		ConcurrentDecoders:   10,
	}
	var server *minhq.Server
	cs := test.NewClientServerPair(func(ms *minq.Server) *mw.Server {
		server = minhq.RunServer(ms, config)
		return &server.Server
	}, func(ms *mw.Server) *mw.Connection {
		assert.Equal(t, &server.Server, ms)
		serverConnection := <-server.Connections
		return &serverConnection.Connection
	})
	client := minhq.NewClientConnection(cs.ClientConnection, config)
	return &clientServer{cs, server, client}
}

func TestFetch(t *testing.T) {
	cs := newClientServerPair(t)
	//defer cs.Close()

	url := "https://example.com/%2fhello"
	clientRequest, err := cs.client.Fetch("GET", url,
		hc.HeaderField{Name: "User-Agent", Value: "Test"})
	assert.Nil(t, err)
	assert.Nil(t, clientRequest.Close())

	serverRequest := <-cs.server.Requests
	assert.Equal(t, "Test", serverRequest.GetHeader("user-AGENT"))
	assert.Equal(t, url, serverRequest.Target.String())
	_, err = io.Copy(ioutil.Discard, serverRequest)
	assert.Nil(t, err)
	assert.Nil(t, <-serverRequest.Trailers)

	serverResponse, err := serverRequest.Respond(200,
		hc.HeaderField{Name: "Content-Type", Value: "text/plain"})
	assert.Nil(t, err)
	_, err = io.Copy(serverResponse, strings.NewReader("Hello World"))
	assert.Nil(t, err)
	assert.Nil(t, serverResponse.Close())

	clientResponse := <-clientRequest.Response
	assert.Equal(t, 200, clientResponse.Status)
}
