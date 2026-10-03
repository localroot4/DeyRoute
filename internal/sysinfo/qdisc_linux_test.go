//go:build linux

package sysinfo

import (
	"encoding/binary"
	"regexp"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// TestRootQdiscLive asks the running kernel for the root qdisc of lo. A
// sandbox may refuse rtnetlink: the answer is then "", never a panic.
func TestRootQdiscLive(t *testing.T) {
	kind := RootQdisc("lo")
	if kind != "" {
		require.Regexp(t, regexp.MustCompile(`^[a-z0-9_]{1,16}$`), kind)
	}
	t.Logf("root qdisc of lo: %q", kind)
	require.Empty(t, RootQdisc("no-such-if0"))
	require.Empty(t, RootQdisc("bad/name"))
	require.Empty(t, RootQdisc(""))
}

// nlMsg builds one netlink message.
func nlMsg(typ uint16, seq uint32, payload []byte) []byte {
	ne := binary.NativeEndian
	l := unix.SizeofNlMsghdr + len(payload)
	b := make([]byte, align4(l))
	ne.PutUint32(b[0:], uint32(l))
	ne.PutUint16(b[4:], typ)
	ne.PutUint32(b[8:], seq)
	copy(b[unix.SizeofNlMsghdr:], payload)
	return b
}

// qdiscMsg builds an RTM_NEWQDISC payload with a TCA_KIND attribute.
func qdiscMsg(ifindex int32, parent uint32, kind string) []byte {
	ne := binary.NativeEndian
	b := make([]byte, sizeofTcMsg)
	ne.PutUint32(b[4:], uint32(ifindex))
	ne.PutUint32(b[12:], parent)
	// An unrelated attribute first, then TCA_KIND (NUL-terminated).
	other := make([]byte, 8)
	ne.PutUint16(other[0:], 8)
	ne.PutUint16(other[2:], 2)
	b = append(b, other...)
	attr := make([]byte, align4(4+len(kind)+1))
	ne.PutUint16(attr[0:], uint16(4+len(kind)+1))
	ne.PutUint16(attr[2:], unix.TCA_KIND)
	copy(attr[4:], kind)
	return append(b, attr...)
}

func TestParseQdiscDump(t *testing.T) {
	const seq = 7
	var dgram []byte
	dgram = append(dgram, nlMsg(unix.RTM_NEWQDISC, seq, qdiscMsg(1, tcHRoot, "noqueue"))...)
	dgram = append(dgram, nlMsg(unix.RTM_NEWQDISC, seq, qdiscMsg(2, 0xFFFFFFF1, "ingress"))...)
	dgram = append(dgram, nlMsg(unix.RTM_NEWQDISC, 99, qdiscMsg(2, tcHRoot, "stale"))...)
	dgram = append(dgram, nlMsg(unix.RTM_NEWQDISC, seq, qdiscMsg(2, 0x00010000, "fq_codel"))...)
	dgram = append(dgram, nlMsg(unix.RTM_NEWQDISC, seq, qdiscMsg(2, tcHRoot, "fq"))...)
	dgram = append(dgram, nlMsg(unix.RTM_NEWQDISC, seq, []byte{1, 2})...) // short: skipped
	done, kind, err := parseQdiscDump(dgram, seq, 2)
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, "fq", kind)

	done, kind, err = parseQdiscDump(nlMsg(unix.NLMSG_DONE, seq, make([]byte, 4)), seq, 2)
	require.NoError(t, err)
	require.True(t, done)
	require.Empty(t, kind)

	errPayload := make([]byte, 4)
	binary.NativeEndian.PutUint32(errPayload, uint32(0xFFFFFFFF)) // -EPERM
	_, _, err = parseQdiscDump(nlMsg(unix.NLMSG_ERROR, seq, errPayload), seq, 2)
	require.ErrorIs(t, err, syscall.EPERM)
	_, _, err = parseQdiscDump(nlMsg(unix.NLMSG_ERROR, seq, make([]byte, 4)), seq, 2)
	require.NoError(t, err, "an ack")
	_, _, err = parseQdiscDump(nlMsg(unix.NLMSG_ERROR, seq, nil), seq, 2)
	require.ErrorIs(t, err, errNetlink)

	bad := nlMsg(unix.RTM_NEWQDISC, seq, qdiscMsg(2, tcHRoot, "fq"))
	binary.NativeEndian.PutUint32(bad[0:], 4096)
	_, _, err = parseQdiscDump(bad, seq, 2)
	require.ErrorIs(t, err, errNetlink)

	_, kind, err = parseQdiscDump(nlMsg(unix.RTM_NEWQDISC, seq, qdiscMsg(2, tcHRoot, "a b")), seq, 2)
	require.NoError(t, err)
	require.Empty(t, kind, "not a plain name")
	require.Empty(t, qdiscKind([]byte{2, 0, 1, 0}), "attribute shorter than its header")
	require.Empty(t, qdiscKind(nil))
}

func TestQdiscRequest(t *testing.T) {
	req := qdiscRequest(5, 3)
	ne := binary.NativeEndian
	require.Len(t, req, unix.SizeofNlMsghdr+sizeofTcMsg)
	require.Equal(t, uint32(len(req)), ne.Uint32(req[0:]))
	require.Equal(t, uint16(unix.RTM_GETQDISC), ne.Uint16(req[4:]))
	require.Equal(t, uint16(unix.NLM_F_REQUEST|unix.NLM_F_DUMP), ne.Uint16(req[6:]))
	require.Equal(t, uint32(5), ne.Uint32(req[8:]))
	require.Equal(t, uint32(3), ne.Uint32(req[unix.SizeofNlMsghdr+4:]))
}
