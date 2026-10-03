// Package learning is the daemon-facing service for learning capture: it takes
// the transcript bookkeeping agent hooks report, and answers what capture has
// stored and how healthy it is. The capture loop itself lives in
// observe/learncapture; the contract (what is read, kept, sent) is the
// "Learning capture" section of docs/architecture.md.
package learning

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ErrInvalidTranscriptPath is a reported path that does not point at a
// transcript inside Claude Code's project directories.
var ErrInvalidTranscriptPath = errors.New("transcript path is not a Claude Code transcript")

// ErrUnknownProject is a project id the store does not have.
var ErrUnknownProject = errors.New("unknown project")

// ErrStillLearning refuses to forget a project that still learns from
// sessions: capture would read every transcript again on its next pass, and
// the forget would undo itself.
var ErrStillLearning = errors.New("project still learns from sessions; turn learnFromSessions off first")

// Store is the persistence the service needs.
type Store interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
	ListProjects(ctx context.Context) ([]domain.ProjectRecord, error)
	RecordTranscriptRef(ctx context.Context, ref domain.TranscriptRef) error
	RecordPromptFingerprint(ctx context.Context, fp domain.PromptFingerprint) error
	ListLearnCursors(ctx context.Context, projectID domain.ProjectID) ([]domain.LearnCursor, error)
	ListLearnExcerpts(ctx context.Context, projectID domain.ProjectID, limit int) ([]domain.LearnExcerpt, error)
	ForgetLearning(ctx context.Context, projectID domain.ProjectID) (int, error)
	LearnCounts(ctx context.Context, projectID domain.ProjectID, unmatchedBefore time.Time) (domain.LearnCounts, error)
}

// Service is the learning-capture service.
type Service struct {
	store Store
	// validPath confines a hook-reported path to Claude Code's transcripts.
	validPath func(string) bool
	clock     func() time.Time
}

// New builds the service. validPath is the claude-code adapter's
// IsTranscriptPath in production.
func New(st Store, validPath func(string) bool) *Service {
	return &Service{store: st, validPath: validPath, clock: func() time.Time { return time.Now().UTC() }}
}

// RecordTranscriptRef files what a session's hook reported: the transcript it
// is writing, always (a path is not content), and the prompt's fingerprint only
// when the session's project learns from sessions.
func (s *Service) RecordTranscriptRef(ctx context.Context, id domain.SessionID, ref domain.HookTranscriptRef) error {
	if s.validPath == nil || !s.validPath(ref.TranscriptPath) {
		return ErrInvalidTranscriptPath
	}
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return ports.ErrSessionNotFound
	}
	now := s.clock()
	if err := s.store.RecordTranscriptRef(ctx, domain.TranscriptRef{
		SessionID:       id,
		Path:            ref.TranscriptPath,
		ClaudeSessionID: ref.ClaudeSessionID,
		FirstSeenAt:     now,
		LastSeenAt:      now,
	}); err != nil {
		return err
	}
	if ref.PromptSHA256 == "" {
		return nil
	}
	proj, ok, err := s.store.GetProject(ctx, string(rec.ProjectID))
	if err != nil || !ok || !proj.Config.LearnFromSessions {
		return err
	}
	return s.store.RecordPromptFingerprint(ctx, domain.PromptFingerprint{
		SessionID:       id,
		ProjectID:       rec.ProjectID,
		ClaudeSessionID: ref.ClaudeSessionID,
		SHA256:          ref.PromptSHA256,
		Bytes:           ref.PromptBytes,
		SubmittedAt:     now,
	})
}

// UnmatchedGrace is how long a prompt the hook saw may stay unfound before it
// counts against the parser: capture runs every few minutes and leaves the
// newest turn open until the agent has answered it.
const UnmatchedGrace = 30 * time.Minute

// StaleAfter is the age at which an unread transcript is reported as at risk:
// Claude Code deletes a transcript 30 days after it was last written.
const StaleAfter = 25 * 24 * time.Hour

// ProjectStatus is capture's account of one project.
type ProjectStatus struct {
	ProjectID         domain.ProjectID
	Enabled           bool
	Transcripts       int
	Excerpts          int
	BySourceClass     map[domain.LearnSourceClass]int
	HumanTurns        int
	MachineTurns      int
	Prompts           int
	UnmatchedPrompts  int
	LastCaptureAt     time.Time
	FailingFiles      []FailingFile
	AtRiskTranscripts int
}

// FailingFile is a transcript whose last pass failed.
type FailingFile struct {
	Path  string
	Error string
}

// Status reports every project that learns from sessions, or holds what an
// earlier opt-in captured.
func (s *Service) Status(ctx context.Context) ([]ProjectStatus, error) {
	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	now := s.clock()
	var out []ProjectStatus
	for _, p := range projects {
		id := domain.ProjectID(p.ID)
		counts, err := s.store.LearnCounts(ctx, id, now.Add(-UnmatchedGrace))
		if err != nil {
			return nil, err
		}
		if !p.Config.LearnFromSessions && counts.Excerpts == 0 {
			continue
		}
		cursors, err := s.store.ListLearnCursors(ctx, id)
		if err != nil {
			return nil, err
		}
		st := ProjectStatus{
			ProjectID:        id,
			Enabled:          p.Config.LearnFromSessions,
			Transcripts:      len(cursors),
			Excerpts:         counts.Excerpts,
			BySourceClass:    counts.BySourceClass,
			Prompts:          counts.Prompts,
			UnmatchedPrompts: counts.UnmatchedPrompts,
		}
		for _, c := range cursors {
			st.HumanTurns += c.HumanTurns
			st.MachineTurns += c.MachineTurns
			if c.UpdatedAt.After(st.LastCaptureAt) {
				st.LastCaptureAt = c.UpdatedAt
			}
			if c.LastError != "" {
				st.FailingFiles = append(st.FailingFiles, FailingFile{Path: c.TranscriptPath, Error: c.LastError})
			}
			behind := c.ByteOffset < c.FileSize && c.PendingCarry == ""
			if (behind || c.LastError != "") && !c.FileMTime.IsZero() && now.Sub(c.FileMTime) >= StaleAfter {
				st.AtRiskTranscripts++
			}
		}
		sort.Slice(st.FailingFiles, func(i, j int) bool { return st.FailingFiles[i].Path < st.FailingFiles[j].Path })
		out = append(out, st)
	}
	return out, nil
}

// MaxExcerptsLimit bounds one excerpts read.
const MaxExcerptsLimit = 500

// Excerpts returns the project's newest captured turns first.
func (s *Service) Excerpts(ctx context.Context, projectID domain.ProjectID, limit int) ([]domain.LearnExcerpt, error) {
	if _, ok, err := s.store.GetProject(ctx, string(projectID)); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownProject, projectID)
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > MaxExcerptsLimit {
		limit = MaxExcerptsLimit
	}
	return s.store.ListLearnExcerpts(ctx, projectID, limit)
}

// Forget deletes everything capture kept for a project and returns how many
// turns went. The project must have learning off first.
func (s *Service) Forget(ctx context.Context, projectID domain.ProjectID) (int, error) {
	proj, ok, err := s.store.GetProject(ctx, string(projectID))
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrUnknownProject, projectID)
	}
	if proj.Config.LearnFromSessions {
		return 0, ErrStillLearning
	}
	return s.store.ForgetLearning(ctx, projectID)
}
