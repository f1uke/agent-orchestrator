// Package learncapture is the background loop that keeps what the human taught
// AO's agents after Claude Code deletes the transcript it was said in.
//
// For every project with learnFromSessions on, it finds the Claude Code
// transcripts of the project's sessions, reads each one from where it last
// stopped (internal/learn/transcript), and stores the human's turns - redacted,
// each with a bounded window of the agent's activity around it - as
// learn_excerpt rows. It calls no model and sends nothing anywhere; a later
// stage reads the excerpts.
//
// It runs continuously rather than only for transcripts close to Claude Code's
// 30-day pruning, because a transcript read once never needs reading again, and
// a single path is simpler to trust than a deadline-driven second one.
//
// Like the token-usage observer it is purely additive: it never touches session
// lifecycle, a failure on one file is recorded on that file's cursor and never
// stops the others, and a project with the switch off is never opened.
package learncapture

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/redact"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/transcript"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe"
)

// DefaultTickInterval is how often capture looks for new turns. Nothing here is
// urgent - the deadline it races is 30 days - and a pass over a file that has
// not changed is a stat, so ten minutes keeps excerpts close to live at no cost.
const DefaultTickInterval = 10 * time.Minute

// DefaultQuietAfter is how long a transcript must go unwritten before capture
// closes the window of its last human turn at end of file. Until then the agent
// may still be answering, and its answer belongs in the window.
const DefaultQuietAfter = 10 * time.Minute

// Store is the persistence capture needs.
type Store interface {
	ListProjects(ctx context.Context) ([]domain.ProjectRecord, error)
	ListSessions(ctx context.Context, project domain.ProjectID) ([]domain.SessionRecord, error)
	ListTranscriptRefs(ctx context.Context, project domain.ProjectID) ([]domain.TranscriptRef, error)
	ListDeliveredFingerprints(ctx context.Context, session domain.SessionID) ([]domain.DeliveredFingerprint, error)
	GetLearnCursor(ctx context.Context, path string) (domain.LearnCursor, bool, error)
	CommitLearnPass(ctx context.Context, cursor domain.LearnCursor, excerpts []domain.LearnExcerpt, matches []domain.PromptMatch, now time.Time) (int, error)
	SetLearnCursorError(ctx context.Context, cursor domain.LearnCursor, msg string, now time.Time) error
}

// Locator says where Claude Code keeps a session's transcripts. The daemon
// wires the claude-code adapter's helpers; tests point it at a temp dir.
type Locator struct {
	// WorkspaceDir is the directory holding every conversation started in a
	// workspace.
	WorkspaceDir func(workspacePath string) (string, error)
	// Pinned is the transcript of the conversation AO launched for a session.
	Pinned func(workspacePath, aoSessionID string) (string, error)
}

// Config holds the observer's knobs; zero values use production defaults.
type Config struct {
	Tick       time.Duration
	QuietAfter time.Duration
	Clock      func() time.Time
	Logger     *slog.Logger
	// Environ is the daemon environment searched for secret values to redact.
	// Nil uses os.Environ.
	Environ func() []string
	// OnTick fires before each poll; the daemon's loop-timing seam.
	OnTick func()
}

// Observer is the capture loop.
type Observer struct {
	store      Store
	locate     Locator
	tick       time.Duration
	quietAfter time.Duration
	clock      func() time.Time
	logger     *slog.Logger
	environ    func() []string
	onTick     func()
}

// New builds an Observer.
func New(store Store, locate Locator, cfg Config) *Observer {
	o := &Observer{
		store:      store,
		locate:     locate,
		tick:       cfg.Tick,
		quietAfter: cfg.QuietAfter,
		clock:      cfg.Clock,
		logger:     cfg.Logger,
		environ:    cfg.Environ,
		onTick:     cfg.OnTick,
	}
	if o.tick <= 0 {
		o.tick = DefaultTickInterval
	}
	if o.quietAfter <= 0 {
		o.quietAfter = DefaultQuietAfter
	}
	if o.clock == nil {
		o.clock = time.Now
	}
	if o.logger == nil {
		o.logger = slog.Default()
	}
	if o.environ == nil {
		o.environ = os.Environ
	}
	return o
}

// Start launches the loop and returns a channel that closes when it exits.
func (o *Observer) Start(ctx context.Context) <-chan struct{} {
	return observe.StartPollLoop(ctx, o.tick, o.Poll, o.logger, "learn-capture", o.onTick)
}

// Poll runs one capture pass over every opted-in project. It returns an error
// only when the project list itself cannot be read.
func (o *Observer) Poll(ctx context.Context) error {
	projects, err := o.store.ListProjects(ctx)
	if err != nil {
		return err
	}
	var enabled []domain.ProjectRecord
	for _, p := range projects {
		if p.Config.LearnFromSessions && p.ArchivedAt.IsZero() {
			enabled = append(enabled, p)
		}
	}
	if len(enabled) == 0 {
		return nil
	}
	// The dictionary is rebuilt every pass: a test account added to a script
	// store today must be redacted from today's turns.
	redactor := redact.New(learn.Dictionary(projects, o.environ()))
	for _, p := range enabled {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := o.captureProject(ctx, p, redactor); err != nil {
			o.logger.Warn("learn-capture: project failed", "project", p.ID, "err", err)
		}
	}
	return nil
}

