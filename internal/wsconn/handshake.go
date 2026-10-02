package wsconn

import (
	"crypto/rand"
	"crypto/sha1" // #nosec G505 -- RFC 6455 mandates SHA-1 for Sec-WebSocket-Accept; it is a handshake echo, not a security primitive
	"encoding/base64"
)

// wsGUID is the fixed string of RFC 6455 section 1.3.
const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// NewKey returns a fresh random Sec-WebSocket-Key (16 random bytes, base64).
func NewKey() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // never fails since Go 1.24
	return base64.StdEncoding.EncodeToString(b[:])
}

// AcceptKey returns the Sec-WebSocket-Accept value for a request key.
func AcceptKey(key string) string {
	h := sha1.New() // #nosec G401 -- see the import: mandated by the protocol
	h.Write([]byte(key))
	h.Write([]byte(wsGUID))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}
