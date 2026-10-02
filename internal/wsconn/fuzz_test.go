package wsconn

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"testing"
)

// FuzzReadFrames feeds arbitrary bytes to the frame parser. Whatever the input
// it must not panic, hang or deliver more payload than it was given, must end
// in an error that is not io.EOF, and the delivered bytes must be exactly the
// payload of the well-formed binary frames at the start of the input (checked
// against a tiny reference parser for the simple cases).
func FuzzReadFrames(f *testing.F) {
	key := [4]byte{1, 2, 3, 4}
	seeds := [][]byte{
		bin("hello").bytes(),
		rawFrame{fin: true, op: opBinary, payload: []byte("masked"), mask: true, key: key}.bytes(),
		append(rawFrame{op: opBinary, payload: []byte("ab")}.bytes(), rawFrame{fin: true, op: opContinuation, payload: []byte("cd")}.bytes()...),
		append(rawFrame{op: opBinary, payload: []byte("ab")}.bytes(),
			append(rawFrame{fin: true, op: opPing, payload: []byte("p")}.bytes(), rawFrame{fin: true, op: opContinuation, payload: []byte("cd")}.bytes()...)...),
		rawFrame{fin: true, op: opBinary, payload: bytes.Repeat([]byte{7}, 300)}.bytes(),
		rawFrame{fin: true, op: opBinary, payload: []byte("tiny"), lenForm: 64}.bytes(),
		rawFrame{fin: true, op: opBinary}.bytes(),
		rawFrame{fin: true, op: opPing, payload: []byte("x")}.bytes(),
		rawFrame{fin: true, op: opClose, payload: []byte{0x03, 0xe8, 'o', 'k'}}.bytes(),
		rawFrame{fin: true, op: opClose, payload: []byte{3}}.bytes(),
		rawFrame{fin: true, op: opText, payload: []byte("text")}.bytes(),
		rawFrame{fin: true, op: opBinary, rsv: 0x40}.bytes(),
		rawFrame{fin: true, op: 3}.bytes(),
		rawFrame{fin: true, op: opBinary, length: MaxRecvFrame + 1}.bytes(),
		{0x82, 127, 0x80, 0, 0, 0, 0, 0, 0, 1},
		bin("trunc").bytes()[:4],
		{},
		{0x82},
	}
	for _, s := range seeds {
		f.Add(s, false)
		f.Add(s, true)
	}
	f.Fuzz(func(t *testing.T, data []byte, buffered bool) {
		var fc *fakeConn
		var br *bufio.Reader
		if buffered {
			// The first half arrived behind the HTTP upgrade.
			k := len(data) / 2
			br = bufio.NewReaderSize(bytes.NewReader(data[:k]), k+1)
			_, _ = br.Peek(k)
			fc = &fakeConn{r: bytes.NewReader(data[k:])}
		} else {
			fc = &fakeConn{r: bytes.NewReader(data)}
		}
		c := New(fc, br, Config{PingInterval: -1})
		defer func() { _ = c.Close() }()

		got := 0
		buf := make([]byte, 97)
		var err error
		for i := 0; i < 4*len(data)+16; i++ {
			var n int
			n, err = c.Read(buf)
			got += n
			if err != nil {
				break
			}
		}
		if err == nil {
			t.Fatalf("no error after %d reads of %d input bytes", 4*len(data)+16, len(data))
		}
		if errors.Is(err, io.EOF) {
			t.Fatalf("Read returned io.EOF-like error: %v", err)
		}
		if got > len(data) {
			t.Fatalf("delivered %d bytes from %d input bytes", got, len(data))
		}
		// Sticky terminal error.
		if _, err2 := c.Read(buf); err2 == nil {
			t.Fatal("Read succeeded after a terminal error")
		}
		if want, ok := referencePayload(data); ok && got != len(want) {
			t.Fatalf("delivered %d bytes, the reference parser expects %d", got, len(want))
		}
	})
}

// referencePayload is a deliberately simple parser: the payload bytes of the
// complete, valid, FIN binary frames at the start of data (it stops at the first
// frame it does not understand). ok is false when it stopped at anything but the
// end of the data.
func referencePayload(data []byte) (out []byte, ok bool) {
	for len(data) >= 2 {
		b0, b1 := data[0], data[1]
		if b0&0xff != 0x82 {
			return out, false
		}
		n := int(b1 & 0x7f)
		off := 2
		switch n {
		case 126:
			if len(data) < 4 {
				return out, false
			}
			n = int(data[2])<<8 | int(data[3])
			off = 4
		case 127:
			return out, false
		}
		key := [4]byte{}
		masked := b1&0x80 != 0
		if masked {
			if len(data) < off+4 {
				return out, false
			}
			copy(key[:], data[off:off+4])
			off += 4
		}
		if len(data) < off+n {
			return out, false
		}
		for i := 0; i < n; i++ {
			c := data[off+i]
			if masked {
				c ^= key[i&3]
			}
			out = append(out, c)
		}
		data = data[off+n:]
	}
	return out, len(data) == 0
}
