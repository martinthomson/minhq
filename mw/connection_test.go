package mw_test

import (
	"net"
	"testing"

	"github.com/ekr/minq"
	"github.com/martinthomson/minhq/mw"
	"github.com/stvp/assert"
)

var clientAddr = &net.UDPAddr{IP: net.ParseIP("::1"), Port: 12589}
var serverAddr = &net.UDPAddr{IP: net.ParseIP("::1"), Port: 12590}

type Transport struct {
	read  <-chan []byte
	write chan<- []byte
}

func (t *Transport) Send(p []byte) error {
	t.write <- p
	return nil
}

func (t *Transport) Service(addr *net.UDPAddr, c chan<- *mw.Packet) {
	for {
		p, ok := <-t.read
		if !ok {
			return
		}
		c <- &mw.Packet{RemoteAddr: addr, Data: p}
	}
}

func (t *Transport) Close() error {
	close(t.write)
	return nil
}

func newTransportPair() (*Transport, *Transport) {
	a := make(chan []byte, 10)
	b := make(chan []byte, 10)
	return &Transport{a, b}, &Transport{b, a}
}

type simpleTransportFactory struct {
	t *Transport
}

func (tf *simpleTransportFactory) MakeTransport(remote *net.UDPAddr) (minq.Transport, error) {
	t := tf.t
	tf.t = nil
	return t, nil
}

type connectionHandler struct {
	streams chan *minq.Stream
}

func TestConnect(t *testing.T) {
	clientTransport, serverTransport := newTransportPair()
	defer clientTransport.Close()
	defer serverTransport.Close()

	serverConfig := minq.NewTlsConfig("localhost")
	server := mw.RunServer(minq.NewServer(&simpleTransportFactory{serverTransport}, &serverConfig, nil))
	defer server.Close()
	go serverTransport.Service(clientAddr, server.IncomingPackets)

	clientConfig := minq.NewTlsConfig("localhost")
	clientConnection := mw.NewConnection(minq.NewConnection(clientTransport, minq.RoleClient, &clientConfig, nil))
	defer clientConnection.Close()
	go clientTransport.Service(serverAddr, clientConnection.IncomingPackets)

	clientConnectionConnected := <-clientConnection.Connected
	assert.Equal(t, clientConnection, clientConnectionConnected)

	serverConnection := <-server.Connections
	defer serverConnection.Close()

	cstr := clientConnection.CreateStream()
	out := []byte{1, 2, 3}
	n, err := cstr.Write(out)
	assert.Nil(t, err)
	assert.Equal(t, 3, n)

	sstr := <-serverConnection.RemoteStreams
	assert.Equal(t, cstr.Id(), sstr.Id())

	in := make([]byte, len(out))
	n, err = sstr.Read(in)
	assert.Nil(t, err)
	assert.Equal(t, 3, n)
	assert.Equal(t, out, in)

	/*n, err = sstr.Read(in)
	assert.Nil(t, err) */
}
