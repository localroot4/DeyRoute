package hub

import (
	"context"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/install"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/version"
)

// Step ids of `deyroute update`.
const (
	stepResolve  = "resolve"
	stepDownload = "download"
	stepInstall  = "install"
	stepNodes    = "nodes"
	stepRestart2 = "restart_hub"
)

// releaseCheckTimeout bounds the release check.
const releaseCheckTimeout = 2 * time.Minute

// changelogMaxLines bounds the release notes shown before an update; the
// release page names the rest.
const changelogMaxLines = 40

// nodeUpdateRecord is metaNodeUpdate: set by update and rollback, cleared
// when every node runs the hub's version.
type nodeUpdateRecord struct {
	Since time.Time `json:"since"`
}

// releaseURL is the page of a release, named when its changelog cannot be
// shown.
func releaseURL(v string) string {
	return install.GitHubReleases + "/tag/" + install.Tag(v)
}

// releaseRef is a release found in a validly signed SHA256SUMS: its
// version, the parsed checksums and the sources with the one that served
// them first.
type releaseRef struct {
	version string
	sums    map[string]string
	sources []install.Source
}

// checkRelease reads the newest release from the signed SHA256SUMS of the
// first source that serves a valid one (through the fetcher chain: via a
// node first, section 5) and compares it with the running version. A newer
// release comes with its changelog text (releaseNotes).
func (h *Hub) checkRelease(ctx context.Context) (api.UpdateInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, releaseCheckTimeout)
	defer cancel()
	ref, err := h.latestRelease(ctx, "")
	if err != nil {
		return api.UpdateInfo{}, err
	}
	info := api.UpdateInfo{
		Current:   version.Version,
		Latest:    ref.version,
		Available: install.NewerThan(ref.version, version.Version),
		Changelog: releaseURL(ref.version),
		Previous:  h.previousVersion(),
	}
	if info.Available {
		info.Changelog = h.releaseNotes(ctx, ref)
	}
	_ = h.st.PutMeta(metaUpdateCheck, info)
	return info, nil
}

// releaseNotes is the changelog shown before an update (section 5): the
// sections of the release's CHANGELOG.md after the running version, read
// through the fetcher chain (a node first, as the release page usually does
// not open from Iran) and verified against the signed SHA256SUMS, at most
// changelogMaxLines lines. The release page URL stands in when the release
// has no CHANGELOG.md, it cannot be downloaded, or it names no change.
func (h *Hub) releaseNotes(ctx context.Context, ref releaseRef) string {
	page := releaseURL(ref.version)
	data, err := install.FetchChangelog(ctx, h.Fetcher(), ref.sources, ref.version, ref.sums,
		install.RetryOptions{Tries: 2, AttemptTimeout: 30 * time.Second})
	if err != nil {
		h.log.Info("release notes not available; naming the release page", dlog.Err(err))
		return page
	}
	notes := install.ChangelogNotes(data, version.Version, ref.version)
	if notes == "" {
		return page
	}
	lines := strings.Split(notes, "\n")
	if len(lines) > changelogMaxLines {
		lines = append(lines[:changelogMaxLines], "… "+page)
	}
	return strings.Join(lines, "\n")
}

