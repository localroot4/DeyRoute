package wireguard

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"syscall"
)

// Generic netlink encoding for the WireGuard kernel API (QUESTIONS.md C.10:
// keys and peers are configured without the `wg` tool). The constants are
// the Linux UAPI values (include/uapi/linux/{netlink,genetlink,wireguard}.h),
// spelled out here so the encoder and its byte-level tests build on every
// platform; netlink_linux.go only adds the socket.
const (
	nlmsgHdrLen  = 16
	genlHdrLen   = 4
	nlaHdrLen    = 4
	nlmsgError   = 0x2
	nlmsgDone    = 0x3
	nlmFRequest  = 0x1
	nlmFAck      = 0x4
	nlaFNested   = 0x8000
	nlaTypeMask  = 0x3fff // clears NLA_F_NESTED and NLA_F_NET_BYTEORDER
	genlIDCtrl   = 0x10
	ctrlCmdGetFm = 0x3
	ctrlAttrID   = 0x1
	ctrlAttrName = 0x2

	wgGenlName     = "wireguard"
	wgGenlVersion  = 1
	wgCmdSetDevice = 1

	wgDeviceAIfname     = 2
	wgDeviceAPrivateKey = 3
	wgDeviceAFlags      = 5
	wgDeviceAListenPort = 6
	wgDeviceAPeers      = 8
	wgDeviceFReplace    = 1 // WGDEVICE_F_REPLACE_PEERS

	wgPeerAPublicKey = 1
	wgPeerAFlags     = 3
	wgPeerAEndpoint  = 4
	wgPeerAKeepalive = 5
	wgPeerAAllowedIP = 9
	wgPeerFReplaceIP = 2 // WGPEER_F_REPLACE_ALLOWEDIPS

	wgAllowedIPAFamily = 1
	wgAllowedIPAAddr   = 2
	wgAllowedIPAMask   = 3

	afInet  = 2
	afInet6 = 10
)

// ne is the host byte order netlink uses for headers and integer
// attributes.
var ne = binary.NativeEndian

// nlAttr encodes one attribute (header + payload, padded to 4 bytes).
func nlAttr(typ uint16, payload []byte) []byte {
	n := nlaHdrLen + len(payload)
	b := make([]byte, align4(n))
	ne.PutUint16(b[0:2], uint16(n)) // #nosec G115 -- attributes here are < 1 KiB
	ne.PutUint16(b[2:4], typ)
	copy(b[nlaHdrLen:], payload)
	return b
}

// nlNested encodes a nested attribute holding children.
func nlNested(typ uint16, children ...[]byte) []byte {
	var payload []byte
	for _, c := range children {
		payload = append(payload, c...)
	}
	return nlAttr(typ|nlaFNested, payload)
}

func nlU16(typ, v uint16) []byte {
	var b [2]byte
	ne.PutUint16(b[:], v)
	return nlAttr(typ, b[:])
}

func nlU32(typ uint16, v uint32) []byte {
	var b [4]byte
	ne.PutUint32(b[:], v)
	return nlAttr(typ, b[:])
}

func nlString(typ uint16, s string) []byte {
	return nlAttr(typ, append([]byte(s), 0))
}

func align4(n int) int { return (n + 3) &^ 3 }

// genlMessage wraps a generic netlink payload in nlmsghdr + genlmsghdr.
func genlMessage(family uint16, seq uint32, cmd, version uint8, attrs ...[]byte) []byte {
	var body []byte
	for _, a := range attrs {
		body = append(body, a...)
	}
	n := nlmsgHdrLen + genlHdrLen + len(body)
	b := make([]byte, nlmsgHdrLen+genlHdrLen, n)
	ne.PutUint32(b[0:4], uint32(n)) // #nosec G115 -- messages here are < 4 KiB
	ne.PutUint16(b[4:6], family)
	ne.PutUint16(b[6:8], nlmFRequest|nlmFAck)
	ne.PutUint32(b[8:12], seq)
	// b[12:16] port id 0: the kernel fills in the sender's.
	b[16] = cmd
	b[17] = version
	return append(b, body...)
}

// encodeGetFamily asks the generic netlink controller for the id of name.
func encodeGetFamily(seq uint32, name string) []byte {
	return genlMessage(genlIDCtrl, seq, ctrlCmdGetFm, 1, nlString(ctrlAttrName, name))
}

// deviceSettings are the values WG_CMD_SET_DEVICE applies.
type deviceSettings struct {
	iface      string
	privateKey [32]byte
	listenPort int // 0 = leave unset (kernel picks a port)
	peerKey    [32]byte
	endpoint   netip.AddrPort // zero = none
	keepalive  int
	allowedIPs []netip.Prefix
}

