package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"time"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// Control API wire limits and headers.
const (
	// HeaderSHA256 carries the hex sha256 of an asset (GET /v1/assets/...).
	HeaderSHA256 = "X-Deyroute-Sha256"
	// HeaderVersion carries the deyroute version of an asset.
	HeaderVersion = "X-Deyroute-Version"

	// MaxJoinRequest is the body limit of POST /v1/join.
	MaxJoinRequest = 64 << 10
	// MaxControlLine is the size limit of one NDJSON message on /v1/stream.
	MaxControlLine = 16 << 20
	// DefaultCommandTimeout bounds Session.Call when ctx has no deadline.
	DefaultCommandTimeout = 30 * time.Second
)

var (
	uploadIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	archRe     = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)
)

// errLineTooLong is returned by lineReader for an oversized message.
var errLineTooLong = deyerr.Plain("api: control message too large")

// lineReader reads newline-delimited messages with a size limit.
type lineReader struct {
	r   *bufio.Reader
	max int
}

func newLineReader(r io.Reader, max int) *lineReader {
	return &lineReader{r: bufio.NewReaderSize(r, 64<<10), max: max}
}

// next returns the next non-empty line without its newline. A final line
// without newline is returned before io.EOF.
func (l *lineReader) next() ([]byte, error) {
	for {
		var buf []byte
		for {
			frag, err := l.r.ReadSlice('\n')
			if len(buf)+len(frag) > l.max {
				return nil, errLineTooLong
			}
			buf = append(buf, frag...)
			if err == nil {
				break
			}
			if errors.Is(err, bufio.ErrBufferFull) {
				continue
			}
			if errors.Is(err, io.EOF) && len(bytes.TrimSpace(buf)) > 0 {
				return bytes.TrimSpace(buf), nil
			}
			return nil, err
		}
		if line := bytes.TrimSpace(buf); len(line) > 0 {
			return line, nil
		}
	}
}

// writeJSONLine writes v as one NDJSON line.
func writeJSONLine(w io.Writer, v any) error {
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(line, '\n'))
	return err
}

// controlEnvelope is the body of every non-stream Control API response.
type controlEnvelope struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  *ErrorDTO       `json:"error,omitempty"`
}

// writeControlResult answers 200 {"result": v}.
func writeControlResult(w http.ResponseWriter, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		writeControlError(w, deyerr.Wrap(deyerr.X000, err, nil))
		return
	}
	w.Header().Set("Content-Type", ContentTypeJSON)
	w.WriteHeader(http.StatusOK)
	_ = writeJSONLine(w, controlEnvelope{Result: data})
}

// writeControlError answers {"error": ErrorDTO} with a status derived from
// the code.
func writeControlError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", ContentTypeJSON)
	w.WriteHeader(controlStatus(err))
	_ = writeJSONLine(w, controlEnvelope{Error: ToDTO(err)})
}

// controlStatus maps a DEY code to an HTTP status (informational only; the
// client decides on the ErrorDTO).
func controlStatus(err error) int {
	switch deyerr.As(err).Code {
	case deyerr.N013, deyerr.N008:
		return http.StatusUnauthorized
	case deyerr.N001:
		return http.StatusForbidden
	case deyerr.N007:
		return http.StatusTooManyRequests
	case deyerr.N010:
		return http.StatusConflict
	case deyerr.N015:
		return http.StatusBadRequest
	}
	if deyerr.ExitCodeFor(deyerr.As(err).Code) == deyerr.ExitSystem {
		return http.StatusInternalServerError
	}
	return http.StatusBadRequest
}

// readControlResponse decodes a non-stream Control API response into out
// (nil = ignore the result). Hub-side errors keep their DEY codes; a
// response that is not an envelope is DEY-N015.
func readControlResponse(resp *http.Response, out any) error {
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxControlLine+1))
	if err != nil {
		return err
	}
	var env controlEnvelope
	if len(data) > MaxControlLine || json.Unmarshal(data, &env) != nil || env.Error == nil && env.Result == nil && resp.StatusCode != http.StatusOK {
		return deyerr.New(deyerr.N015, deyerr.Params{"node": "hub", "reason": fmt.Sprintf("unexpected response %s", resp.Status)})
	}
	if env.Error != nil {
		if env.Error.Code == "" {
			env.Error.Code = string(deyerr.X000)
		}
		return env.Error.Err()
	}
	if resp.StatusCode != http.StatusOK {
		return deyerr.New(deyerr.N015, deyerr.Params{"node": "hub", "reason": fmt.Sprintf("unexpected response %s", resp.Status)})
	}
	if out == nil || len(env.Result) == 0 || string(env.Result) == "null" {
		return nil
	}
	if err := json.Unmarshal(env.Result, out); err != nil {
		return deyerr.Wrap(deyerr.N015, err, deyerr.Params{"node": "hub", "reason": "invalid result"})
	}
	return nil
}

// remoteIP returns the IP part of r.RemoteAddr.
func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
