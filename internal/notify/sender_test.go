package notify

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// senderFunc adapts a function to Sender.
type senderFunc func(ctx context.Context, url, contentType string, body []byte) (int, []byte, error)

func (f senderFunc) Post(ctx context.Context, url, contentType string, body []byte) (int, []byte, error) {
	return f(ctx, url, contentType, body)
}

func fixed(status int, body string, err error) senderFunc {
	return func(context.Context, string, string, []byte) (int, []byte, error) {
		return status, []byte(body), err
	}
}

func TestHTTPSender(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "text/plain" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if string(body) == "big" {
			_, _ = io.WriteString(w, strings.Repeat("x", 3*maxResponseBody))
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(append([]byte("echo:"), body...))
	}))
	defer srv.Close()

	status, body, err := HTTPSender{Client: srv.Client()}.Post(context.Background(), srv.URL, "text/plain", []byte("hi"))
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, status)
	assert.Equal(t, "echo:hi", string(body))

	// Default client; oversized bodies are truncated.
	status, body, err = HTTPSender{}.Post(context.Background(), srv.URL, "text/plain", []byte("big"))
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
	assert.Len(t, body, maxResponseBody)
}

func TestHTTPSenderDoesNotFollowRedirects(t *testing.T) {
	var elsewhere int
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		elsewhere++
		w.WriteHeader(http.StatusOK)
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/steal", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()

	for _, s := range []HTTPSender{{}, {Client: srv.Client()}} {
		status, _, err := s.Post(context.Background(), srv.URL+"/botX/sendMessage", "application/json", []byte("{}"))
		require.NoError(t, err)
		assert.Equal(t, http.StatusTemporaryRedirect, status)
	}
	assert.Zero(t, elsewhere, "the message must not be re-posted to another host")

	// A caller-provided redirect policy is kept.
	c := srv.Client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return nil }
	status, _, err := HTTPSender{Client: c}.Post(context.Background(), srv.URL, "application/json", []byte("{}"))
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, 1, elsewhere)
}

func TestHTTPSenderErrorsHideURL(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	u := srv.URL + "/botSECRET-TOKEN/sendMessage"
	srv.Close()
	_, _, err := HTTPSender{}.Post(context.Background(), u, "application/json", nil)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SECRET-TOKEN")

	_, _, err = HTTPSender{}.Post(context.Background(), "http://[::1/botSECRET-TOKEN", "application/json", nil)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SECRET-TOKEN")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = HTTPSender{}.Post(ctx, "http://127.0.0.1:1/botSECRET-TOKEN", "application/json", nil)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SECRET-TOKEN")
	assert.Equal(t, errors.New("x"), stripURL(errors.New("x")))
}

func TestChainSender(t *testing.T) {
	ctx := context.Background()
	netErr := errors.New("dial tcp: i/o timeout")

	// Falls back to the node after a direct failure.
	var calls []string
	direct := senderFunc(func(context.Context, string, string, []byte) (int, []byte, error) {
		calls = append(calls, "direct")
		return 0, nil, netErr
	})
	viaNode := senderFunc(func(_ context.Context, u, ct string, b []byte) (int, []byte, error) {
		calls = append(calls, "node:"+u+":"+ct+":"+string(b))
		return 200, []byte("ok"), nil
	})
	status, body, err := ChainSender{Senders: []Sender{nil, direct, viaNode}}.Post(ctx, "U", "CT", []byte("B"))
	require.NoError(t, err)
	assert.Equal(t, 200, status)
	assert.Equal(t, "ok", string(body))
	assert.Equal(t, []string{"direct", "node:U:CT:B"}, calls)

	// Stops at the first 2xx.
	status, _, err = ChainSender{Senders: []Sender{fixed(204, "", nil), fixed(0, "", netErr)}}.Post(ctx, "U", "CT", nil)
	require.NoError(t, err)
	assert.Equal(t, 204, status)

	// A non-2xx answer is tried past, and reported when nobody succeeds.
	status, body, err = ChainSender{Senders: []Sender{fixed(502, "gw", nil), fixed(0, "", netErr), fixed(401, "unauth", nil), fixed(0, "", netErr)}}.Post(ctx, "U", "CT", nil)
	require.NoError(t, err)
	assert.Equal(t, 401, status)
	assert.Equal(t, "unauth", string(body))

	// Only transport errors: joined.
	_, _, err = ChainSender{Senders: []Sender{fixed(0, "", netErr), fixed(0, "", errors.New("node offline"))}}.Post(ctx, "U", "CT", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, netErr)
	assert.Contains(t, err.Error(), "node offline")

	_, _, err = ChainSender{}.Post(ctx, "U", "CT", nil)
	assert.EqualError(t, err, "no sender configured")

	cctx, cancel := context.WithCancel(ctx)
	cancel()
	_, _, err = ChainSender{Senders: []Sender{fixed(200, "", nil)}}.Post(cctx, "U", "CT", nil)
	assert.ErrorIs(t, err, context.Canceled)
}
