package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// DialOptions tunes the Local API client.
type DialOptions struct {
	// Service is the unit named in DEY-X003 ("systemctl start <Service>");
	// default "deyroute-hub" (use "deyroute-node" on a node).
	Service string
	// Timeout bounds each non-streaming call whose context has no deadline;
	// default 2 minutes. Streaming methods (progress/emit) rely on ctx only.
	Timeout time.Duration
}

// localClient implements Local over the unix socket (methods in rpc_gen.go).
type localClient struct {
	socket  string
	service string
	timeout time.Duration
	http    *http.Client
}

// Dial returns a Local API client for the daemon listening on socketPath.
// When the socket is missing or nobody accepts connections on it, it
// returns DEY-X003 ("daemon not running: systemctl start <service>"). Every
// later call reports the same error if the daemon stops meanwhile.
func Dial(socketPath string, opts DialOptions) (Local, error) {
	c := newLocalClient(socketPath, opts)
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		return nil, c.connErr(err)
	}
	_ = conn.Close()
	return c, nil
}

func newLocalClient(socketPath string, opts DialOptions) *localClient {
	if opts.Service == "" {
		opts.Service = DefaultService
	}
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultCallTimeout
	}
	c := &localClient{socket: socketPath, service: opts.Service, timeout: opts.Timeout}
	c.http = &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
		// One connection per call: the CLI has no Close, and a unix socket
		// connection is cheap. This also leaves no idle goroutines behind.
		DisableKeepAlives:  true,
		DisableCompression: true,
	}}
	return c
}

// daemonDown reports errors that mean "nobody listens on the socket".
func daemonDown(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENOENT) ||
		errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTSOCK)
}

// connErr maps a connection-level failure to a DEY error.
func (c *localClient) connErr(err error) error {
	if daemonDown(err) {
		return deyerr.Wrap(deyerr.X003, err, deyerr.Params{"service": c.service})
	}
	if errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) {
		// The socket is 0600 root (section 11): only root may use the menu.
		return deyerr.Wrap(deyerr.I001, err, nil).
			WithWhy("the local API socket " + c.socket + " is only accessible to root").
			WithFix("run it as root, e.g.: sudo deyroute")
	}
	return deyerr.Wrap(deyerr.X006, err, deyerr.Params{"status": "connection failed"})
}

// callErr maps a transport failure during method to a DEY error.
func (c *localClient) callErr(ctx context.Context, method string, err error) error {
	if ctx.Err() != nil {
		return deyerr.Wrap(deyerr.X042, ctx.Err(), deyerr.Params{"method": method, "service": c.service})
	}
	return c.connErr(err)
}

// post sends one call and returns the response (the caller closes the body).
func (c *localClient) post(ctx context.Context, method string, args []any) (*http.Response, error) {
	if args == nil {
		args = []any{}
	}
	body, err := json.Marshal(args)
	if err != nil {
		return nil, deyerr.Wrap(deyerr.X000, err, nil)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://deyroute"+PathRPCPrefix+method, bytes.NewReader(body))
	if err != nil {
		return nil, deyerr.Wrap(deyerr.X000, err, nil)
	}
	req.Header.Set("Content-Type", ContentTypeJSON)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, c.callErr(ctx, method, err)
	}
	return resp, nil
}

// call performs a non-streaming call and decodes the result into out.
func (c *localClient) call(ctx context.Context, method string, args []any, out any) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}
	resp, err := c.post(ctx, method, args)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxRPCFrame+1))
	if err != nil {
		return c.callErr(ctx, method, err)
	}
	if len(data) > maxRPCFrame {
		return deyerr.New(deyerr.X006, deyerr.Params{"status": "response too large"})
	}
	final, err := decodeFrame(data, resp)
	if err != nil {
		return err
	}
	return final.into(out)
}

