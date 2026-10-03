package fronttest

import "encoding/binary"

// activity says what a chunk of a WebSocket byte stream contained.
type activity struct {
	data bool // bytes of a data or close frame (anything that is not a ping/pong)
	ping bool // bytes of a ping or pong frame
}

// frameScanner follows the frame boundaries of one direction of a WebSocket
// stream without interpreting the payload. It lets the CDN tell pings from
// data (the real edge's idle timer is not reset by pings, as far as anyone
// can tell) and drop ping/pong frames. A stream that is not valid framing
// switches the scanner off: everything is then forwarded and counted as data.
//
// The header bytes of a frame are held back until the header is complete,
// because only then is it known whether the frame is forwarded.
type frameScanner struct {
	hdr    [14]byte
	hn     int
	remain int
	ping   bool // the frame being consumed is a ping or pong
	drop   bool // ... and it is dropped
	broken bool
}

// feed consumes p and returns the bytes that must be forwarded and what p
// contained.
func (s *frameScanner) feed(p []byte, swallowPings bool) ([]byte, activity) {
	var act activity
	if s.broken {
		act.data = len(p) > 0
		return p, act
	}
	out := make([]byte, 0, len(p))
	i := 0
	for i < len(p) {
		if s.remain > 0 {
			k := min(len(p)-i, s.remain)
			if s.ping {
				act.ping = true
			} else {
				act.data = true
			}
			if !s.drop {
				out = append(out, p[i:i+k]...)
			}
			s.remain -= k
			i += k
			continue
		}
		s.hdr[s.hn] = p[i]
		s.hn++
		i++
		need := s.headerLen()
		if need == 0 || s.hn < need {
			continue
		}
		plen, ok := s.payloadLen()
		if !ok {
			s.broken = true
			act.data = true
			out = append(out, s.hdr[:s.hn]...)
			out = append(out, p[i:]...)
			if !swallowPings {
				return p, act
			}
			return out, act
		}
		op := s.hdr[0] & 0x0f
		s.ping = op == 9 || op == 10
		s.drop = s.ping && swallowPings
		if s.ping {
			act.ping = true
		} else {
			act.data = true
		}
		if !s.drop {
			out = append(out, s.hdr[:s.hn]...)
		}
		s.hn = 0
		s.remain = plen
	}
	if !swallowPings {
		return p, act // nothing was dropped: forward the chunk as it came
	}
	return out, act
}

// headerLen returns the full header length once the first two bytes are
// known, else 0.
func (s *frameScanner) headerLen() int {
	if s.hn < 2 {
		return 0
	}
	n := 2
	switch s.hdr[1] & 0x7f {
	case 126:
		n += 2
	case 127:
		n += 8
	}
	if s.hdr[1]&0x80 != 0 {
		n += 4
	}
	return n
}

func (s *frameScanner) payloadLen() (int, bool) {
	switch l := s.hdr[1] & 0x7f; l {
	case 126:
		return int(binary.BigEndian.Uint16(s.hdr[2:4])), true
	case 127:
		v := binary.BigEndian.Uint64(s.hdr[2:10])
		if v>>40 != 0 { // not a frame this fake edge will ever see (and the MSB must be clear)
			return 0, false
		}
		return int(v), true // #nosec G115 -- bounded by the check above
	default:
		return int(l), true
	}
}
