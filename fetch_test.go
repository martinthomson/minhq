package minhq_test

import (
	"bytes"
	"io"
	"io/ioutil"
	"strings"
	"sync"
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
	return cs.cs.Close()
}

func newClientServerPair(t *testing.T) *clientServer {
	config := &minhq.Config{
		DecoderTableCapacity: 4096,
		ConcurrentDecoders:   10,
		MaxConcurrentPushes:  10,
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
	assert.Nil(t, client.Connect())
	return &clientServer{cs, server, client}
}

func TestFetch(t *testing.T) {
	cs := newClientServerPair(t)
	defer cs.Close()

	url := "https://example.com/%2fhello"
	clientRequest, err := cs.client.Fetch("GET", url,
		hc.HeaderField{Name: "User-Agent", Value: "Test"},
	)
	assert.Nil(t, err)
	assert.Nil(t, clientRequest.Close())

	serverRequest := <-cs.server.Requests
	assert.Equal(t, "Test", serverRequest.GetHeader("user-AGENT"))
	assert.Equal(t, "GET", serverRequest.Method())
	assert.Equal(t, url, serverRequest.Target().String())
	_, err = io.Copy(ioutil.Discard, serverRequest)
	assert.Nil(t, err)
	assert.Nil(t, <-serverRequest.Trailers)

	serverResponse, err := serverRequest.Respond(200,
		hc.HeaderField{Name: "Content-Type", Value: "text/plain"})
	assert.Nil(t, err)
	contentString := "Hello World"
	_, err = io.Copy(serverResponse, strings.NewReader(contentString))
	assert.Nil(t, err)
	assert.Nil(t, serverResponse.Close())

	clientResponse := clientRequest.Response()
	assert.Equal(t, 200, clientResponse.Status)
	var body bytes.Buffer
	_, err = io.Copy(&body, clientResponse)
	assert.Nil(t, err)
	bodyString, err := body.ReadString(0)
	assert.Equal(t, contentString, bodyString)
}

func Test1xx(t *testing.T) {
	cs := newClientServerPair(t)
	defer cs.Close()

	url := "https://example.com/1xx"
	clientRequest, err := cs.client.Fetch("GET", url)
	assert.Nil(t, err)
	assert.Nil(t, clientRequest.Close())

	serverRequest := <-cs.server.Requests
	assert.Equal(t, "GET", serverRequest.Method())
	assert.Equal(t, url, serverRequest.Target().String())
	_, err = io.Copy(ioutil.Discard, serverRequest)
	assert.Nil(t, err)
	assert.Nil(t, <-serverRequest.Trailers)

	serverResponse, err := serverRequest.Respond(103,
		hc.HeaderField{Name: "Link", Value: "</data>;rel=\"preload\""},
	)
	assert.Nil(t, serverResponse)

	serverResponse, err = serverRequest.Respond(200,
		hc.HeaderField{Name: "Content-Type", Value: "text/plain"},
	)
	assert.Nil(t, err)
	assert.Nil(t, serverResponse.Close())

	clientResponse := clientRequest.Response()
	assert.Equal(t, 200, clientResponse.Status)
}

var (
	pushMessage     = []byte("this is a push")
	responseMessage = []byte("this is a response")
)

func TestPushOnRequest(t *testing.T) {
	cs := newClientServerPair(t)
	defer cs.Close()

	url := "https://example.com/push"
	clientRequest, err := cs.client.Fetch("POST", url)
	assert.Nil(t, err)
	assert.Nil(t, clientRequest.Close())

	serverRequest := <-cs.server.Requests

	serverPromise, err := serverRequest.Push("GET", "/other",
		hc.HeaderField{Name: "Push-ID", Value: "123"},
	)
	assert.Nil(t, err)
	serverPushResponse, err := serverPromise.Respond(200, hc.HeaderField{Name: "Push-ID", Value: "123"})
	assert.Nil(t, err)
	_, err = serverPushResponse.Write(pushMessage)
	assert.Nil(t, err)
	assert.Nil(t, serverPushResponse.Close())

	promise := <-clientRequest.Pushes
	assert.Equal(t, promise.Target().String(), "https://example.com/other")

	serverResponse, err := serverRequest.Respond(500)
	assert.Nil(t, err)
	_, err = serverResponse.Write(responseMessage)
	assert.Nil(t, err)
	assert.Nil(t, serverResponse.Close())

	// From this point, we receive the promised response in parallel to the
	// response, so we're into WaitGroup territory...
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer t.Logf("promise reading done")
		defer wg.Done()
		clientPushResponse := promise.Response()
		assert.Equal(t, clientPushResponse.Status, 200)
		var buf bytes.Buffer
		_, err := io.Copy(&buf, clientPushResponse)
		assert.Nil(t, err)
		assert.Equal(t, buf.Bytes(), pushMessage)
	}()

	go func() {
		defer t.Logf("request reading done")
		defer wg.Done()
		response := clientRequest.Response()
		assert.Equal(t, response.Status, 500)
		var buf bytes.Buffer
		_, err := io.Copy(&buf, response)
		assert.Nil(t, err)
		assert.Equal(t, buf.Bytes(), responseMessage)
	}()

	wg.Wait()
}
