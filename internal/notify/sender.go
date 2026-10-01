package notify

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"
)

// maxResponseBody bounds how much of a response body is read.
const maxResponseBody = 64 << 10

// Sender performs one HTTP POST and returns the response status and body.
// Implementations: HTTPSender (direct), a node-backed sender in the daemon
// (control command http.post), and ChainSender to try several in order.
type Sender interface {
	Post(ctx context.Context, url, contentType string, body []byte) (status int, respBody []byte, err error)
}

// defaultClient is used by an HTTPSender without Client. It honours
// https_proxy / HTTPS_PROXY from the environment (http.DefaultTransport).
var defaultClient = &http.Client{Timeout: 20 * time.Second, CheckRedirect: noRedirect}

// noRedirect makes a client return 3xx responses instead of following them:
// the Bot API never redirects, and the message must not be re-posted to a
// host other than the configured one.
func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// HTTPSender posts directly with an *http.Client (a 20 s default client when
// nil). Redirects are never followed (a 3xx is returned as the status). Its
// errors never contain the request URL, which for Telegram holds the bot
// token.
type HTTPSender struct {
	Client *http.Client
}

// Post implements Sender.
func (s HTTPSender) Post(ctx context.Context, rawURL, contentType string, body []byte) (int, []byte, error) {
	client := s.Client
	if client == nil {
		client = defaultClient
	} else if client.CheckRedirect == nil {
		c := *client
		c.CheckRedirect = noRedirect
		client = &c
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(body))
	if err != nil {
		return 0, nil, stripURL(err)
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, stripURL(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return resp.StatusCode, data, stripURL(err)
	}
	// Drain what is left so the connection can be reused.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBody))
	return resp.StatusCode, data, nil
}

// stripURL removes the URL that net/http puts into its errors.
func stripURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return errors.New(ue.Op + ": " + ue.Err.Error())
	}
	return err
}

// ChainSender tries Senders in order until one gets a 2xx response. When
// none does, it returns the last HTTP response received (so a Telegram
// rejection such as 401 is reported), or all transport errors joined when
// no sender got a response at all.
type ChainSender struct {
	Senders []Sender
}

// Post implements Sender.
func (c ChainSender) Post(ctx context.Context, rawURL, contentType string, body []byte) (int, []byte, error) {
	var (
		errs       []error
		lastStatus int
		lastBody   []byte
		answered   bool
	)
	for _, s := range c.Senders {
		if s == nil {
			continue
		}
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			break
		}
		status, resp, err := s.Post(ctx, rawURL, contentType, body)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if status >= 200 && status < 300 {
			return status, resp, nil
		}
		lastStatus, lastBody, answered = status, resp, true
	}
	if answered {
		return lastStatus, lastBody, nil
	}
	if len(errs) == 0 {
		return 0, nil, errors.New("no sender configured")
	}
	return 0, nil, errors.Join(errs...)
}