// latestRelease returns the release of want ("" = newest) for this hub's
// architecture from the first source with a validly signed SHA256SUMS.
func (h *Hub) latestRelease(ctx context.Context, want string) (releaseRef, error) {
	f := h.Fetcher()
	var sigErr, lastErr error
	sources := h.sources()
	for i, src := range sources {
		data, _, err := install.FetchBytes(ctx, f, []string{src.Sums(want)}, install.RetryOptions{File: install.SumsFile})
		if err != nil {
			lastErr = err
			continue
		}
		sig, _, err := install.FetchBytes(ctx, f, []string{src.SumsSig(want)}, install.RetryOptions{File: install.SumsSigFile})
		if err != nil {
			lastErr = err
			continue
		}
		if err := install.VerifyMinisign(data, sig, h.o.MinisignKey); err != nil {
			sigErr = err
			continue
		}
		sums, err := install.ParseSHA256SUMS(data)
		if err != nil {
			sigErr = err
			continue
		}
		ver, _, _, err := install.ReleaseFromSums(sums, h.o.Arch, want)
		if err != nil {
			lastErr = err
			continue
		}
		order := append([]install.Source{src}, sources[:i]...)
		return releaseRef{version: ver, sums: sums, sources: append(order, sources[i+1:]...)}, nil
	}
	switch {
	case sigErr != nil:
		return releaseRef{}, sigErr
	case lastErr != nil:
		return releaseRef{}, lastErr
	}
	return releaseRef{}, deyerr.New(deyerr.I004, deyerr.Params{"file": install.SumsFile}).WithDetail("no download source configured")
}

// previousVersion is the version the last update replaced ("" = none).
func (h *Hub) previousVersion() string {
	if !(install.SelfUpdater{Root: h.o.Root}).HasPrevious() {
		return ""
	}
	var v string
	if ok, err := h.st.GetMeta(metaUpdatePrevious, &v); err == nil && ok {
		return v
	}
	return ""
}

// UpdateCheck implements api.Local (`deyroute update --check`): the newest
// release, whether it is newer than this hub and its release notes (the
// CHANGELOG.md text, or the release page when it has none).
func (l *local) UpdateCheck(ctx context.Context) (api.UpdateInfo, error) {
	info, err := l.h.checkRelease(ctx)
	return info, withLog(err)
}

