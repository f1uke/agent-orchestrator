package preview

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// DefaultPollInterval is the preview poller's scan interval when none is
// configured. Every tick lists all sessions and stats each worker workspace, so
// it is paced for the event it watches for — a build dropping an index.html —
// not for keystroke latency.
const DefaultPollInterval = 2 * time.Second

type sessionPreviewSource interface {
	ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error)
}

type previewService interface {
	SetPreview(ctx context.Context, id domain.SessionID, previewURL string) (domain.Session, error)
	// EnsurePreviewAllowed reports whether the session's project renders in a
	// browser at all; `ao preview` is refused outright when it does not.
	EnsurePreviewAllowed(ctx context.Context, id domain.SessionID) error
}

// PollerConfig configures preview poller timing and logging.
type PollerConfig struct {
	Interval time.Duration
	Logger   *slog.Logger
}

// Poller watches active worker workspaces for static frontend entrypoints and
// persists preview URL refreshes through the normal session service path.
type Poller struct {
	source   sessionPreviewSource
	setter   previewService
	baseURL  string
	interval time.Duration
	logger   *slog.Logger
	seen     map[domain.SessionID]entryState
	// discover resolves a workspace's previewable entry. It is a field so tests
	// can observe how often the poller reaches for the filesystem.
	discover func(workspacePath string) (Entry, bool)
}

type entryState struct {
	path    string
	modUnix int64
	size    int64
	// cleared is set when the poller itself cleared the preview URL because the
	// workspace entry was missing. When the file reappears, shouldRefresh uses
	// this to re-discover even though the revision was bumped by the clear.
	cleared bool
}

// NewPoller constructs a preview poller over the supplied session source and setter.
func NewPoller(source sessionPreviewSource, setter previewService, baseURL string, cfg PollerConfig) *Poller {
	p := &Poller{
		source:   source,
		setter:   setter,
		baseURL:  baseURL,
		interval: cfg.Interval,
		logger:   cfg.Logger,
		seen:     map[domain.SessionID]entryState{},
		discover: DiscoverEntrypoint,
	}
	if p.interval <= 0 {
		p.interval = DefaultPollInterval
	}
	if p.logger == nil {
		p.logger = slog.Default()
	}
	return p
}

// Start runs an immediate poll followed by interval polling until ctx is
// cancelled. The returned channel closes after the goroutine exits.
func (p *Poller) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.pollAndLog(ctx)
		ticker := time.NewTicker(p.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.pollAndLog(ctx)
			}
		}
	}()
	return done
}

func (p *Poller) pollAndLog(ctx context.Context) {
	if err := p.Poll(ctx); err != nil {
		p.logger.Error("preview poller: poll failed", "err", err)
	}
}

// Poll performs one deterministic scan of active worker sessions.
func (p *Poller) Poll(ctx context.Context) error {
	if p.source == nil || p.setter == nil {
		return nil
	}
	sessions, err := p.source.ListAllSessions(ctx)
	if err != nil {
		return fmt.Errorf("preview poller list sessions: %w", err)
	}
	activeIDs := make(map[domain.SessionID]struct{}, len(sessions))
	for _, sess := range sessions {
		if sess.IsTerminated {
			continue
		}
		activeIDs[sess.ID] = struct{}{}
		if sess.Kind != domain.KindWorker {
			continue
		}
		// An `ao preview <url>` target belongs to the agent that set it and is
		// never overwritten, so there is nothing to learn from the filesystem.
		// Checking that first keeps the common case off the disk entirely.
		if hasExplicitPreview(sess) {
			continue
		}
		entry, ok := p.discover(sess.Metadata.WorkspacePath)
		if !ok {
			if isDanglingWorkspacePreview(sess) {
				if _, err := p.setter.SetPreview(ctx, sess.ID, ""); err != nil {
					p.logger.Error("preview poller: failed to clear stale preview",
						"session", sess.ID, "err", err)
				}
				p.seen[sess.ID] = entryState{cleared: true}
			}
			continue
		}
		state := stateFor(entry)
		previous, seenBefore := p.seen[sess.ID]
		if seenBefore && previous == state {
			continue
		}
		target := FileURL(p.baseURL, sess.ID, entry.Path)
		if !p.shouldRefresh(sess, target, seenBefore) {
			p.seen[sess.ID] = state
			continue
		}
		// `ao preview` is refused for a project with no web UI, so the poller must
		// not publish behind the agent's back what the agent is forbidden to ask
		// for. Asked here — after the cheap checks and only when a write is
		// actually pending — so a steady workspace costs no database reads.
		//
		// A refusal deliberately leaves p.seen untouched: remembering it would
		// mark the entry handled, and turning "Web UI" on later would then never
		// publish until the entrypoint happened to change again.
		if err := p.setter.EnsurePreviewAllowed(ctx, sess.ID); err != nil {
			p.logger.Debug("preview poller: preview not allowed for session",
				"session", sess.ID, "err", err)
			continue
		}
		if _, err := p.setter.SetPreview(ctx, sess.ID, target); err != nil {
			return fmt.Errorf("preview poller set preview %s: %w", sess.ID, err)
		}
		p.seen[sess.ID] = state
	}
	for id := range p.seen {
		if _, ok := activeIDs[id]; !ok {
			delete(p.seen, id)
		}
	}
	return nil
}

