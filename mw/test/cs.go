package test

import (
	"net"

	"github.com/ekr/minq"
	"github.com/martinthomson/minhq/mw"
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

type simpleTransportFactory struct {
	t *Transport
}

func (tf *simpleTransportFactory) MakeTransport(remote *net.UDPAddr) (minq.Transport, error) {
	t := tf.t
	tf.t = nil
	return t, nil
}

type ClientServer struct {
	ClientConnection *mw.Connection
	ServerConnection *mw.Connection
	Server           *mw.Server

	clientTransport *Transport
	serverTransport *Transport
}

func NewClientServerPair(runServerFunc func(*minq.Server) *mw.Server) *ClientServer {
	cs := &ClientServer{}

	a := make(chan []byte, 10)
	b := make(chan []byte, 10)
	cs.clientTransport = &Transport{a, b}
	cs.serverTransport = &Transport{b, a}

	serverConfig := minq.NewTlsConfig("localhost")
	cs.Server = runServerFunc(minq.NewServer(&simpleTransportFactory{cs.serverTransport}, &serverConfig, nil))
	go cs.serverTransport.Service(clientAddr, cs.Server.IncomingPackets)

	clientConfig := minq.NewTlsConfig("localhost")
	cs.ClientConnection = mw.NewConnection(minq.NewConnection(cs.clientTransport, minq.RoleClient, &clientConfig, nil))
	go cs.clientTransport.Service(serverAddr, cs.ClientConnection.IncomingPackets)

	if cs.ClientConnection != <-cs.ClientConnection.Connected {
		cs.Close()
		panic("got a different client connection at the server")
	}

	cs.ServerConnection = <-cs.Server.Connections
	return cs
}

// Close releases all the resources for the pair.
func (cs *ClientServer) Close() error {
	if cs.ClientConnection != nil {
		cs.ClientConnection.Close()
	}
	if cs.ServerConnection != nil {
		cs.ServerConnection.Close()
	}
	if cs.Server != nil {
		cs.Server.Close()
	}
	if cs.serverTransport != nil {
		cs.serverTransport.Close()
	}
	if cs.clientTransport != nil {
		cs.clientTransport.Close()
	}
	return nil
}