// UpdateApply implements api.Local (`deyroute update [--version V]`, section
// 5; the CLI asks for confirmation first): the release is downloaded and
// verified (SHA256SUMS signature and archive checksum), the binary replaces
// /usr/local/bin/deyroute (the old one is kept as deyroute.prev), update_applied
// is emitted and deyroute-hub restarts right after this answer. Tunnels keep
// running (their units are separate). The nodes follow: when a node
// connects with another version it is told to install the hub's binary
// (self.update from GET /v1/assets), until every node runs it. Without a
// version and without a newer release nothing changes (Available false).
func (l *local) UpdateApply(ctx context.Context, want string, progress func(api.Step)) (api.UpdateInfo, error) {
	h := l.h
	h.ops.updMu.Lock()
	defer h.ops.updMu.Unlock()
	rep := &steps{progress: progress}
	want = strings.TrimPrefix(strings.TrimSpace(want), "v")
	info := api.UpdateInfo{Current: version.Version, Previous: h.previousVersion()}
	if err := h.refuseOlderThanFront(want); err != nil {
		return info, withLog(err)
	}
	if err := h.refuseOlderThanNewerKeys(want); err != nil {
		return info, withLog(err)
	}
	var target string
	if err := rep.run(stepResolve, func() (string, error) {
		ref, err := h.latestRelease(ctx, want)
		if err != nil {
			return "", err
		}
		target = ref.version
		return target, nil
	}); err != nil {
		return info, withLog(err)
	}
	info.Latest, info.Changelog = target, releaseURL(target)
	if want == "" && !install.NewerThan(target, version.Version) {
		rep.emit(api.Step{ID: stepDownload, Status: api.StepSkipped, Detail: "deyroute " + version.Version + " is the newest release"})
		return info, nil
	}
	work, err := os.MkdirTemp(ensureDir(h.path(DownloadDir)), "update-")
	if err != nil {
		return info, withLog(deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": h.path(DownloadDir)}))
	}
	defer func() { _ = os.RemoveAll(work) }()
	var rel *install.Release
	if err := rep.run(stepDownload, func() (string, error) {
		r, err := install.DownloadRelease(ctx, install.ReleaseOptions{
			Fetcher: h.Fetcher(), Sources: h.sources(), Version: target, Arch: h.o.Arch,
			PublicKey: h.o.MinisignKey, WorkDir: work,
		})
		if err != nil {
			return "", err
		}
		rel = r
		return install.ArchiveName(r.Version, h.o.Arch) + " (" + r.Source + ")", nil
	}); err != nil {
		return info, withLog(err)
	}
	if err := rep.run(stepInstall, func() (string, error) {
		su := install.SelfUpdater{Root: h.o.Root}
		if err := su.Install(rel.Binary); err != nil {
			return "", err
		}
		return su.BinaryPath(), nil
	}); err != nil {
		return info, withLog(err)
	}
	if err := h.st.PutMeta(metaUpdatePrevious, version.Version); err != nil {
		h.log.Warn("cannot record the replaced version", dlog.Err(err))
	}
	_ = rep.run(stepNodes, func() (string, error) { return h.markNodesForUpdate(), nil })
	h.log.Info("deyroute updated; restarting the hub", slog.String("from", version.Version), slog.String("to", rel.Version))
	h.Emit(state.Event{Type: state.EvUpdateApplied, Level: state.LevelInfo,
		Message: "deyroute updated from " + version.Version + " to " + rel.Version + "; the hub restarts and the nodes follow"})
	rep.emit(api.Step{ID: stepRestart2, Status: api.StepOK, Detail: ServiceName + " restarts now; tunnels keep running"})
	h.scheduleRestart()
	return api.UpdateInfo{Current: rel.Version, Latest: rel.Version, Previous: version.Version, Changelog: releaseURL(rel.Version)}, nil
}

// UpdateRollback implements api.Local (`deyroute update --rollback`): the
// previous binary (deyroute.prev) is put back (DEY-S007 when there is none),
// update_rolled_back is emitted and deyroute-hub restarts right after this
// answer; the nodes follow the hub's version again.
func (l *local) UpdateRollback(context.Context) (api.UpdateInfo, error) {
	h := l.h
	h.ops.updMu.Lock()
	defer h.ops.updMu.Unlock()
	if err := h.refuseWhileFrontInUse("roll back", "the previous binary may predate front mode"); err != nil {
		return api.UpdateInfo{Current: version.Version}, withLog(err)
	}
	if err := h.refuseWhileNewerKeysInUse("roll back",
		"the previous binary may predate traffic monitoring and automatic tuning"); err != nil {
		return api.UpdateInfo{Current: version.Version}, withLog(err)
	}
	prev := h.previousVersion()
	if err := (install.SelfUpdater{Root: h.o.Root}).Rollback(); err != nil {
		return api.UpdateInfo{Current: version.Version}, withLog(err)
	}
	// A second rollback returns to the binary running now.
	if err := h.st.PutMeta(metaUpdatePrevious, version.Version); err != nil {
		h.log.Warn("cannot record the replaced version", dlog.Err(err))
	}
	h.markNodesForUpdate()
	target := firstNonEmpty(prev, "the previous version")
	e := deyerr.New(deyerr.S003, deyerr.Params{"component": "deyroute", "reason": "rolled back by the owner"})
	h.log.Warn("deyroute rolled back; restarting the hub", slog.String("from", version.Version), slog.String("to", target), dlog.Code(e.Code))
	h.Emit(state.Event{Type: state.EvUpdateRolledBack, Level: state.LevelWarn, Code: string(e.Code),
		Message: "deyroute rolled back from " + version.Version + " to " + target + "; the hub restarts and the nodes follow"})
	h.scheduleRestart()
	return api.UpdateInfo{Current: prev, Previous: version.Version}, nil
}

// pushBlockedForFront reports whether hubVersion must not be pushed to node:
// it is a front node and the build predates front mode.
func (h *Hub) pushBlockedForFront(node, hubVersion string) bool {
	n, ok := h.Config().NodeByID(node)
	return ok && n.Route == config.RouteFront && olderThanFront(hubVersion)
}

// olderThanFront reports whether v is a release older than
// FirstFrontVersion. An unparseable version ("dev") is a current build.
func olderThanFront(v string) bool {
	c, ok := install.CompareVersions(strings.TrimPrefix(v, "v"), FirstFrontVersion)
	return ok && c < 0
}

// refuseWhileFrontInUse returns DEY-S010 while hub.front is enabled or a
// node has route front: older binaries refuse the front keys of config.yaml
// (strict decoding), and nodes abroad cannot be repaired remotely.
func (h *Hub) refuseWhileFrontInUse(action, reason string) error {
	cfg := h.Config()
	if !frontInUse(cfg) {
		return nil
	}
	detail := reason
	if nodes := frontNodes(cfg); len(nodes) > 0 {
		detail += " (front nodes: " + strings.Join(nodes, ", ") + ")"
	}
	return deyerr.New(deyerr.S010, deyerr.Params{"action": action, "reason": detail})
}

// refuseOlderThanFront refuses `update --version V` for a V older than the
// first release with front mode while the front is in use.
func (h *Hub) refuseOlderThanFront(want string) error {
	if want == "" || !olderThanFront(want) {
		return nil
	}
	return h.refuseWhileFrontInUse("install an older version",
		"version "+want+" is older than "+FirstFrontVersion+", the first release with front mode")
}

// FirstTrafficVersion is the first release that knows the monitoring and
// automatic tuning keys of config.yaml (config.NewerKeysInUse). An older
// binary refuses them, so rollback and downgrades below it are refused
// while any is set (DEY-S011). It is the first edge build that has them:
// every later 0.3.0-edge.N, the 0.3.0 pre-releases after it and 0.3.0
// itself know them (a plain "0.3.0" here made every edge build of 0.3.0
// look older, so `deyroute update` refused the newest edge build).
const FirstTrafficVersion = "0.3.0-edge.15"

// refuseWhileNewerKeysInUse returns DEY-S011 while config.yaml sets keys
// that a release before FirstTrafficVersion refuses (strict decoding).
func (h *Hub) refuseWhileNewerKeysInUse(action, reason string) error {
	keys := h.Config().NewerKeysInUse()
	if len(keys) == 0 {
		return nil
	}
	return deyerr.New(deyerr.S011, deyerr.Params{"action": action, "keys": strings.Join(keys, ", "), "reason": reason})
}

// refuseOlderThanNewerKeys refuses `update --version V` for a V older than
// FirstTrafficVersion while such keys are set.
func (h *Hub) refuseOlderThanNewerKeys(want string) error {
	if want == "" {
		return nil
	}
	if c, ok := install.CompareVersions(want, FirstTrafficVersion); !ok || c >= 0 {
		return nil
	}
	return h.refuseWhileNewerKeysInUse("install an older version",
		"version "+want+" is older than "+FirstTrafficVersion+", the first release with traffic monitoring and automatic tuning")
}

// markNodesForUpdate records that every node must run the hub's version
// and queues the nodes connected now; it returns a progress detail.
func (h *Hub) markNodesForUpdate() string {
	if err := h.st.PutMeta(metaNodeUpdate, nodeUpdateRecord{Since: h.now()}); err != nil {
		h.log.Warn("cannot record the pending node update", dlog.Err(err))
	}
	return "every node installs the hub's binary when it reconnects"
}

// requestNodeUpdate queues node for self.update when an update is pending
// and it runs another version than the hub (called on every connect).
func (h *Hub) requestNodeUpdate(node, nodeVersion string) {
	if strings.TrimPrefix(nodeVersion, "v") == strings.TrimPrefix(version.Version, "v") {
		h.maybeFinishNodeUpdate()
		return
	}
	if h.pushBlockedForFront(node, version.Version) {
		// A build that does not know front mode would refuse the node's
		// config.yaml (DEY-C001) and cut it off for good.
		h.log.Warn("the hub's version predates front mode: it is not pushed to a front node",
			dlog.Node(node), slog.String("version", version.Version), dlog.Code(deyerr.S010))
		return
	}
	var rec nodeUpdateRecord
	if ok, err := h.st.GetMeta(metaNodeUpdate, &rec); err != nil || !ok {
		return
	}
	select {
	case h.ops.nodeUpd <- node:
	default:
		h.log.Warn("node update queue is full", dlog.Node(node))
	}
}

// maybeFinishNodeUpdate clears the pending node update once every node
// that has connected runs the hub's version.
func (h *Hub) maybeFinishNodeUpdate() {
	var rec nodeUpdateRecord
	if ok, err := h.st.GetMeta(metaNodeUpdate, &rec); err != nil || !ok {
		return
	}
	for _, n := range h.Config().Nodes {
		ns, _ := h.nodeState(n.ID)
		if strings.TrimPrefix(ns.AgentVersion, "v") != strings.TrimPrefix(version.Version, "v") {
			return
		}
	}
	if err := h.st.DeleteMeta(metaNodeUpdate); err == nil {
		h.log.Info("every node runs the hub's version", slog.String("version", version.Version))
	}
}

// nodeUpdateLoop sends self.update to the queued nodes (one at a time):
// the node downloads the hub's binary (GET /v1/assets), verifies it,
// installs it and restarts.
func (h *Hub) nodeUpdateLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case node := <-h.ops.nodeUpd:
			h.selfUpdateNode(ctx, node)
		}
	}
}