// stream performs a streaming call: step/log lines go to the callbacks (nil
// = ignored), then the final result is decoded into out. An emit error ends
// the call (the request is cancelled) and is returned as is.
func (c *localClient) stream(ctx context.Context, method string, args []any, out any, onStep func(Step), onLog func(LogLine) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	resp, err := c.post(ctx, method, args)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if !isContentType(resp, ContentTypeNDJSON) {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, maxRPCFrame))
		final, err := decodeFrame(data, resp)
		if err != nil {
			return err
		}
		return final.into(out)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64<<10), maxRPCFrame)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		f, err := parseFrame(line)
		if err != nil {
			return err
		}
		switch {
		case f.isFinal():
			return f.into(out)
		case f.step != nil:
			if onStep != nil {
				onStep(*f.step)
			}
		case f.log != nil:
			if onLog != nil {
				if err := onLog(*f.log); err != nil {
					return err
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return c.callErr(ctx, method, err)
	}
	if ctx.Err() != nil {
		return c.callErr(ctx, method, ctx.Err())
	}
	return deyerr.New(deyerr.X006, deyerr.Params{"status": "connection lost before the result"})
}

func isContentType(resp *http.Response, want string) bool {
	mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	return err == nil && strings.EqualFold(mt, want)
}

// frame is a decoded response object.
type frame struct {
	result    json.RawMessage
	hasResult bool
	errDTO    *ErrorDTO
	step      *Step
	log       *LogLine
}

func (f frame) isFinal() bool { return f.hasResult || f.errDTO != nil }

// into returns the call's error or decodes the result into out.
func (f frame) into(out any) error {
	if f.errDTO != nil {
		return f.errDTO.Err()
	}
	if out == nil || len(f.result) == 0 || string(f.result) == "null" {
		return nil
	}
	if err := json.Unmarshal(f.result, out); err != nil {
		return deyerr.Wrap(deyerr.X006, err, deyerr.Params{"status": "invalid result"})
	}
	return nil
}

func parseFrame(line []byte) (frame, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		return frame{}, deyerr.Wrap(deyerr.X006, err, deyerr.Params{"status": "invalid response"})
	}
	var f frame
	if r, ok := raw["result"]; ok {
		f.result, f.hasResult = r, true
	}
	if e, ok := raw["error"]; ok && string(e) != "null" {
		var d ErrorDTO
		if err := json.Unmarshal(e, &d); err != nil {
			return frame{}, deyerr.Wrap(deyerr.X006, err, deyerr.Params{"status": "invalid error"})
		}
		if d.Code == "" {
			d.Code = string(deyerr.X000)
		}
		f.errDTO = &d
	}
	if s, ok := raw["step"]; ok {
		f.step = new(Step)
		if err := json.Unmarshal(s, f.step); err != nil {
			return frame{}, deyerr.Wrap(deyerr.X006, err, deyerr.Params{"status": "invalid progress line"})
		}
	}
	if l, ok := raw["log"]; ok {
		f.log = new(LogLine)
		if err := json.Unmarshal(l, f.log); err != nil {
			return frame{}, deyerr.Wrap(deyerr.X006, err, deyerr.Params{"status": "invalid log line"})
		}
	}
	return f, nil
}

// decodeFrame decodes a whole (non-streaming) response.
func decodeFrame(data []byte, resp *http.Response) (frame, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || !isContentType(resp, ContentTypeJSON) && !isContentType(resp, ContentTypeNDJSON) {
		return frame{}, deyerr.New(deyerr.X006, deyerr.Params{"status": resp.Status})
	}
	// A streaming response may reach here when the method is unknown to
	// the caller's expectations: the last line carries the outcome.
	if i := bytes.LastIndexByte(data, '\n'); i >= 0 {
		data = data[i+1:]
	}
	f, err := parseFrame(data)
	if err != nil {
		return frame{}, err
	}
	if !f.isFinal() {
		return frame{}, deyerr.New(deyerr.X006, deyerr.Params{"status": fmt.Sprintf("%s without result", resp.Status)})
	}
	return f, nil
}
