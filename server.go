package minhq

import (
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"

	"github.com/ekr/minq"
	"github.com/martinthomson/minhq/mw"
)

// Server is a basic HTTP server.  New requests are emitted from the Requests channel.
type Server struct {
	mw.Server
	config *Config

	// Incoming requests are the primary purpose of this API.
	Requests <-chan *ServerRequest

	// Connections holds the connections that have been established.
	// Users of this API should ignore these with a simple goroutine like
	// `go func() { for <-server.Connections != nil {} }()` unless they
	// need direct access to the connection.
	Connections <-chan *ServerConnection
}

func (s *Server) serviceConnections(requests chan<- *ServerRequest, connections chan<- *ServerConnection) {
	for c := range s.Server.Connections {
		connections <- newServerConnection(c, s.config, requests)
	}
}

// RunServer takes a minq Server and starts the various goroutines that service it.
// Run Listen() for a basic server.
func RunServer(ms *minq.Server, config *Config) *Server {
	requests := make(chan *ServerRequest)
	connections := make(chan *ServerConnection)
	s := &Server{
		Server:      *mw.RunServer(ms),
		config:      config,
		Requests:    requests,
		Connections: connections,
	}

	go s.serviceConnections(requests, connections)
	return s
}

func loadCert(config *minq.TlsConfig, certfile string, keyfile string) error {
	cert, err := tls.LoadX509KeyPair(certfile, keyfile)
	if err != nil {
		return err
	}
	config.CertificateChain = make([]*x509.Certificate, len(cert.Certificate))
	for i, b := range cert.Certificate {
		config.CertificateChain[i], err = x509.ParseCertificate(b)
		if err != nil {
			return err
		}
	}
	privateKey, ok := cert.PrivateKey.(crypto.Signer)
	if !ok {
		return errors.New("Private key isn't suitable for signing")
	}
	config.Key = privateKey
	return nil
}

// Listen creates and starts a simple server.
func Listen(host string, certfile string, keyfile string, config *Config) (*Server, error) {
	serverName, _, err := net.SplitHostPort(host)
	if err != nil {
		return nil, err
	}
	addr, err := net.ResolveUDPAddr("udp", host)
	if err != nil {
		return nil, err
	}
	sock, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, err
	}

	minqConfig := minq.NewTlsConfig(serverName)
	err = loadCert(&minqConfig, certfile, keyfile)
	if err != nil {
		return nil, err
	}
	tf := minq.NewUdpTransportFactory(sock)
	minqServer := minq.NewServer(tf, &minqConfig, nil)
	server := RunServer(minqServer, config)
	go serviceUdpSocket(server.IncomingPackets, sock, server)
	return server, nil
}
