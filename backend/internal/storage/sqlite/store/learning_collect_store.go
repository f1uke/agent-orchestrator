package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// AbandonRunningLearnJobs marks every model run still 'running' as abandoned.
// It is called once at daemon start: a run in flight died with the previous
// daemon, and its turns were never marked collected, so they simply run again.
func (s *Store) AbandonRunningLearnJobs(ctx context.Context, now time.Time) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.AbandonRunningLearnJobs(ctx, sql.NullTime{Time: now, Valid: true})
	if err != nil {
		return 0, fmt.Errorf("abandon running learn jobs: %w", err)
	}
	return int(n), nil
}

// ListCollectCandidates returns, per session, the captured turns no model has
// seen yet, oldest session first.
func (s *Store) ListCollectCandidates(ctx context.Context, projectID domain.ProjectID) ([]domain.LearnCollectCandidate, error) {
	rows, err := s.qr.ListUncollectedTurnTimes(ctx, string(projectID))
	if err != nil {
		return nil, fmt.Errorf("list collect candidates for %s: %w", projectID, err)
	}
	index := map[string]int{}
	var out []domain.LearnCollectCandidate
	for _, r := range rows {
		i, seen := index[r.SessionID]
		if !seen {
			index[r.SessionID] = len(out)
			out = append(out, domain.LearnCollectCandidate{SessionID: domain.SessionID(r.SessionID), OldestAt: r.TurnAt, NewestAt: r.TurnAt})
			i = len(out) - 1
		}
		c := &out[i]
		c.Turns++
		if r.TurnAt.Before(c.OldestAt) {
			c.OldestAt = r.TurnAt
		}
		if r.TurnAt.After(c.NewestAt) {
			c.NewestAt = r.TurnAt
		}
	}
	return out, nil
}

// ListUncollectedExcerpts returns a session's oldest uncollected turns.
func (s *Store) ListUncollectedExcerpts(ctx context.Context, projectID domain.ProjectID, sessionID domain.SessionID, limit int) ([]domain.LearnExcerpt, error) {
	rows, err := s.qr.ListUncollectedExcerptsBySession(ctx, gen.ListUncollectedExcerptsBySessionParams{
		ProjectID: string(projectID),
		SessionID: string(sessionID),
		Limit:     int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list uncollected excerpts for %s: %w", sessionID, err)
	}
	out := make([]domain.LearnExcerpt, 0, len(rows))
	for _, r := range rows {
		out = append(out, excerptFromRow(gen.LearnExcerpt{
			ID: r.ID, ProjectID: r.ProjectID, SessionID: r.SessionID, TranscriptPath: r.TranscriptPath,
			TurnUuid: r.TurnUuid, TurnAt: r.TurnAt, SourceClass: r.SourceClass, Cwd: r.Cwd,
			GitBranch: r.GitBranch, BeforeJson: r.BeforeJson, HumanText: r.HumanText,
			AfterJson: r.AfterJson, RedactionsJson: r.RedactionsJson, CreatedAt: r.CreatedAt,
		}))
	}
	return out, nil
}

// StartLearnJob records a model run that is about to start and returns its id.
func (s *Store) StartLearnJob(ctx context.Context, job domain.LearnJob) (int64, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	id, err := s.qw.InsertLearnJob(ctx, gen.InsertLearnJobParams{
		ProjectID: string(job.ProjectID),
		SessionID: string(job.SessionID),
		Model:     job.Model,
		Turns:     int64(job.Turns),
		StartedAt: job.StartedAt,
	})
	if err != nil {
		return 0, fmt.Errorf("start learn job: %w", err)
	}
	return id, nil
}

// FailLearnJob closes a run that failed. Its turns stay uncollected.
func (s *Store) FailLearnJob(ctx context.Context, job domain.LearnJob) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	job.State = domain.LearnJobFailed
	if err := s.qw.FinishLearnJob(ctx, finishParams(job)); err != nil {
		return fmt.Errorf("fail learn job %d: %w", job.ID, err)
	}
	return nil
}

func finishParams(job domain.LearnJob) gen.FinishLearnJobParams {
	return gen.FinishLearnJobParams{
		State:        string(job.State),
		Drafts:       int64(job.Drafts),
		Rejected:     int64(job.Rejected),
		CostUsd:      job.CostUSD,
		InputTokens:  job.InputTokens,
		OutputTokens: job.OutputTokens,
		DurationMs:   job.DurationMS,
		Error:        job.Error,
		StderrTail:   job.StderrTail,
		FinishedAt:   sql.NullTime{Time: job.FinishedAt, Valid: !job.FinishedAt.IsZero()},
		ID:           job.ID,
	}
}

