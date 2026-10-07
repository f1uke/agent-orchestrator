package testiny

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	testinyadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/testiny"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// RecordResults records case and step results in a run linked to the task,
// and returns the run read fresh from Testiny.
//
// by is the session id of the agent recording them, or "" for a person in the
// app. A person may set any case or step. An agent must be on the task (its
// qa, when the task has one), and may not change a case or a step a person
// set: see agentMayOverwrite. The whole batch is checked before anything is
// written. A result that gives only steps keeps the case's status.
//
// Testiny cannot tell an agent's write from a person's, so every result that
// reaches Testiny is logged as it was asked for, with by and sha (the commit
// the agent tested). A partial write logs what reached Testiny before
// returning the error.
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
	refused, err := checkBatch(id, results, current.Cases, latest, by != "")
	if err != nil {
		return domain.TestinyRunView{}, err
	}
	send, refusedSteps, err := s.withSteps(ctx, task, id, results, current.Cases, by != "")
	if err != nil {
		return domain.TestinyRunView{}, err
	}
	if refused = append(refused, refusedSteps...); len(refused) > 0 {
		return domain.TestinyRunView{}, fmt.Errorf("%w: %s in %s; report your result in the handback instead. A person can set the case or step back to NOTRUN to ask for a re-run",
			ErrSetByPerson, strings.Join(refused, ", "), id)
	}
	project, err := s.project(ctx, cfg.TestinyProject)
	if err != nil {
		return domain.TestinyRunView{}, err
	}

	written, writeErr := s.reader.SetResults(ctx, id, project.ID, send)
	s.forget(id)
	if len(written) > 0 {
		asked := make(map[int64]domain.TestinyResult, len(results))
		for _, r := range results {
			asked[r.CaseID] = r
		}
		at := s.now().UTC()
		entries := make([]domain.TestinyResultEntry, len(written))
		for i, r := range written {
			entries[i] = domain.TestinyResultEntry{SessionID: task, RunID: id, TestinyResult: asked[r.CaseID], SetBy: by, SHA: sha, CreatedAt: at}
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
	return s.viewOf(ctx, link, cfg)
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

// checkBatch refuses a batch that names a case the run does not have, and,
// for an agent, names every case status a person set that the batch would
// change. A result that gives no case status leaves the case's alone.
func checkBatch(id domain.TestinyRunID, results []domain.TestinyResult, cases []testinyadapter.Case, latest []domain.TestinyResultEntry, agent bool) ([]string, error) {
	current := caseStatuses(cases)
	for _, r := range results {
		if _, ok := current[r.CaseID]; !ok {
			return nil, fmt.Errorf("%w: TC-%d is not in %s", domain.ErrBadTestinyResult, r.CaseID, id)
		}
	}
	if !agent {
		return nil, nil
	}
	last := make(map[int64]lastWrite, len(latest))
	for _, e := range latest {
		last[e.CaseID] = lastWrite{by: e.SetBy, status: e.Status}
	}
	var refused []string
	for _, r := range results {
		if r.Status == "" {
			continue
		}
		if w, logged := last[r.CaseID]; !agentMayOverwrite(current[r.CaseID], w, logged) {
			refused = append(refused, fmt.Sprintf("TC-%d (%s)", r.CaseID, current[r.CaseID]))
		}
	}
	return refused, nil
}

func caseStatuses(cases []testinyadapter.Case) map[int64]domain.TestinyCaseStatus {
	current := make(map[int64]domain.TestinyCaseStatus, len(cases))
	for _, c := range cases {
		current[c.ID] = domain.TestinyCaseStatus(c.Status)
	}
	return current
}

// lastWrite is the latest status AO wrote for a case or a step, and who asked
// for it ("" for a person).
type lastWrite struct {
	by     string
	status domain.TestinyCaseStatus
}

// agentMayOverwrite is the overwrite guard, for a case or a step: a person's
// verdict outranks an agent's. An agent may write one that is NOTRUN in
// Testiny (never played, or set back to NOTRUN by a person to ask for a
// re-run), or one whose status is still the one an agent last wrote through
// AO. Anything else was set by a person, in the app or in Testiny itself.
func agentMayOverwrite(current domain.TestinyCaseStatus, last lastWrite, logged bool) bool {
	if current == domain.TestinyNotRun {
		return true
	}
	return logged && last.by != "" && last.status == current
}

// withSteps is the batch as Testiny is to be sent it. Testiny replaces a
// case's step results with the ones a write gives, so a result that gives
// steps carries every step result the case keeps, and the case's status now
// when it gives none. Each case's steps are read fresh, so a step number means
// the step a person sees. For an agent, it names every step a person set that
// the batch would change.
func (s *Service) withSteps(ctx context.Context, task domain.SessionID, id domain.TestinyRunID, results []domain.TestinyResult, cases []testinyadapter.Case, agent bool) ([]domain.TestinyResult, []string, error) {
	if !slices.ContainsFunc(results, func(r domain.TestinyResult) bool { return len(r.Steps) > 0 }) {
		return results, nil, nil
	}
	run, err := s.reader.Run(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	var log []domain.TestinyResultEntry
	if agent {
		if log, err = s.links.TestinyStepResultLog(ctx, task, id); err != nil {
			return nil, nil, err
		}
	}
	current := caseStatuses(cases)
	send := slices.Clone(results)
	var refused []string
	for i, r := range send {
		if len(r.Steps) == 0 {
			continue
		}
		detail, err := s.reader.Case(ctx, r.CaseID)
		if err != nil {
			return nil, nil, err
		}
		s.mu.Lock()
		s.cases[r.CaseID] = caseRead{at: s.now(), detail: detail}
		s.mu.Unlock()
		if err := checkSteps(r, detail); err != nil {
			return nil, nil, err
		}
		held := heldSteps(run.Steps[r.CaseID], detail.Steps)
		if agent {
			last := lastStepWrites(log, r.CaseID)
			for _, step := range r.Steps {
				status, ok := held[step.N]
				if !ok {
					status = domain.TestinyNotRun
				}
				if w, logged := last[step.N]; !agentMayOverwrite(status, w, logged) {
					refused = append(refused, fmt.Sprintf("TC-%d step %d (%s)", r.CaseID, step.N, status))
				}
			}
		}
		for _, step := range r.Steps {
			held[step.N] = step.Status
		}
		send[i].Steps = make([]domain.TestinyStepResult, 0, len(held))
		for _, n := range slices.Sorted(maps.Keys(held)) {
			send[i].Steps = append(send[i].Steps, domain.TestinyStepResult{N: n, Status: held[n]})
		}
		if r.Status == "" {
			send[i].Status = current[r.CaseID]
		}
	}
	return send, refused, nil
}

// checkSteps refuses step results the case cannot take: only a STEPS case
// has steps, and Testiny names a step by its row id, which a step written as
// Markdown lacks until someone saves the case in Testiny's web app.
func checkSteps(r domain.TestinyResult, detail domain.TestinyCaseDetail) error {
	if detail.Template != domain.TestinyTemplateSteps {
		return fmt.Errorf("%w: TC-%d is a %s case, and only a STEPS case has steps to record", domain.ErrBadTestinyResult, r.CaseID, detail.Template)
	}
	n := len(detail.Steps)
	for _, step := range r.Steps {
		if step.N > n {
			noun := "steps"
			if n == 1 {
				noun = "step"
			}
			return fmt.Errorf("%w: TC-%d has %d %s, so step %d does not exist", domain.ErrBadTestinyResult, r.CaseID, n, noun, step.N)
		}
	}
	for _, step := range detail.Steps {
		if step.RID == "" {
			return fmt.Errorf("%w: TC-%d step %d has no row id in Testiny yet, so no step of the case can take a result: open the case in Testiny's web app and save it once", domain.ErrBadTestinyResult, r.CaseID, step.N)
		}
	}
	return nil
}

// heldSteps is the result Testiny holds for each step of the case now, by
// step number. A held result whose step is gone is left out.
func heldSteps(held []domain.TestinyRunStep, steps []domain.TestinyCaseStep) map[int]domain.TestinyCaseStatus {
	out := make(map[int]domain.TestinyCaseStatus, len(held))
	for _, step := range steps {
		if i := slices.IndexFunc(held, func(r domain.TestinyRunStep) bool { return domain.SameTestinyStep(r, step) }); i >= 0 {
			out[step.N] = held[i].Status
		}
	}
	return out
}

// lastStepWrites is the latest status AO wrote for each step of a case, by
// step number, from the step log (oldest first).
func lastStepWrites(log []domain.TestinyResultEntry, caseID int64) map[int]lastWrite {
	out := map[int]lastWrite{}
	for _, e := range log {
		if e.CaseID != caseID {
			continue
		}
		for _, step := range e.Steps {
			out[step.N] = lastWrite{by: e.SetBy, status: step.Status}
		}
	}
	return out
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
