// Command fakecdn runs the fake Cloudflare edge of internal/front/fronttest
// in the integration lab (scenario S34): it listens on -listen with an edge
// certificate for -host issued by its own CA, writes that CA to -ca (the
// node trusts it like a public CA) and forwards every request to -origin,
// the hub's front listener, adding CF-Connecting-IP.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/localroot4/deyroute/internal/front/fronttest"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:2053", "edge address")
	origin := flag.String("origin", "", "hub front host:port")
	host := flag.String("host", "front.it.lab", "name of the edge certificate")
	caFile := flag.String("ca", "/tmp/fakecdn-ca.crt", "where the edge CA is written")
	clientIP := flag.String("client-ip", "203.0.113.7", "CF-Connecting-IP")
	originTLS := flag.Bool("origin-tls", true, "TLS to the origin (Cloudflare SSL Full)")
	flag.Parse()
	cdn, err := fronttest.NewCDN(fronttest.Options{
		Listen: *listen, OriginAddr: *origin, OriginTLS: *originTLS,
		Hosts: []string{*host}, ClientIP: *clientIP,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakecdn:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*caFile, cdn.CAPEM(), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "fakecdn:", err)
		os.Exit(1)
	}
	fmt.Println("fakecdn listening on", cdn.Addr(), "->", *origin)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	_ = cdn.Close()
}