// selfUpdateNode sends self.update to one node.
func (h *Hub) selfUpdateNode(ctx context.Context, node string) {
	ns, _ := h.nodeState(node)
	if !h.Online(node) || strings.TrimPrefix(ns.AgentVersion, "v") == strings.TrimPrefix(version.Version, "v") {
		return
	}
	args := api.SelfUpdateArgs{Version: version.Version}
	if ns.Arch == h.o.Arch {
		if info, err := h.openAsset(h.o.SelfBinary); err == nil {
			args.SHA256 = info.SHA256
			_ = info.Reader.Close()
		}
	}
	cctx, cancel := context.WithTimeout(ctx, nodeUpdateTimeout)
	err := h.Call(cctx, node, api.CmdSelfUpdate, args, nil)
	cancel()
	if err != nil {
		h.log.Warn("node could not install the hub's version; it is tried again when it reconnects",
			dlog.Node(node), slog.String("version", version.Version), dlog.Err(err), dlog.Code(deyerr.As(err).Code))
		return
	}
	h.log.Info("node installs the hub's version and restarts", dlog.Node(node), slog.String("from", ns.AgentVersion),
		slog.String("to", version.Version))
}

// UpdateManifest implements api.Local (`deyroute update manifest`, section
// 5): the signed backends.yaml of the newest release is downloaded through
// the fetcher chain, its signature verified, written to
// /etc/deyroute/backends.yaml and activated. Running tunnels keep their
// backend versions until `deyroute update backends`.
func (l *local) UpdateManifest(ctx context.Context) (api.ManifestInfo, error) {
	h := l.h
	h.ops.updMu.Lock()
	defer h.ops.updMu.Unlock()
	m, data, err := install.FetchManifest(ctx, h.Fetcher(), h.sources(), "", h.o.MinisignKey, install.RetryOptions{})
	if err != nil {
		return api.ManifestInfo{}, withLog(err)
	}
	if err := install.InstallManifest(h.o.Root, data); err != nil {
		return api.ManifestInfo{}, withLog(err)
	}
	if err := backend.LoadManifestOverride(h.path(config.ManifestPath)); err != nil {
		return api.ManifestInfo{}, withLog(err)
	}
	info := api.ManifestInfo{Source: config.ManifestPath, Versions: map[string]string{}}
	names := make([]string, 0, len(m.Backends))
	for name, e := range m.Backends {
		info.Versions[name] = e.Version
		names = append(names, name)
	}
	sort.Strings(names)
	h.log.Info("backend manifest updated", slog.String("backends", strings.Join(names, ",")))
	return info, nil
}
