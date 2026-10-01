package hub

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	stderrors "errors"
	"io"
	"log/slog"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/install"
	dlog "github.com/localroot4/deyroute/internal/log"
)

// MaxUnverifiedFetch caps a fetch.proxy download without an expected
// sha256 (SHA256SUMS, signatures, manifests: the caller verifies their
// signature). It matches the node's own limit.
const MaxUnverifiedFetch = 16 << 20

// Upload errors (the node sees them as the answer to its upload).
var (
	errUploadClosed   = deyerr.Plain("the download was abandoned by the hub")
	errUploadTooLarge = deyerr.Plain("the upload exceeds its size limit")
)

// pendingUpload is one fetch.proxy waiting for its payload on
// POST /v1/upload/{id}. Writes go to w until the fetch gives up.
type pendingUpload struct {
	node string
	max  int64
	done chan struct{} // closed when the upload handler finished

	mu      sync.Mutex
	w       io.Writer
	n       int64
	closed  bool // the fetch returned: w must not be written any more
	started bool // an upload for this id arrived
	err     error
}

// Write implements io.Writer for the upload handler.
func (p *pendingUpload) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0, errUploadClosed
	}
	if p.n+int64(len(b)) > p.max {
		return 0, errUploadTooLarge
	}
	n, err := p.w.Write(b)
	p.n += int64(n)
	return n, err
}

// written returns the bytes delivered so far.
func (p *pendingUpload) written() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.n
}

// close stops further writes (the fetch returned).
func (p *pendingUpload) close() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
}

// newUploadID returns a random upload id (32 hex characters).
func newUploadID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", deyerr.Wrap(deyerr.X000, err, nil)
	}
	return hex.EncodeToString(b), nil
}

// upload handles POST /v1/upload/{id}: the payload is streamed into the
// waiting fetch, never more than its MaxBytes. An unknown id, an id of
// another node or a second upload for the same id is DEY-N015.
func (h *Hub) upload(_ context.Context, id, nodeID string, body io.Reader) error {
	h.upMu.Lock()
	p := h.uploads[id]
	var reason string
	switch {
	case p == nil:
		reason = "unknown upload id"
	case p.node != nodeID:
		reason = "the upload id belongs to another node"
	default:
		p.mu.Lock()
		if p.started {
			reason = "the upload was already received"
		}
		p.started = true
		p.mu.Unlock()
	}
	h.upMu.Unlock()
	if reason != "" {
		return deyerr.New(deyerr.N015, deyerr.Params{"node": nodeID, "reason": reason})
	}
	_, err := io.Copy(p, body)
	switch {
	case stderrors.Is(err, errUploadTooLarge):
		err = deyerr.New(deyerr.N015, deyerr.Params{"node": nodeID,
			"reason": "the upload is larger than the allowed " + strconv.FormatInt(p.max, 10) + " bytes"})
	case stderrors.Is(err, errUploadClosed):
		err = deyerr.New(deyerr.N014, deyerr.Params{"node": nodeID, "command": api.CmdFetchProxy})
	case err != nil:
		err = deyerr.Wrap(deyerr.N015, err, deyerr.Params{"node": nodeID, "reason": "the upload was interrupted"})
	}
	p.mu.Lock()
	p.err = err
	p.mu.Unlock()
	close(p.done)
	return err
}

// fetchVia downloads url through node with fetch.proxy (section 5): the
// node downloads, verifies sha256 (when given) and uploads the payload,
// which is streamed into w. It returns how many bytes reached w.
func (h *Hub) fetchVia(ctx context.Context, node, rawURL, sha string, maxBytes int64, w io.Writer) (int64, error) {
	id, err := newUploadID()
	if err != nil {
		return 0, err
	}
	p := &pendingUpload{node: node, max: maxBytes, w: w, done: make(chan struct{})}
	h.upMu.Lock()
	h.uploads[id] = p
	h.upMu.Unlock()
	defer func() {
		p.close()
		h.upMu.Lock()
		delete(h.uploads, id)
		h.upMu.Unlock()
	}()
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultFetchTimeout)
		defer cancel()
	}
	var res api.FetchResult
	err = h.Call(ctx, node, api.CmdFetchProxy, api.FetchArgs{URL: rawURL, SHA256: sha, UploadID: id, MaxBytes: maxBytes}, &res)
	if err != nil {
		return p.written(), err
	}
	// The node answers after its upload was accepted, so the upload is
	// normally complete; wait briefly in case the answer overtook it.
	t := time.NewTimer(h.o.UploadGrace)
	defer t.Stop()
	select {
	case <-p.done:
	case <-t.C:
		return p.written(), deyerr.New(deyerr.N015, deyerr.Params{"node": node, "reason": "fetch.proxy finished without an upload"})
	case <-ctx.Done():
		return p.written(), deyerr.Wrap(deyerr.N014, ctx.Err(), deyerr.Params{"node": node, "command": api.CmdFetchProxy})
	}
	p.mu.Lock()
	n, uerr := p.n, p.err
	p.mu.Unlock()
	if uerr != nil {
		return n, uerr
	}
	if res.Bytes != n {
		return n, deyerr.New(deyerr.N015, deyerr.Params{"node": node,
			"reason": "the node reported " + strconv.FormatInt(res.Bytes, 10) + " bytes but uploaded " + strconv.FormatInt(n, 10)})
	}
	if sha != "" && !strings.EqualFold(res.SHA256, sha) {
		return n, deyerr.New(deyerr.S001, deyerr.Params{"file": fileOfURL(rawURL)}).
			WithDetail("the node verified sha256 " + res.SHA256 + " instead of " + sha)
	}
	return n, nil
}