// encodeSetDevice builds WG_CMD_SET_DEVICE replacing every peer with the
// single configured one (and its allowed IPs), like `wg setconf`.
func encodeSetDevice(family uint16, seq uint32, d deviceSettings) []byte {
	var peer [][]byte
	peer = append(peer,
		nlAttr(wgPeerAPublicKey, d.peerKey[:]),
		nlU32(wgPeerAFlags, wgPeerFReplaceIP),
	)
	if d.endpoint.IsValid() {
		peer = append(peer, nlAttr(wgPeerAEndpoint, sockaddr(d.endpoint)))
	}
	if d.keepalive > 0 {
		peer = append(peer, nlU16(wgPeerAKeepalive, uint16(d.keepalive))) // #nosec G115 -- validated 0..65535
	}
	var ips [][]byte
	for _, p := range d.allowedIPs {
		fam, addr := uint16(afInet), p.Addr().AsSlice()
		if p.Addr().Is6() {
			fam = afInet6
		}
		ips = append(ips, nlNested(0,
			nlU16(wgAllowedIPAFamily, fam),
			nlAttr(wgAllowedIPAAddr, addr),
			nlAttr(wgAllowedIPAMask, []byte{byte(p.Bits())}), // #nosec G115 -- prefix bits <= 128
		))
	}
	peer = append(peer, nlNested(wgPeerAAllowedIP, ips...))

	attrs := [][]byte{
		nlString(wgDeviceAIfname, d.iface),
		nlAttr(wgDeviceAPrivateKey, d.privateKey[:]),
	}
	if d.listenPort > 0 {
		attrs = append(attrs, nlU16(wgDeviceAListenPort, uint16(d.listenPort))) // #nosec G115 -- validated 1..65535
	}
	attrs = append(attrs,
		nlU32(wgDeviceAFlags, wgDeviceFReplace),
		nlNested(wgDeviceAPeers, nlNested(0, peer...)),
	)
	return genlMessage(family, seq, wgCmdSetDevice, wgGenlVersion, attrs...)
}

// sockaddr encodes struct sockaddr_in / sockaddr_in6 (family in host order,
// port in network order), the WGPEER_A_ENDPOINT payload.
func sockaddr(ap netip.AddrPort) []byte {
	a := ap.Addr().Unmap()
	if a.Is4() {
		b := make([]byte, 16)
		ne.PutUint16(b[0:2], afInet)
		binary.BigEndian.PutUint16(b[2:4], ap.Port())
		v4 := a.As4()
		copy(b[4:8], v4[:])
		return b
	}
	b := make([]byte, 28)
	ne.PutUint16(b[0:2], afInet6)
	binary.BigEndian.PutUint16(b[2:4], ap.Port())
	v6 := a.As16()
	copy(b[8:24], v6[:])
	return b
}

// nlMessage is one parsed netlink message.
type nlMessage struct {
	typ   uint16
	flags uint16
	seq   uint32
	data  []byte // payload after the 16-byte header
}

// parseMessages splits a netlink datagram into messages.
func parseMessages(b []byte) ([]nlMessage, error) {
	var out []nlMessage
	for len(b) >= nlmsgHdrLen {
		n := int(ne.Uint32(b[0:4]))
		if n < nlmsgHdrLen || n > len(b) {
			return nil, errors.New("netlink: truncated message")
		}
		out = append(out, nlMessage{
			typ:   ne.Uint16(b[4:6]),
			flags: ne.Uint16(b[6:8]),
			seq:   ne.Uint32(b[8:12]),
			data:  b[nlmsgHdrLen:n],
		})
		b = b[min(align4(n), len(b)):]
	}
	return out, nil
}

// ackError returns the error carried by an NLMSG_ERROR message (nil for
// an ACK).
func ackError(m nlMessage) error {
	if len(m.data) < 4 {
		return errors.New("netlink: short error message")
	}
	code := int32(ne.Uint32(m.data[0:4])) // #nosec G115 -- wire format is a signed errno
	if code == 0 {
		return nil
	}
	return syscall.Errno(-code)
}

// parseAttrs returns the top-level attributes of b by type (nested flag
// cleared).
func parseAttrs(b []byte) map[uint16][]byte {
	out := map[uint16][]byte{}
	for len(b) >= nlaHdrLen {
		n := int(ne.Uint16(b[0:2]))
		if n < nlaHdrLen || n > len(b) {
			break
		}
		out[ne.Uint16(b[2:4])&nlaTypeMask] = b[nlaHdrLen:n]
		b = b[min(align4(n), len(b)):]
	}
	return out
}

// familyID extracts CTRL_ATTR_FAMILY_ID from a CTRL_CMD_NEWFAMILY reply.
func familyID(msgs []nlMessage, seq uint32) (uint16, error) {
	for _, m := range msgs {
		if m.seq != seq || m.typ == nlmsgError || m.typ == nlmsgDone || len(m.data) < genlHdrLen {
			continue
		}
		if id, ok := parseAttrs(m.data[genlHdrLen:])[ctrlAttrID]; ok && len(id) >= 2 {
			return ne.Uint16(id), nil
		}
	}
	return 0, fmt.Errorf("generic netlink family %q not found (is the wireguard module available?)", wgGenlName)
}
