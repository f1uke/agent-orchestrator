package testiny

import (
	"context"
	"fmt"
	"strings"

	testinyadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/testiny"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// RecordResults records case results in a run linked to the task, and returns
// the run read fresh from Testiny.
//
// by is the session id of the agent recording them, or "" for a person in the
// app. A person may set any case. An agent must be on the task (its qa, when
// the task has one), and may not change a case a person set: see
// agentMayOverwrite. The whole batch is checked before anything is written.
//
// Testiny cannot tell an agent's write from a person's, so every result that
// reaches Testiny is logged with by and sha (the commit the agent tested). A
// partial write logs what reached Testiny before returning the error.
func (s *Service) RecordResults(ctx context.Context, task domain.SessionID, id domain.TestinyRunID, in []domain.TestinyResult, by, sha string) (domain.TestinyRunView, error) {
	cfg, err := s.config(ctx, task)
	if err != nil {
		return domain.TestinyRunView{}, err
	}
	link, err := s.linkOf(ctx, task, id)
	if err != nil {
		return domain.TestinyRunView{}, err
	}
	results, err := domain.ParseTestinyResults(in)
	if err != nil {
		return domain.TestinyRunView{}, err
	}
	if by != "" {
		if err := s.mayWrite(ctx, task, domain.SessionID(by)); err != nil {
			return domain.TestinyRunView{}, err
		}
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	current, err := s.reader.Results(ctx, id)
	if err != nil {
		return domain.TestinyRunView{}, err
	}
	latest, err := s.links.LatestTestinyResults(ctx, task, id)
	if err != nil {
		return domain.TestinyRunView{}, err
	}
	if err := checkBatch(id, results, current.Cases, latest, by != ""); err != nil {
		return domain.TestinyRunView{}, err
	}
	project, err := s.project(ctx, cfg.TestinyProject)
	if err != nil {
		return domain.TestinyRunView{}, err
	}

	written, writeErr := s.reader.SetResults(ctx, id, project.ID, results)
	s.forget(id)
	if len(written) > 0 {
		at := s.now().UTC()
		entries := make([]domain.TestinyResultEntry, len(written))
		for i, r := range written {
			entries[i] = domain.TestinyResultEntry{SessionID: task, RunID: id, TestinyResult: r, SetBy: by, SHA: sha, CreatedAt: at}
		}
		if err := s.links.AppendTestinyResults(ctx, entries); err != nil {
			return domain.TestinyRunView{}, fmt.Errorf("%d results reached Testiny but could not be logged: %w", len(written), err)
		}
	}
	if writeErr != nil {
		return domain.TestinyRunView{}, writeErr
	}

	fresh, err := s.fetchByID(ctx, id)
	s.remember(id, fresh, err)
	records, err := s.records(ctx, link)
	if err != nil {
		return domain.TestinyRunView{}, err
	}
	return s.view(link, s.caseScripts(ctx, cfg), records), nil
}

func (s *Service) linkOf(ctx context.Context, task domain.SessionID, id domain.TestinyRunID) (domain.TestinyRunLink, error) {
	links, err := s.links.ListTestinyRunLinks(ctx, task)
	if err != nil {
		return domain.TestinyRunLink{}, err
	}
	for _, l := range links {
		if l.RunID == id {
			return l, nil
		}
	}
	return domain.TestinyRunLink{}, fmt.Errorf("%w: %s is not linked to %s; link it first (ao testiny link %s %d)", ErrRunNotLinked, id, task, task, int64(id))
}

// mayWrite says whether agent `by` may record results on the task: it must be
// on the task, and when the task has a qa that is still running, it must be
// that qa. A solo worker records its own task's results.
func (s *Service) mayWrite(ctx context.Context, task, by domain.SessionID) error {
	rec, ok, err := s.sessions.GetSession(ctx, by)
	if err != nil {
		return err
	}
	if !ok || (rec.ID != task && rec.CrewID != task) {
		return fmt.Errorf("%w: %s is not on task %s", ErrWriteNotYours, by, task)
	}
	if rec.CrewRole == domain.CrewRoleQA {
		return nil
	}
	members, err := s.sessions.ListSessions(ctx, rec.ProjectID)
	if err != nil {
		return err
	}
	for _, m := range members {
		if m.CrewID == task && m.CrewRole == domain.CrewRoleQA && !m.IsTerminated {
			return fmt.Errorf("%w: task %s has a qa (%s), and only qa records its results; send qa what to check instead", ErrWriteNotYours, task, m.ID)
		}
	}
	return nil
}

// checkBatch refuses a batch that names a case the run does not have, or, for
// an agent, any case a person set. Every refused case is named.
func checkBatch(id domain.TestinyRunID, results []domain.TestinyResult, cases []testinyadapter.Case, latest []domain.TestinyResultEntry, agent bool) error {
	current := make(map[int64]domain.TestinyCaseStatus, len(cases))
	for _, c := range cases {
		current[c.ID] = domain.TestinyCaseStatus(c.Status)
	}
	for _, r := range results {
		if _, ok := current[r.CaseID]; !ok {
			return fmt.Errorf("%w: TC-%d is not in %s", domain.ErrBadTestinyResult, r.CaseID, id)
		}
	}
	if !agent {
		return nil
	}
	last := make(map[int64]domain.TestinyResultEntry, len(latest))
	for _, e := range latest {
		last[e.CaseID] = e
	}
	var refused []string
	for _, r := range results {
		e, logged := last[r.CaseID]
		if !agentMayOverwrite(current[r.CaseID], e, logged) {
			refused = append(refused, fmt.Sprintf("TC-%d (%s)", r.CaseID, current[r.CaseID]))
		}
	}
	if len(refused) > 0 {
		return fmt.Errorf("%w: %s in %s; report your result in the handback instead. A person can set the case back to NOTRUN to ask for a re-run",
			ErrSetByPerson, strings.Join(refused, ", "), id)
	}
	return nil
}

// agentMayOverwrite is the overwrite guard: a person's verdict outranks an
// agent's. An agent may write a case that is NOTRUN in Testiny (never played,
// or set back to NOTRUN by a person to ask for a re-run), or one whose status
// is still the one an agent last wrote through AO. Anything else was set by a
// person, in the app or in Testiny itself.
func agentMayOverwrite(current domain.TestinyCaseStatus, last domain.TestinyResultEntry, logged bool) bool {
	if current == domain.TestinyNotRun {
		return true
	}
	return logged && last.SetBy != "" && last.Status == current
}

// forget drops a run's cached read, so the next read asks Testiny.
func (s *Service) forget(id domain.TestinyRunID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.reads, id)
}

// records is the latest result AO recorded for each case of a linked run, by
// case id, with the crew role of the agent that wrote it.
func (s *Service) records(ctx context.Context, l domain.TestinyRunLink) (map[int64]*domain.TestinyResultRecord, error) {
	latest, err := s.links.LatestTestinyResults(ctx, l.SessionID, l.RunID)
	if err != nil || len(latest) == 0 {
		return nil, err
	}
	roles := map[string]domain.CrewRole{"": ""}
	out := make(map[int64]*domain.TestinyResultRecord, len(latest))
	for _, e := range latest {
		role, known := roles[e.SetBy]
		if !known {
			rec, ok, err := s.sessions.GetSession(ctx, domain.SessionID(e.SetBy))
			if err != nil {
				return nil, err
			}
			if ok {
				role = rec.CrewRole
			}
			roles[e.SetBy] = role
		}
		out[e.CaseID] = &domain.TestinyResultRecord{
			Status: e.Status, Comment: e.Comment, By: e.SetBy, ByRole: role, SHA: e.SHA, At: e.CreatedAt,
		}
	}
	return out, nil
}
