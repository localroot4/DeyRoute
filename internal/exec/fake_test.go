package exec

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

var _ Runner = (*Fake)(nil)
var _ Runner = (*OSRunner)(nil)

func TestFakeExactAndQueue(t *testing.T) {
	f := NewFake().
		On("systemctl is-active deyroute-hub.service", Fail(3, "inactive"), OK("active\n")).
		On("systemctl daemon-reload")
	ctx := context.Background()

	_, se, err := f.Run(ctx, "systemctl", []string{"is-active", "deyroute-hub.service"}, nil)
	require.True(t, deyerr.HasCode(err, deyerr.X007))
	require.Equal(t, "inactive", string(se))
	code, ok := ExitCode(err)
	require.True(t, ok)
	require.Equal(t, 3, code)

	for i := 0; i < 2; i++ { // last response repeats
		so, _, err := f.Run(ctx, "systemctl", []string{"is-active", "deyroute-hub.service"}, nil)
		require.NoError(t, err)
		require.Equal(t, "active\n", string(so))
	}

	so, se, err := f.Run(ctx, "systemctl", []string{"daemon-reload"}, nil)
	require.NoError(t, err)
	require.Nil(t, so)
	require.Nil(t, se)

	require.Equal(t, 3, f.Count("systemctl is-active deyroute-hub.service"))
	require.True(t, f.Called("systemctl daemon-reload"))
	require.False(t, f.Called("systemctl start x"))
	require.Equal(t, []string{"systemctl daemon-reload", "systemctl is-active deyroute-hub.service"}, f.Scripted())
}

func TestFakeResponsesPrefixesDefault(t *testing.T) {
	sentinel := deyerr.New(deyerr.X030, deyerr.Params{"command": "ufw"})
	f := &Fake{
		Responses: map[string]Response{"nft list tables": OK("table inet deyroute\n")},
		Prefixes: map[string]Response{
			"ip ":       OK("short"),
			"ip link ":  OK("long"),
			"ufw":       {Err: sentinel},
			"iptables ": Fail(1, "iptables: bad rule"),
		},
	}
	ctx := context.Background()
	so, _, err := f.Run(ctx, "nft", []string{"list", "tables"}, nil)
	require.NoError(t, err)
	require.Equal(t, "table inet deyroute\n", string(so))

	so, _, _ = f.Run(ctx, "ip", []string{"link", "show"}, nil)
	require.Equal(t, "long", string(so))
	so, _, _ = f.Run(ctx, "ip", []string{"route", "get", "1.1.1.1"}, nil)
	require.Equal(t, "short", string(so))

	_, _, err = f.Run(ctx, "ufw", []string{"status"}, nil)
	require.Same(t, sentinel, err)

	_, _, err = f.Run(ctx, "iptables", []string{"-S"}, nil)
	require.True(t, deyerr.HasCode(err, deyerr.X007))
	require.Contains(t, err.Error(), "iptables: bad rule")

	// Unmatched without default.
	_, se, err := f.Run(ctx, "ss", []string{"-tlnp"}, nil)
	require.True(t, deyerr.HasCode(err, deyerr.X007))
	require.Contains(t, string(se), "no scripted response for ss -tlnp")
	code, _ := ExitCode(err)
	require.Equal(t, 127, code)

	// Default.
	d := OK("dflt")
	f.Default = &d
	so, _, err = f.Run(ctx, "ss", []string{"-tlnp"}, nil)
	require.NoError(t, err)
	require.Equal(t, "dflt", string(so))

	f.OnPrefix("ss -t", OK("prefix"))
	so, _, _ = f.Run(ctx, "ss", []string{"-tlnp"}, nil)
	require.Equal(t, "prefix", string(so))
}

func TestFakeAbsolutePathMatchesBaseName(t *testing.T) {
	f := NewFake().On("xray x25519", OK("Private key: a\nPublic key: b\n"))
	so, _, err := f.Run(context.Background(), "/var/lib/deyroute/bin/xray/v26.3.27/xray", []string{"x25519"}, nil)
	require.NoError(t, err)
	require.Contains(t, string(so), "Public key")
	calls := f.Calls()
	require.Len(t, calls, 1)
	require.Equal(t, "/var/lib/deyroute/bin/xray/v26.3.27/xray", calls[0].Name)

	f.OnPrefix("rathole --", OK("keys"))
	so, _, err = f.Run(context.Background(), "/opt/rathole", []string{"--genkey"}, nil)
	require.NoError(t, err)
	require.Equal(t, "keys", string(so))
}

func TestFakeHandlerAndStdin(t *testing.T) {
	f := NewFake()
	f.Handler = func(c Call) (Response, bool) {
		if c.Name == "nft" && len(c.Args) == 2 && c.Args[0] == "-f" {
			return OK(fmt.Sprintf("read %d bytes", len(c.Stdin))), true
		}
		return Response{}, false
	}
	f.On("nft list ruleset", OK("rules"))
	in := []byte("table inet deyroute {}\n")
	so, _, err := f.Run(context.Background(), "nft", []string{"-f", "-"}, in)
	require.NoError(t, err)
	require.Equal(t, "read 23 bytes", string(so))
	in[0] = 'X' // the fake keeps its own copy
	require.Equal(t, byte('t'), f.Calls()[0].Stdin[0])

	so, _, _ = f.Run(context.Background(), "nft", []string{"list", "ruleset"}, nil)
	require.Equal(t, "rules", string(so))
	require.Nil(t, f.Calls()[1].Stdin)
	require.Equal(t, []string{"nft -f -", "nft list ruleset"}, f.Lines())

	f.Reset()
	require.Empty(t, f.Calls())
}

func TestFakeRejectsDisallowedAndCancelled(t *testing.T) {
	f := NewFake().On("bash -c id")
	_, _, err := f.Run(context.Background(), "bash", []string{"-c", "id"}, nil)
	require.True(t, deyerr.HasCode(err, deyerr.X004))
	require.Equal(t, []string{"bash -c id"}, f.Lines()) // still recorded

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.On("ip link")
	_, _, err = f.Run(ctx, "ip", []string{"link"}, nil)
	require.True(t, deyerr.HasCode(err, deyerr.X031))
	require.ErrorIs(t, err, context.Canceled)
}

func TestFakeOnWithoutResponse(t *testing.T) {
	f := NewFake().On("systemctl daemon-reload")
	_, _, err := f.Run(context.Background(), "systemctl", []string{"daemon-reload"}, nil)
	require.NoError(t, err)
	require.Equal(t, "systemctl", CommandLine("systemctl", nil))
	require.Equal(t, "systemctl daemon-reload", Call{Name: "systemctl", Args: []string{"daemon-reload"}}.Line())
}

func TestFakeConcurrent(t *testing.T) {
	f := NewFake().OnPrefix("systemctl show", OK("ActiveState=active\n"))
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, err := f.Run(context.Background(), "systemctl", []string{"show", fmt.Sprint("u", i)}, nil)
			require.NoError(t, err)
			_ = f.Lines()
		}(i)
	}
	wg.Wait()
	require.Len(t, f.Calls(), 16)
}
