package wireguard

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// uapiSetRequest renders the WireGuard cross-platform configuration
// protocol "set" operation (https://www.wireguard.com/xplatform/) with the
// AmneziaWG device keys of amneziawg-go v1.0.4 (device/uapi.go: jc, jmin,
// jmax, s1, s2, h1-h4). Keys are hex, as the protocol requires. The
// request replaces every peer, like `awg setconf`.
func uapiSetRequest(c *Config) (string, error) {
	priv, err := decodeKey(c.PrivateKey)
	if err != nil {
		return "", err
	}
	pub, err := decodeKey(c.Peer.PublicKey)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	kv := func(k, v string) { b.WriteString(k + "=" + v + "\n") }
	kv("set", "1")
	kv("private_key", hex.EncodeToString(priv[:]))
	if c.ListenPort > 0 {
		kv("listen_port", strconv.Itoa(c.ListenPort))
	}
	if a := c.AWG; a != nil {
		kv("jc", strconv.Itoa(a.Jc))
		kv("jmin", strconv.Itoa(a.Jmin))
		kv("jmax", strconv.Itoa(a.Jmax))
		kv("s1", strconv.Itoa(a.S1))
		kv("s2", strconv.Itoa(a.S2))
		kv("h1", strconv.FormatUint(uint64(a.H1), 10))
		kv("h2", strconv.FormatUint(uint64(a.H2), 10))
		kv("h3", strconv.FormatUint(uint64(a.H3), 10))
		kv("h4", strconv.FormatUint(uint64(a.H4), 10))
	}
	kv("replace_peers", "true")
	kv("public_key", hex.EncodeToString(pub[:]))
	if c.Peer.Endpoint != "" {
		kv("endpoint", c.Peer.Endpoint)
	}
	if c.Peer.PersistentKeepalive > 0 {
		kv("persistent_keepalive_interval", strconv.Itoa(c.Peer.PersistentKeepalive))
	}
	kv("replace_allowed_ips", "true")
	for _, ip := range c.Peer.AllowedIPs {
		kv("allowed_ip", ip)
	}
	b.WriteString("\n")
	return b.String(), nil
}

// uapiExchange writes req to conn and reads the reply up to the blank line
// that ends it; a non-zero errno is an error. The deadline follows ctx.
func uapiExchange(ctx context.Context, conn net.Conn, req string) error {
	dl, ok := ctx.Deadline()
	if !ok {
		dl = time.Now().Add(5 * time.Second)
	}
	if err := conn.SetDeadline(dl); err != nil {
		return err
	}
	if _, err := io.WriteString(conn, req); err != nil {
		return fmt.Errorf("write to UAPI socket: %w", err)
	}
	sc := bufio.NewScanner(conn)
	errno := ""
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			break
		}
		if v, ok := strings.CutPrefix(line, "errno="); ok {
			errno = v
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read from UAPI socket: %w", err)
	}
	switch errno {
	case "0":
		return nil
	case "":
		return fmt.Errorf("UAPI socket closed without an errno reply")
	case "-" + strconv.Itoa(int(syscall.EADDRINUSE)):
		// amneziawg-go answers a listen port it cannot bind with the
		// negative errno; Up retries this one (Manager.PortWait).
		return fmt.Errorf("amneziawg-go rejected the configuration (errno=%s, the listen port is in use; see the tunnel log): %w", errno, syscall.EADDRINUSE)
	default:
		return fmt.Errorf("amneziawg-go rejected the configuration (errno=%s; see the tunnel log)", errno)
	}
}