// fileOfURL names the file of a download URL in messages.
func fileOfURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "download"
	}
	b := path.Base(u.Path)
	if b == "." || b == "/" || b == "" {
		return u.Host
	}
	return b
}

// nodeFetcher is the install.Fetcher that downloads through online nodes
// (section 5: GitHub from Iran). The expected sha256 travels in the
// context (install.WithExpectedSHA256, set by install.FetchVerified).
type nodeFetcher struct{ h *Hub }

// resettable is a download target that can be rewound (an *os.File or
// the capped file of install.FetchVerified).
type resettable interface {
	Truncate(size int64) error
	Seek(offset int64, whence int) (int64, error)
}

// Fetch implements install.Fetcher: it tries the online, compatible nodes
// (fastest control link first) until one delivers; DEY-N012 when no node
// is online. Both that and "no node can fetch" (every node answered
// DEY-X008) are permanent, so a retry loop does not wait for them.
func (f nodeFetcher) Fetch(ctx context.Context, rawURL string, w io.Writer) error {
	nodes := f.h.onlineNodes()
	if len(nodes) == 0 {
		// Permanent for this attempt: the chain moves on to the direct
		// download at once instead of waiting for a node.
		return install.Permanent(deyerr.New(deyerr.N012, nil))
	}
	sha := install.ExpectedSHA256(ctx)
	maxBytes := int64(install.DefaultMaxBytes)
	if sha == "" {
		maxBytes = MaxUnverifiedFetch
	}
	var (
		errs    []error
		written int64
	)
	for i, node := range nodes {
		if i > 0 {
			if r, ok := w.(resettable); ok {
				if err := r.Truncate(0); err != nil {
					break
				}
				if _, err := r.Seek(0, io.SeekStart); err != nil {
					break
				}
			} else if written > 0 {
				break
			}
		}
		n, err := f.h.fetchVia(ctx, node, rawURL, sha, maxBytes, w)
		if err == nil {
			f.h.log.Info("file downloaded through a node", dlog.Node(node),
				slog.String("url", install.RedactURL(rawURL)), slog.Int64("bytes", n))
			return nil
		}
		f.h.log.Warn("download through a node failed", dlog.Node(node),
			slog.String("url", install.RedactURL(rawURL)), dlog.Err(err))
		errs = append(errs, err)
		written = n
		if ctx.Err() != nil {
			break
		}
	}
	var err error
	if len(errs) == 1 {
		err = errs[0]
	} else {
		err = stderrors.Join(errs...)
	}
	if ctx.Err() == nil && len(errs) == len(nodes) && allUnsupported(errs) {
		// No node of this version can fetch files (DEY-X008): trying
		// again in a moment cannot help, only the next download path.
		return install.Permanent(err)
	}
	return err
}

// allUnsupported reports whether every error is DEY-X008 (the node does
// not implement the command).
func allUnsupported(errs []error) bool {
	for _, e := range errs {
		if !deyerr.HasCode(e, deyerr.X008) {
			return false
		}
	}
	return len(errs) > 0
}

// Fetcher returns the hub's download chain (section 5): once any node has
// joined every download goes through a node first and directly second;
// before that only directly.
func (h *Hub) Fetcher() install.Fetcher {
	if len(h.Config().Nodes) == 0 {
		return h.o.Fetcher
	}
	return install.ChainFetcher{Fetchers: []install.Fetcher{nodeFetcher{h}, h.o.Fetcher}}
}
