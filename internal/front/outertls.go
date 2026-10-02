package front

import "crypto/tls"

// tlsConfig is the outer TLS client configuration: the front domain as server
// name (also when an edge address is dialed), TLS 1.2 or later, http/1.1
// only (the upgrade is an HTTP/1.1 feature; offering h2 would make an edge
// that prefers it answer in the wrong protocol), system roots unless the
// Dialer carries others.
func (d *Dialer) tlsConfig(t Target) *tls.Config {
	return &tls.Config{
		ServerName: t.Host,
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"http/1.1"},
		RootCAs:    d.RootCAs,
	}
}
