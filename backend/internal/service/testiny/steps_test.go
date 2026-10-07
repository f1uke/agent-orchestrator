package testiny

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// replaceSteps is Testiny's own rule: a write's step results replace every
// step result the case had. Each step takes the row id of the case's step.
func (f *fakeTestiny) replaceSteps(run domain.TestinyRunID, caseID int64, steps []domain.TestinyStepResult) {
	r := f.runs[run]
	held := make(map[int64][]domain.TestinyRunStep, len(r.Steps))
	for id, s := range r.Steps {
		held[id] = s
	}
	rows := f.details[caseID].Steps
	out := make([]domain.TestinyRunStep, len(steps))
	for i, s := range steps {
		out[i] = domain.TestinyRunStep{N: s.N, RID: rows[s.N-1].RID, Status: s.Status}
	}
	held[caseID] = out
	r.Steps = held
	f.runs[run] = r
}

func (s *fakeStore) TestinyStepResultLog(_ context.Context, sid domain.SessionID, run domain.TestinyRunID) ([]domain.TestinyResultEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.TestinyResultEntry
	for _, e := range s.log {
		if e.SessionID == sid && e.RunID == run && len(e.Steps) > 0 {
			out = append(out, e)
		}
	}
	return out, nil
}

// newStepsRig is the results rig with TC-7201 a STEPS case of three saved
// steps (row ids a, b, c).
func newStepsRig(t *testing.T) *rig {
	t.Helper()
	r := newResultsRig(t)
	r.tny.details[7201] = domain.TestinyCaseDetail{ID: 7201, Template: domain.TestinyTemplateSteps, Steps: []domain.TestinyCaseStep{
		{N: 1, RID: "a", Action: "Open"}, {N: 2, RID: "b", Action: "Tap"}, {N: 3, RID: "c", Action: "Check"},
	}}
	return r
}

// holdSteps is what Testiny holds for TC-7201's steps, set outside AO.
func holdSteps(r *rig, steps ...domain.TestinyRunStep) {
	run := r.tny.runs[640]
	run.Steps = map[int64][]domain.TestinyRunStep{7201: steps}
	r.tny.runs[640] = run
}

func steps(pairs ...any) []domain.TestinyStepResult {
	var out []domain.TestinyStepResult
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, domain.TestinyStepResult{N: pairs[i].(int), Status: domain.TestinyCaseStatus(pairs[i+1].(string))})
	}
	return out
}

func TestTheRunViewCarriesEachCasesStepResults(t *testing.T) {
	r := newStepsRig(t)
	held := []domain.TestinyRunStep{{N: 1, RID: "a", Status: "PASSED"}, {N: 2, RID: "b", Status: "FAILED"}}
	holdSteps(r, held...)
	runs, err := r.svc.Runs(context.Background(), "app-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := caseByID(t, runs.Runs[0], 7201).Steps; !reflect.DeepEqual(got, held) {
		t.Fatalf("TC-7201 steps = %+v, want %+v", got, held)
	}
	if got := caseByID(t, runs.Runs[0], 7202).Steps; got == nil || len(got) != 0 {
		t.Fatalf("TC-7202 steps = %#v, want an empty list", got)
	}
}

func TestAStepOnlyWriteKeepsTheCaseStatusAndEveryOtherStep(t *testing.T) {
	r := newStepsRig(t)
	r.tny.setStatus(640, 7201, domain.TestinyFailed)
	holdSteps(r, domain.TestinyRunStep{N: 1, RID: "a", Status: "PASSED"}, domain.TestinyRunStep{N: 3, RID: "c", Status: "BLOCKED"})

	view, err := r.svc.RecordResults(context.Background(), "app-1", 640, []domain.TestinyResult{
		{CaseID: 7201, Steps: steps(2, "failed")},
	}, "", "")
	if err != nil {
		t.Fatalf("RecordResults: %v", err)
	}

	sent := []domain.TestinyResult{{CaseID: 7201, Status: domain.TestinyFailed, Steps: steps(1, "PASSED", 2, "FAILED", 3, "BLOCKED")}}
	if len(r.tny.sets) != 1 || !reflect.DeepEqual(r.tny.sets[0].results, sent) {
		t.Fatalf("Testiny was sent %+v, want %+v: the case's status and its other steps kept", r.tny.sets, sent)
	}
	wantLog := []domain.TestinyResultEntry{{SessionID: "app-1", RunID: 640, CreatedAt: r.clock.Now(),
		TestinyResult: domain.TestinyResult{CaseID: 7201, Steps: steps(2, "FAILED")}}}
	if !reflect.DeepEqual(r.store.log, wantLog) {
		t.Fatalf("log =\n%+v\nwant\n%+v (what was asked, with no case status)", r.store.log, wantLog)
	}
	c := caseByID(t, view, 7201)
	wantSteps := []domain.TestinyRunStep{{N: 1, RID: "a", Status: "PASSED"}, {N: 2, RID: "b", Status: "FAILED"}, {N: 3, RID: "c", Status: "BLOCKED"}}
	if c.Status != domain.TestinyFailed || c.Recorded != nil || !reflect.DeepEqual(c.Steps, wantSteps) {
		t.Fatalf("TC-7201 = %+v, want FAILED with no AO record and steps %+v", c, wantSteps)
	}
}