// candidate is one transcript file and the session it belongs to.
type candidate struct {
	path        string
	session     domain.SessionRecord
	attribution domain.LearnAttribution
}

func (o *Observer) captureProject(ctx context.Context, p domain.ProjectRecord, redactor *redact.Redactor) error {
	projectID := domain.ProjectID(p.ID)
	sessions, err := o.store.ListSessions(ctx, projectID)
	if err != nil {
		return err
	}
	refs, err := o.store.ListTranscriptRefs(ctx, projectID)
	if err != nil {
		return err
	}
	cands := o.candidates(ctx, sessions, refs)
	briefs := briefFingerprints(sessions)
	for _, c := range cands {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		o.captureFile(ctx, projectID, c, briefs, redactor)
	}
	return nil
}

// candidates finds every transcript of the project's sessions: the file AO
// pinned for each session, the files its hooks reported, and any other file in
// a session's worktree directory whose recorded working directory IS that
// worktree - a `/clear` continuation. A file in a worktree's directory that was
// started from somewhere else (a subdirectory, a test's scratch repo) is not
// the session's conversation and is left alone.
func (o *Observer) candidates(ctx context.Context, sessions []domain.SessionRecord, refs []domain.TranscriptRef) []candidate {
	byPath := map[string]candidate{}
	byID := map[domain.SessionID]domain.SessionRecord{}
	byDir := map[string][]domain.SessionRecord{}
	for _, s := range sessions {
		byID[s.ID] = s
		if s.Harness != domain.HarnessClaudeCode || s.Metadata.WorkspacePath == "" {
			continue
		}
		if pinned, err := o.locate.Pinned(s.Metadata.WorkspacePath, string(s.ID)); err == nil && fileExists(pinned) {
			byPath[pinned] = candidate{path: pinned, session: s, attribution: domain.LearnAttributionPinned}
		}
		if dir, err := o.locate.WorkspaceDir(s.Metadata.WorkspacePath); err == nil {
			byDir[dir] = append(byDir[dir], s)
		}
	}
	for _, r := range refs {
		if _, ok := byPath[r.Path]; ok || !fileExists(r.Path) {
			continue
		}
		if s, ok := byID[r.SessionID]; ok {
			byPath[r.Path] = candidate{path: r.Path, session: s, attribution: domain.LearnAttributionHook}
		}
	}
	for dir, owners := range byDir {
		files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
		for _, f := range files {
			if _, ok := byPath[f]; ok {
				continue
			}
			if cur, ok, err := o.store.GetLearnCursor(ctx, f); err == nil && ok {
				// Attributed on an earlier pass; keep that answer rather than
				// re-reading the head every tick.
				if s, ok := byID[cur.SessionID]; ok {
					byPath[f] = candidate{path: f, session: s, attribution: cur.Attribution}
				}
				continue
			}
			head, err := transcript.ReadHead(f)
			if err != nil || !sameDir(head.CWD, owners[0].Metadata.WorkspacePath) {
				continue
			}
			byPath[f] = candidate{path: f, session: ownerAt(owners, head.FirstAt), attribution: domain.LearnAttributionWorkspace}
		}
	}
	out := make([]candidate, 0, len(byPath))
	for _, c := range byPath {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

// ownerAt picks, among the sessions sharing one worktree, the one that was
// live when a conversation began: the newest created at or before that moment.
// Every orchestrator of a project shares a worktree, as do a crew's dev and qa.
func ownerAt(owners []domain.SessionRecord, at time.Time) domain.SessionRecord {
	sorted := append([]domain.SessionRecord(nil), owners...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].CreatedAt.Before(sorted[j].CreatedAt) })
	best := sorted[0]
	for _, s := range sorted {
		if !at.IsZero() && s.CreatedAt.After(at) {
			break
		}
		best = s
	}
	return best
}

