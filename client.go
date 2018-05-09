package minhq

import (
	"errors"
	"io"
	"net"
	"net/url"
	"strings"

	"github.com/ekr/minq"
	"github.com/martinthomson/minhq/hc"
	"github.com/martinthomson/minhq/mw"
)

// Client is the top-level thing that makes connections to servers and makes
// requests.
type Client struct {
	Connections map[string]*ClientConnection
	Config      Config
}

// Connect to a given host.
func (c *Client) Connect(host string) (*ClientConnection, error) {
	var serverName string
	if strings.ContainsRune(host, 58 /* ":" */) {
		var err error
		serverName, _, err = net.SplitHostPort(host)
		if err != nil {
			return nil, err
		}
	} else {
		serverName = host
		host += ":443"
	}
	addr, err := net.ResolveUDPAddr("udp", host)
	if err != nil {
		return nil, err
	}
	socket, err := net.ListenUDP("udp", nil) // ephemeral ports
	if err != nil {
		return nil, err
	}

	minqTransport := minq.NewUdpTransport(socket, addr)
	minqConfig := minq.NewTlsConfig(serverName)
	minq := minq.NewConnection(minqTransport, minq.RoleClient,
		&minqConfig, nil)
	connection := NewClientConnection(mw.NewConnection(minq), &c.Config)
	if c.Connections == nil {
		c.Connections = make(map[string]*ClientConnection)
	}

	go serviceUdpSocket(connection.IncomingPackets, socket, connection)

	c.Connections[host] = connection
	return connection, nil
}

func serviceUdpSocket(packets chan<- *mw.Packet, socket *net.UDPConn,
	resource io.Closer) {
	defer resource.Close()

	laddr := socket.LocalAddr()
	localAddr, err := net.ResolveUDPAddr(laddr.Network(), laddr.String())
	if err != nil {
		panic("can't get UDP address of UDP socket")
	}

	for {
		buf := make([]byte, 4096) // Increase to support larger MTU
		n, remoteAddr, err := socket.ReadFromUDP(buf)
		if err != nil {
			return
		}
		packets <- &mw.Packet{
			LocalAddr:  localAddr,
			RemoteAddr: remoteAddr,
			Data:       buf[:n],
		}
	}
}

// Fetch is the basic client request handling function.  This isn't safe to
// run concurrently, and it blocks.  If you need to make requests concurrently,
// find the connection you need and use that directly.
func (c *Client) Fetch(method string, target string, headers ...hc.HeaderField) (*ClientRequest, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "https" {
		return nil, errors.New("only the 'https' scheme is supported")
	}

	connection := c.Connections[u.Host]
	if connection == nil {
		connection, err = c.Connect(u.Host)
		if err != nil {
			return nil, err
		}
	}
	return connection.Fetch(method, target, headers...)
}

// Close closes all connections indiscriminately.
func (c *Client) Close() error {
	for _, conn := range c.Connections {
		_ = conn.Close()
	}
	return nil
}