func TestACaseStatusWithStepsSendsBoth(t *testing.T) {
	r := newStepsRig(t)
	if _, err := r.svc.RecordResults(context.Background(), "app-1", 640, []domain.TestinyResult{
		{CaseID: 7201, Status: "PASSED", Steps: steps(1, "PASSED", 2, "PASSED", 3, "PASSED")},
	}, "app-1", "4f2c9e1"); err != nil {
		t.Fatalf("RecordResults: %v", err)
	}
	sent := []domain.TestinyResult{{CaseID: 7201, Status: domain.TestinyPassed, Steps: steps(1, "PASSED", 2, "PASSED", 3, "PASSED")}}
	if !reflect.DeepEqual(r.tny.sets[0].results, sent) {
		t.Fatalf("sent %+v, want %+v", r.tny.sets[0].results, sent)
	}
	if e := r.store.log[0]; e.Status != domain.TestinyPassed || e.SetBy != "app-1" || len(e.Steps) != 3 {
		t.Fatalf("log = %+v", e)
	}
}

// A step result Testiny holds belongs to the step whose row id it names, so a
// step moved since keeps its result, and one whose step is gone is dropped. A
// result with no row id stays at its number.
func TestHeldStepResultsFollowTheirRowIDs(t *testing.T) {
	r := newStepsRig(t)
	holdSteps(r,
		domain.TestinyRunStep{N: 1, RID: "b", Status: "PASSED"},
		domain.TestinyRunStep{N: 2, RID: "gone", Status: "FAILED"},
		domain.TestinyRunStep{N: 3, Status: "BLOCKED"},
	)
	if _, err := r.svc.RecordResults(context.Background(), "app-1", 640, []domain.TestinyResult{
		{CaseID: 7201, Steps: steps(1, "PASSED")},
	}, "", ""); err != nil {
		t.Fatalf("RecordResults: %v", err)
	}
	want := steps(1, "PASSED", 2, "PASSED", 3, "BLOCKED")
	if got := r.tny.sets[0].results[0].Steps; !reflect.DeepEqual(got, want) {
		t.Fatalf("sent steps %+v, want %+v", got, want)
	}
}

func TestStepsAreCheckedAgainstTheCase(t *testing.T) {
	for name, tc := range map[string]struct {
		detail domain.TestinyCaseDetail
		steps  []domain.TestinyStepResult
		says   string
	}{
		"a TEXT case": {domain.TestinyCaseDetail{Template: domain.TestinyTemplateText}, steps(1, "PASSED"), "TC-7201 is a TEXT case"},
		"no such step": {domain.TestinyCaseDetail{Template: domain.TestinyTemplateSteps, Steps: []domain.TestinyCaseStep{{N: 1, RID: "a"}}},
			steps(2, "PASSED"), "TC-7201 has 1 step, so step 2 does not exist"},
		"a step never saved": {domain.TestinyCaseDetail{Template: domain.TestinyTemplateSteps, Steps: []domain.TestinyCaseStep{{N: 1, RID: "a"}, {N: 2}}},
			steps(1, "PASSED"), "TC-7201 step 2 has no row id"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newResultsRig(t)
			r.tny.details[7201] = tc.detail
			_, err := r.svc.RecordResults(context.Background(), "app-1", 640, []domain.TestinyResult{{CaseID: 7201, Steps: tc.steps}}, "", "")
			if !errors.Is(err, domain.ErrBadTestinyResult) || !strings.Contains(err.Error(), tc.says) {
				t.Fatalf("err = %v, want ErrBadTestinyResult saying %q", err, tc.says)
			}
			if len(r.tny.sets) != 0 || len(r.store.log) != 0 {
				t.Fatalf("wrote %+v / logged %+v", r.tny.sets, r.store.log)
			}
		})
	}
}