func (o *Observer) captureFile(ctx context.Context, projectID domain.ProjectID, c candidate, briefs map[string]transcript.Delivery, redactor *redact.Redactor) {
	now := o.clock()
	fi, err := os.Stat(c.path)
	if err != nil {
		return // pruned or moved since enumeration; nothing to read
	}
	base := domain.LearnCursor{
		TranscriptPath: c.path,
		ProjectID:      projectID,
		SessionID:      c.session.ID,
		Attribution:    c.attribution,
	}
	cur, have, err := o.store.GetLearnCursor(ctx, c.path)
	if err != nil {
		o.logger.Warn("learn-capture: read cursor failed", "path", c.path, "err", err)
		return
	}
	quiet := now.Sub(fi.ModTime()) >= o.quietAfter
	unchanged := have && cur.LastError == "" && cur.FileSize == fi.Size() && cur.FileMTime.Equal(fi.ModTime())
	if unchanged && (cur.PendingCarry == "" || !quiet) {
		return
	}
	offset, carry := int64(0), (*transcript.Carry)(nil)
	humanTotal, machineTotal := 0, 0
	if have && fi.Size() >= cur.ByteOffset {
		offset, humanTotal, machineTotal = cur.ByteOffset, cur.HumanTurns, cur.MachineTurns
		if cur.PendingCarry != "" {
			var cc transcript.Carry
			if json.Unmarshal([]byte(cur.PendingCarry), &cc) == nil {
				carry = &cc
			}
		}
	}
	// A file shorter than the cursor was rewritten; it is read again from the
	// top, and the (transcript, turn) key keeps already-stored turns as they are.

	delivered, err := o.deliveredFor(ctx, c.session.ID, briefs)
	if err != nil {
		o.recordError(ctx, base, err, now)
		return
	}
	res, err := transcript.Read(c.path, offset, carry, transcript.Options{
		Delivered: delivered,
		Redactor:  redactor,
		FileQuiet: quiet,
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return
		}
		o.recordError(ctx, base, err, now)
		return
	}

	excerpts := make([]domain.LearnExcerpt, 0, len(res.Turns))
	for _, t := range res.Turns {
		at := t.At
		if at.IsZero() {
			at = fi.ModTime().UTC()
		}
		excerpts = append(excerpts, domain.LearnExcerpt{
			ProjectID:      projectID,
			SessionID:      c.session.ID,
			TranscriptPath: c.path,
			TurnUUID:       t.UUID,
			TurnAt:         at,
			SourceClass:    t.Source,
			CWD:            t.CWD,
			GitBranch:      t.GitBranch,
			Before:         t.Before,
			HumanText:      t.Text,
			After:          t.After,
			Redactions:     t.Redactions,
			CreatedAt:      now,
		})
	}
	matches := make([]domain.PromptMatch, 0, len(res.PaneFingerprints))
	for _, fp := range res.PaneFingerprints {
		matches = append(matches, domain.PromptMatch{SessionID: c.session.ID, SHA256: fp})
	}
	next := base
	next.ByteOffset = res.NextOffset
	next.FileSize = fi.Size()
	next.FileMTime = fi.ModTime()
	next.HumanTurns = humanTotal + res.HumanTurns
	next.MachineTurns = machineTotal + res.MachineTurns
	if res.Carry != nil {
		b, err := json.Marshal(res.Carry)
		if err != nil {
			o.recordError(ctx, base, err, now)
			return
		}
		next.PendingCarry = string(b)
	}
	if _, err := o.store.CommitLearnPass(ctx, next, excerpts, matches, now); err != nil {
		o.recordError(ctx, base, err, now)
	}
}

// deliveredFor is everything AO put into the session, by body fingerprint,
// plus the brief of every session in the project - a brief is never the
// human's, whichever transcript it turns up in.
func (o *Observer) deliveredFor(ctx context.Context, id domain.SessionID, briefs map[string]transcript.Delivery) (map[string]transcript.Delivery, error) {
	rows, err := o.store.ListDeliveredFingerprints(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make(map[string]transcript.Delivery, len(briefs)+len(rows))
	for k, v := range briefs {
		out[k] = v
	}
	for _, r := range rows {
		if _, seen := out[r.SHA256]; !seen {
			out[r.SHA256] = transcript.Delivery{Author: r.Author, Trigger: r.Trigger}
		}
	}
	return out, nil
}

func (o *Observer) recordError(ctx context.Context, cursor domain.LearnCursor, cause error, now time.Time) {
	o.logger.Warn("learn-capture: file failed", "path", cursor.TranscriptPath, "err", cause)
	if err := o.store.SetLearnCursorError(ctx, cursor, cause.Error(), now); err != nil {
		o.logger.Warn("learn-capture: record failure failed", "path", cursor.TranscriptPath, "err", err)
	}
}

func briefFingerprints(sessions []domain.SessionRecord) map[string]transcript.Delivery {
	out := map[string]transcript.Delivery{}
	for _, s := range sessions {
		if s.Metadata.Prompt == "" {
			continue
		}
		fp, _ := transcript.Fingerprint(s.Metadata.Prompt)
		out[fp] = transcript.Delivery{Author: domain.DeliveryAuthorAO, Trigger: transcript.TriggerBrief}
	}
	return out
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// sameDir compares a transcript's recorded cwd with a session's worktree, both
// symlink-resolved: Claude Code records the resolved path.
func sameDir(cwd, workspace string) bool {
	if cwd == "" || workspace == "" {
		return false
	}
	return resolve(cwd) == resolve(workspace)
}

func resolve(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(r)
	}
	return filepath.Clean(p)
}