func (p *Poller) shouldRefresh(sess domain.SessionRecord, target string, seenBefore bool) bool {
	current := strings.TrimSpace(sess.Metadata.PreviewURL)
	if current == "" {
		if !seenBefore {
			return sess.Metadata.PreviewRevision == 0
		}
		previous := p.seen[sess.ID]
		return previous.cleared
	}
	if current == target || isWorkspacePreviewURL(current, sess.ID) {
		return true
	}
	return isStaleWorkspacePath(current)
}

// hasExplicitPreview reports whether the session's stored preview target came
// from the agent rather than from this poller: a target the poller wrote is a
// preview/files URL for this session, and a legacy relative path is one it still
// needs to upgrade.
func hasExplicitPreview(sess domain.SessionRecord) bool {
	current := strings.TrimSpace(sess.Metadata.PreviewURL)
	if current == "" {
		return false
	}
	return !isWorkspacePreviewURL(current, sess.ID) && !isStaleWorkspacePath(current)
}

func stateFor(entry Entry) entryState {
	return entryState{path: entry.Path, modUnix: entry.ModTime.UnixNano(), size: entry.Size}
}

// isDanglingWorkspacePreview reports whether the session's panel points at a
// workspace file that no longer exists, so showing it would 404.
//
// A workspace preview URL is not proof the poller wrote it: a bare `ao preview`
// can pin any file in the workspace, including one the poller would never
// discover. Emptying the panel is therefore tied to the file being gone, not to
// the poller having stopped recognising it.
func isDanglingWorkspacePreview(sess domain.SessionRecord) bool {
	entry, ok := workspacePreviewEntry(sess.Metadata.PreviewURL, sess.ID)
	if !ok {
		return false
	}
	file, ok := ConfinedPath(sess.Metadata.WorkspacePath, entry)
	if !ok {
		return true
	}
	info, err := os.Stat(file)
	return err != nil || info.IsDir()
}

func isWorkspacePreviewURL(raw string, id domain.SessionID) bool {
	_, ok := workspacePreviewEntry(raw, id)
	return ok
}

// workspacePreviewEntry returns the workspace-relative path a preview/files URL
// for this session points at.
func workspacePreviewEntry(raw string, id domain.SessionID) (string, bool) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	previewPath := parsed.Path
	if previewPath == "" {
		previewPath = raw
	}
	prefix := "/api/v1/sessions/" + url.PathEscape(string(id)) + "/preview/files/"
	rest, found := strings.CutPrefix(previewPath, prefix)
	if !found {
		return "", false
	}
	segments := strings.Split(rest, "/")
	for i, segment := range segments {
		decoded, err := url.PathUnescape(segment)
		if err != nil {
			return "", false
		}
		segments[i] = decoded
	}
	return strings.Join(segments, "/"), true
}

func isStaleWorkspacePath(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.Contains(raw, "://") || filepath.IsAbs(raw) || isWindowsAbs(raw) {
		return false
	}
	return !strings.Contains(raw, ":")
}

func isWindowsAbs(raw string) bool {
	return len(raw) >= 3 && ((raw[0] >= 'a' && raw[0] <= 'z') || (raw[0] >= 'A' && raw[0] <= 'Z')) && raw[1] == ':' && (raw[2] == '\\' || raw[2] == '/')
}