// The overwrite guard, step by step: an agent never overwrites a step a
// person set, and a step back at NOTRUN invites a re-run.
func TestAnAgentNeverOverwritesAPersonsStep(t *testing.T) {
	agent := func(status string) *domain.TestinyResultEntry {
		return &domain.TestinyResultEntry{TestinyResult: domain.TestinyResult{CaseID: 7201, Steps: steps(2, status)}, SetBy: "app-1"}
	}
	person := func(status string) *domain.TestinyResultEntry {
		return &domain.TestinyResultEntry{TestinyResult: domain.TestinyResult{CaseID: 7201, Steps: steps(2, status)}}
	}
	for name, tc := range map[string]struct {
		last    *domain.TestinyResultEntry
		held    string
		by      string
		refused bool
	}{
		"never played":                       {nil, "", "app-1", false},
		"held at NOTRUN":                     {nil, "NOTRUN", "app-1", false},
		"set in Testiny, never through AO":   {nil, "PASSED", "app-1", true},
		"the agent's own earlier result":     {agent("FAILED"), "FAILED", "app-1", false},
		"changed in Testiny after the agent": {agent("FAILED"), "PASSED", "app-1", true},
		"set by a person in the app":         {person("PASSED"), "PASSED", "app-1", true},
		"reset by a person in the app":       {person("NOTRUN"), "NOTRUN", "app-1", false},
		"a person overwrites a person":       {person("PASSED"), "PASSED", "", false},
	} {
		t.Run(name, func(t *testing.T) {
			r := newStepsRig(t)
			if tc.last != nil {
				e := *tc.last
				e.SessionID, e.RunID = "app-1", 640
				r.store.log = append(r.store.log, e)
			}
			if tc.held != "" {
				holdSteps(r, domain.TestinyRunStep{N: 2, RID: "b", Status: domain.TestinyCaseStatus(tc.held)})
			}
			_, err := r.svc.RecordResults(context.Background(), "app-1", 640, []domain.TestinyResult{
				{CaseID: 7201, Steps: steps(2, "FAILED")},
			}, tc.by, "")
			if tc.refused != errors.Is(err, ErrSetByPerson) {
				t.Fatalf("err = %v, want refused = %v", err, tc.refused)
			}
			if tc.refused && !strings.Contains(err.Error(), "TC-7201 step 2 ("+tc.held+")") {
				t.Fatalf("err = %v, want it to name TC-7201 step 2 (%s)", err, tc.held)
			}
			if !tc.refused && err != nil {
				t.Fatalf("err = %v", err)
			}
			if wrote := len(r.tny.sets) > 0; wrote == tc.refused {
				t.Fatalf("wrote = %v, refused = %v", wrote, tc.refused)
			}
		})
	}
}

// A step-only write leaves the case's status alone, so it needs no say over
// the case, and it does not make a person's case status the agent's.
func TestAStepOnlyWriteDoesNotTakeOverThePersonsCase(t *testing.T) {
	r := newStepsRig(t)
	if _, err := r.svc.RecordResults(context.Background(), "app-1", 640, []domain.TestinyResult{
		{CaseID: 7201, Status: "PASSED"},
	}, "", ""); err != nil {
		t.Fatalf("person: %v", err)
	}
	if _, err := r.svc.RecordResults(context.Background(), "app-1", 640, []domain.TestinyResult{
		{CaseID: 7201, Steps: steps(1, "PASSED")},
	}, "app-1", ""); err != nil {
		t.Fatalf("agent steps on a person's case: %v", err)
	}
	_, err := r.svc.RecordResults(context.Background(), "app-1", 640, []domain.TestinyResult{
		{CaseID: 7201, Status: "FAILED", Comment: "x"},
	}, "app-1", "")
	if !errors.Is(err, ErrSetByPerson) {
		t.Fatalf("err = %v, want the person's case status still guarded", err)
	}
}

func TestCaseAndStepRefusalsAreNamedTogether(t *testing.T) {
	r := newStepsRig(t)
	r.tny.setStatus(640, 7201, domain.TestinyPassed)
	r.tny.setStatus(640, 7202, domain.TestinyBlocked)
	holdSteps(r, domain.TestinyRunStep{N: 3, RID: "c", Status: "PASSED"})
	_, err := r.svc.RecordResults(context.Background(), "app-1", 640, []domain.TestinyResult{
		{CaseID: 7201, Steps: steps(1, "PASSED", 3, "FAILED")},
		{CaseID: 7202, Status: "PASSED"},
	}, "app-1", "")
	if !errors.Is(err, ErrSetByPerson) || !strings.Contains(err.Error(), "TC-7202 (BLOCKED)") || !strings.Contains(err.Error(), "TC-7201 step 3 (PASSED)") {
		t.Fatalf("err = %v, want both refusals named", err)
	}
	if strings.Contains(err.Error(), "step 1") {
		t.Fatalf("err names step 1, which was free to write: %v", err)
	}
}