// StatementHash identifies a draft's statement within a session, so the same
// lesson extracted twice is stored once. Case and whitespace do not matter.
func StatementHash(statement string) string {
	norm := strings.ToLower(strings.Join(strings.Fields(statement), " "))
	sum := sha256.Sum256([]byte(norm))
	return hex.EncodeToString(sum[:])
}

// CommitLearnJob lands one successful model run atomically: its drafts, the
// drafts they take back (marked reversed), the turns it saw (marked collected)
// and the run itself. It returns how many drafts were new.
func (s *Store) CommitLearnJob(ctx context.Context, job domain.LearnJob, drafts []domain.LearnDraft, excerptIDs []int64, now time.Time) (int, error) {
	params := make([]gen.InsertLearnDraftParams, 0, len(drafts))
	for _, d := range drafts {
		evidence, err := json.Marshal(d.EvidenceExcerptIDs)
		if err != nil {
			return 0, fmt.Errorf("encode draft evidence: %w", err)
		}
		weak := int64(0)
		if d.Weak {
			weak = 1
		}
		params = append(params, gen.InsertLearnDraftParams{
			ProjectID:       string(d.ProjectID),
			SessionID:       string(d.SessionID),
			TaskKey:         d.TaskKey,
			JobID:           job.ID,
			Kind:            string(d.Kind),
			Statement:       d.Statement,
			StatementHash:   StatementHash(d.Statement),
			AppliesWhen:     d.AppliesWhen,
			ScopeHint:       d.ScopeHint,
			Confidence:      d.Confidence,
			About:           string(d.About),
			Quote:           d.Quote,
			AnchorExcerptID: d.AnchorExcerptID,
			EvidenceJson:    string(evidence),
			AgentBefore:     d.AgentBefore,
			Weak:            weak,
			SupersedesID:    d.SupersedesID,
			CreatedAt:       now,
		})
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	inserted := 0
	err := s.inTx(ctx, "commit learn job", func(q *gen.Queries) error {
		for i, p := range params {
			id, err := q.InsertLearnDraft(ctx, p)
			if errors.Is(err, sql.ErrNoRows) {
				continue // the session already holds this statement
			}
			if err != nil {
				return fmt.Errorf("insert draft: %w", err)
			}
			if id > 0 {
				inserted++
			}
			if sup := drafts[i].SupersedesID; sup > 0 {
				if err := q.SetLearnDraftStatus(ctx, gen.SetLearnDraftStatusParams{
					Status: string(domain.LearnDraftReversed), ID: sup, SessionID: p.SessionID,
				}); err != nil {
					return fmt.Errorf("reverse draft %d: %w", sup, err)
				}
			}
		}
		for _, id := range excerptIDs {
			if err := q.MarkExcerptCollected(ctx, gen.MarkExcerptCollectedParams{
				CollectedAt: sql.NullTime{Time: now, Valid: true}, CollectedJobID: job.ID, ID: id,
			}); err != nil {
				return fmt.Errorf("mark excerpt %d collected: %w", id, err)
			}
		}
		job.State = domain.LearnJobDone
		job.Drafts = inserted
		return q.FinishLearnJob(ctx, finishParams(job))
	})
	if err != nil {
		return 0, err
	}
	return inserted, nil
}

// RecentLearnJobs returns a session's newest runs first.
func (s *Store) RecentLearnJobs(ctx context.Context, sessionID domain.SessionID, limit int) ([]domain.LearnJob, error) {
	rows, err := s.qr.ListRecentLearnJobsBySession(ctx, gen.ListRecentLearnJobsBySessionParams{
		SessionID: string(sessionID), Limit: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list recent learn jobs for %s: %w", sessionID, err)
	}
	out := make([]domain.LearnJob, 0, len(rows))
	for _, r := range rows {
		j := domain.LearnJob{ID: r.ID, SessionID: sessionID, State: domain.LearnJobState(r.State), StartedAt: r.StartedAt, Error: r.Error}
		if r.FinishedAt.Valid {
			j.FinishedAt = r.FinishedAt.Time
		}
		out = append(out, j)
	}
	return out, nil
}

// LearnSpendSince sums what every model run since the moment cost, in US
// dollars as the harness reports them.
func (s *Store) LearnSpendSince(ctx context.Context, since time.Time) (float64, error) {
	v, err := s.qr.SumLearnJobCostSince(ctx, since)
	if err != nil {
		return 0, fmt.Errorf("sum learn job cost: %w", err)
	}
	return v, nil
}

// LastFailedLearnJob returns the project's most recent failed run.
func (s *Store) LastFailedLearnJob(ctx context.Context, projectID domain.ProjectID) (domain.LearnJob, bool, error) {
	r, err := s.qr.LastFailedLearnJob(ctx, string(projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LearnJob{}, false, nil
	}
	if err != nil {
		return domain.LearnJob{}, false, fmt.Errorf("last failed learn job: %w", err)
	}
	return domain.LearnJob{
		ID: r.ID, ProjectID: domain.ProjectID(r.ProjectID), SessionID: domain.SessionID(r.SessionID),
		State: domain.LearnJobFailed, Error: r.Error, StderrTail: r.StderrTail, StartedAt: r.StartedAt,
	}, true, nil
}

// LastFinishedLearnJob returns the project's most recent finished run, done or
// failed.
func (s *Store) LastFinishedLearnJob(ctx context.Context, projectID domain.ProjectID) (domain.LearnJob, bool, error) {
	r, err := s.qr.LastFinishedLearnJob(ctx, string(projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LearnJob{}, false, nil
	}
	if err != nil {
		return domain.LearnJob{}, false, fmt.Errorf("last finished learn job: %w", err)
	}
	j := domain.LearnJob{ID: r.ID, ProjectID: projectID, State: domain.LearnJobState(r.State)}
	if r.FinishedAt.Valid {
		j.FinishedAt = r.FinishedAt.Time
	}
	return j, true, nil
}

// OpenDrafts returns a session's open drafts, oldest first: the model sees
// them so it can say a new turn takes one back.
func (s *Store) OpenDrafts(ctx context.Context, sessionID domain.SessionID) ([]domain.LearnDraft, error) {
	rows, err := s.qr.ListOpenDraftsBySession(ctx, string(sessionID))
	if err != nil {
		return nil, fmt.Errorf("list open drafts for %s: %w", sessionID, err)
	}
	out := make([]domain.LearnDraft, 0, len(rows))
	for _, r := range rows {
		out = append(out, domain.LearnDraft{ID: r.ID, SessionID: sessionID, Statement: r.Statement, Status: domain.LearnDraftOpen})
	}
	return out, nil
}

// ListLearnDrafts returns the project's newest drafts first.
func (s *Store) ListLearnDrafts(ctx context.Context, projectID domain.ProjectID, limit int) ([]domain.LearnDraft, error) {
	rows, err := s.qr.ListLearnDraftsByProject(ctx, gen.ListLearnDraftsByProjectParams{
		ProjectID: string(projectID), Limit: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list learn drafts for %s: %w", projectID, err)
	}
	out := make([]domain.LearnDraft, 0, len(rows))
	for _, r := range rows {
		d := domain.LearnDraft{
			ID: r.ID, ProjectID: domain.ProjectID(r.ProjectID), SessionID: domain.SessionID(r.SessionID),
			TaskKey: r.TaskKey, JobID: r.JobID, Kind: domain.LearnDraftKind(r.Kind), Statement: r.Statement,
			AppliesWhen: r.AppliesWhen, ScopeHint: r.ScopeHint, Confidence: r.Confidence, About: domain.LearnDraftAbout(r.About), Quote: r.Quote,
			AnchorExcerptID: r.AnchorExcerptID, AgentBefore: r.AgentBefore, Weak: r.Weak != 0,
			SupersedesID: r.SupersedesID, Status: domain.LearnDraftStatus(r.Status), CreatedAt: r.CreatedAt,
			AnchorSourceClass: domain.LearnSourceClass(r.AnchorSourceClass.String),
		}
		if r.AnchorTurnAt.Valid {
			d.AnchorTurnAt = r.AnchorTurnAt.Time
		}
		_ = json.Unmarshal([]byte(r.EvidenceJson), &d.EvidenceExcerptIDs)
		out = append(out, d)
	}
	return out, nil
}

// LearnCollectCounts tallies a project's uncollected turns and its drafts by
// status.
func (s *Store) LearnCollectCounts(ctx context.Context, projectID domain.ProjectID) (domain.LearnCollectCounts, error) {
	out := domain.LearnCollectCounts{Drafts: map[domain.LearnDraftStatus]int{}}
	n, err := s.qr.CountUncollectedExcerpts(ctx, string(projectID))
	if err != nil {
		return out, fmt.Errorf("count uncollected excerpts: %w", err)
	}
	out.Uncollected = int(n)
	rows, err := s.qr.CountLearnDraftsByStatus(ctx, string(projectID))
	if err != nil {
		return out, fmt.Errorf("count learn drafts: %w", err)
	}
	for _, r := range rows {
		out.Drafts[domain.LearnDraftStatus(r.Status)] = int(r.Drafts)
	}
	return out, nil
}
