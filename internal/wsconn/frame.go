package wsconn

import (
	"encoding/binary"
	"sync"
)

// Opcodes (RFC 6455 section 5.2).
const (
	opContinuation byte = 0x0
	opText         byte = 0x1
	opBinary       byte = 0x2
	opClose        byte = 0x8
	opPing         byte = 0x9
	opPong         byte = 0xA
)

// Close status codes used by the adapter (RFC 6455 section 7.4.1).
const (
	CloseNormal        = 1000
	CloseProtocolError = 1002
	CloseUnsupported   = 1003
	CloseNoStatus      = 1005 // reported when a close frame carried no code
	CloseMessageTooBig = 1009
)

// Sizes.
const (
	// DefaultMaxFrame is the default (and the usual maximum) outgoing payload
	// per frame.
	DefaultMaxFrame = 32 << 10
	// MaxRecvFrame is the largest incoming frame payload accepted; a bigger one
	// is answered with close 1009.
	MaxRecvFrame = 1 << 20

	maxControlPayload = 125
	maxHeader         = 14 // 2 + 8 (64-bit length) + 4 (mask key)
	rbufSize          = 4096
)

// rbufPool holds the small read buffers: only a connection that is waiting for
// data, or holds unconsumed bytes, owns one, so an idle connection costs
// nothing.
var rbufPool = sync.Pool{New: func() any {
	b := make([]byte, rbufSize)
	return &b
}}

// framePool holds the write buffers (header + payload) of the default frame
// size; one is taken for the duration of one frame build and write.
var framePool = sync.Pool{New: func() any {
	b := make([]byte, DefaultMaxFrame+maxHeader)
	return &b
}}

// frameHeader is a parsed frame header.
type frameHeader struct {
	fin    bool
	op     byte
	masked bool
	key    [4]byte
	length int
}

// headerNeed returns how many header bytes are needed in total, given the
// second byte of the header.
func headerNeed(b1 byte) int {
	need := 2
	switch b1 & 0x7f {
	case 126:
		need += 2
	case 127:
		need += 8
	}
	if b1&0x80 != 0 {
		need += 4
	}
	return need
}

// parseHeader decodes a complete header (len(b) == headerNeed(b[1])). It does
// not judge the frame: rsv reports non-zero RSV bits, msb a 64-bit length with
// the top bit set. A length above MaxRecvFrame is reported as MaxRecvFrame+1.
func parseHeader(b []byte) (h frameHeader, rsv, msb bool) {
	h.fin = b[0]&0x80 != 0
	rsv = b[0]&0x70 != 0
	h.op = b[0] & 0x0f
	h.masked = b[1]&0x80 != 0
	off := 2
	switch l := b[1] & 0x7f; l {
	case 126:
		h.length = int(binary.BigEndian.Uint16(b[2:4])) // #nosec G602 -- the caller passes headerNeed(b[1]) bytes
		off = 4
	case 127:
		v := binary.BigEndian.Uint64(b[2:10]) // #nosec G602 -- see above
		switch {
		case v>>63 != 0:
			msb = true
		case v > MaxRecvFrame:
			h.length = MaxRecvFrame + 1
		default:
			h.length = int(v) // #nosec G115 -- bounded by the case above
		}
		off = 10
	default:
		h.length = int(l)
	}
	if h.masked {
		copy(h.key[:], b[off:off+4])
	}
	return h, rsv, msb
}

// maskCopy writes src XOR key (the key starts at payload offset pos) to dst;
// dst and src may be the same slice. It works on 8 bytes at a time.
func maskCopy(dst, src []byte, key [4]byte, pos int) {
	var k [8]byte
	for i := range k {
		k[i] = key[(pos+i)&3]
	}
	k64 := binary.LittleEndian.Uint64(k[:])
	i := 0
	for ; i+8 <= len(src); i += 8 {
		binary.LittleEndian.PutUint64(dst[i:], binary.LittleEndian.Uint64(src[i:])^k64)
	}
	for ; i < len(src); i++ {
		dst[i] = src[i] ^ k[i&3]
	}
}
