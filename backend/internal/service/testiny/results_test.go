package testiny

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	testinyadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/testiny"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// setCall is one SetResults call the fake received.
type setCall struct {
	run       domain.TestinyRunID
	projectID int64
	results   []domain.TestinyResult
}

// SetResults applies the results to the fake's run, so a later read sees
// them, unless failAfter says to fail once that many results are written.
func (f *fakeTestiny) SetResults(_ context.Context, run domain.TestinyRunID, projectID int64, results []domain.TestinyResult) ([]domain.TestinyResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sets = append(f.sets, setCall{run: run, projectID: projectID, results: results})
	res := f.results[run]
	written := []domain.TestinyResult{}
	for _, r := range results {
		if f.failAfter != nil && len(written) == *f.failAfter {
			return written, fmt.Errorf("%w: connection refused", testinyadapter.ErrUnavailable)
		}
		for i, c := range res.Cases {
			if c.ID == r.CaseID {
				res.Summary[c.Status]--
				if res.Summary[c.Status] == 0 {
					delete(res.Summary, c.Status)
				}
				res.Cases[i].Status = string(r.Status)
				res.Summary[string(r.Status)]++
			}
		}
		written = append(written, r)
	}
	return written, nil
}

// setStatus is a person changing a case in Testiny, outside AO.
func (f *fakeTestiny) setStatus(run domain.TestinyRunID, caseID int64, status domain.TestinyCaseStatus) {
	if _, err := f.SetResults(context.Background(), run, 0, []domain.TestinyResult{{CaseID: caseID, Status: status}}); err != nil {
		panic(err)
	}
	f.mu.Lock()
	f.sets = f.sets[:len(f.sets)-1]
	f.mu.Unlock()
}

func (s *fakeStore) AppendTestinyResults(_ context.Context, entries []domain.TestinyResultEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log = append(s.log, entries...)
	return nil
}

func (s *fakeStore) LatestTestinyResults(_ context.Context, sid domain.SessionID, run domain.TestinyRunID) ([]domain.TestinyResultEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	latest := map[int64]domain.TestinyResultEntry{}
	var order []int64
	for _, e := range s.log {
		if e.SessionID != sid || e.RunID != run {
			continue
		}
		if _, ok := latest[e.CaseID]; !ok {
			order = append(order, e.CaseID)
		}
		latest[e.CaseID] = e
	}
	out := make([]domain.TestinyResultEntry, len(order))
	for i, id := range order {
		out[i] = latest[id]
	}
	return out, nil
}

func (s *fakeStore) ListSessions(_ context.Context, project domain.ProjectID) ([]domain.SessionRecord, error) {
	var out []domain.SessionRecord
	for _, r := range s.sessions {
		if r.ProjectID == project {
			out = append(out, r)
		}
	}
	return out, nil
}

// newResultsRig is a rig with run 640 linked to app-1: three cases nobody has
// played yet.
func newResultsRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t, on)
	r.tny.runs[640] = testinyadapter.Run{ID: 640, Title: "[AO-TEST] results", ProjectID: 1}
	r.tny.results[640] = testinyadapter.Results{
		Cases: []testinyadapter.Case{
			{ID: 7201, Title: "Opens", Status: "NOTRUN"},
			{ID: 7202, Title: "Confirms", Status: "NOTRUN"},
			{ID: 7203, Title: "Cancels", Status: "NOTRUN"},
		},
		Summary: map[string]int{"NOTRUN": 3},
	}
	link(r, 640)
	return r
}

// crew makes app-1 a crew's dev and app-2 its qa.
func crew(r *rig) {
	r.store.sessions["app-1"] = domain.SessionRecord{ID: "app-1", ProjectID: "app", CrewID: "app-1", CrewRole: domain.CrewRoleDev}
	r.store.sessions["app-2"] = domain.SessionRecord{ID: "app-2", ProjectID: "app", CrewID: "app-1", CrewRole: domain.CrewRoleQA}
}

func caseByID(t *testing.T, v domain.TestinyRunView, id int64) domain.TestinyCaseResult {
	t.Helper()
	for _, c := range v.Cases {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("TC-%d is not in the view: %+v", id, v.Cases)
	return domain.TestinyCaseResult{}
}

func TestRecordResultsWritesLogsAndReturnsTheFreshView(t *testing.T) {
	r := newResultsRig(t)
	crew(r)
	ctx := context.Background()
	// A read now puts the run in the cache; the write must not be answered from it.
	if _, err := r.svc.Runs(ctx, "app-1", false); err != nil {
		t.Fatal(err)
	}

	view, err := r.svc.RecordResults(ctx, "app-1", 640, []domain.TestinyResult{
		{CaseID: 7201, Status: "passed"},
		{CaseID: 7202, Status: "FAILED", Comment: " ปุ่มยืนยันไม่แสดง "},
	}, "app-2", "4f2c9e1")
	if err != nil {
		t.Fatalf("RecordResults: %v", err)
	}

	want := []domain.TestinyResult{
		{CaseID: 7201, Status: domain.TestinyPassed},
		{CaseID: 7202, Status: domain.TestinyFailed, Comment: "ปุ่มยืนยันไม่แสดง"},
	}
	if len(r.tny.sets) != 1 || r.tny.sets[0].run != 640 || r.tny.sets[0].projectID != 1 || !reflect.DeepEqual(r.tny.sets[0].results, want) {
		t.Fatalf("Testiny was sent %+v, want %+v in run 640 of project 1", r.tny.sets, want)
	}
	at := r.clock.Now()
	wantLog := []domain.TestinyResultEntry{
		{SessionID: "app-1", RunID: 640, TestinyResult: want[0], SetBy: "app-2", SHA: "4f2c9e1", CreatedAt: at},
		{SessionID: "app-1", RunID: 640, TestinyResult: want[1], SetBy: "app-2", SHA: "4f2c9e1", CreatedAt: at},
	}
	if !reflect.DeepEqual(r.store.log, wantLog) {
		t.Fatalf("log =\n%+v\nwant\n%+v", r.store.log, wantLog)
	}

	if !reflect.DeepEqual(view.Counts, map[domain.TestinyCaseStatus]int{"PASSED": 1, "FAILED": 1, "NOTRUN": 1}) {
		t.Fatalf("counts = %v: the view was not read fresh", view.Counts)
	}
	failed := caseByID(t, view, 7202)
	wantRec := &domain.TestinyResultRecord{Status: domain.TestinyFailed, Comment: "ปุ่มยืนยันไม่แสดง", By: "app-2", ByRole: domain.CrewRoleQA, SHA: "4f2c9e1", At: at}
	if failed.Status != domain.TestinyFailed || !reflect.DeepEqual(failed.Recorded, wantRec) {
		t.Fatalf("TC-7202 = %+v (recorded %+v), want FAILED recorded %+v", failed, failed.Recorded, wantRec)
	}
	if c := caseByID(t, view, 7203); c.Recorded != nil {
		t.Fatalf("TC-7203 has a record AO never wrote: %+v", c.Recorded)
	}

	// The tab's own read shows the same provenance.
	runs, err := r.svc.Runs(ctx, "app-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := caseByID(t, runs.Runs[0], 7202).Recorded; !reflect.DeepEqual(got, wantRec) {
		t.Fatalf("Runs: TC-7202 recorded = %+v, want %+v", got, wantRec)
	}
}

func TestRecordResultsRefusesARunNotLinkedToTheTask(t *testing.T) {
	r := newResultsRig(t)
	_, err := r.svc.RecordResults(context.Background(), "app-1", 632, []domain.TestinyResult{{CaseID: 7166, Status: "PASSED"}}, "", "")
	if !errors.Is(err, ErrRunNotLinked) || !strings.Contains(err.Error(), "TR-632") {
		t.Fatalf("err = %v, want ErrRunNotLinked naming TR-632", err)
	}
	if len(r.tny.sets) != 0 || len(r.store.log) != 0 {
		t.Fatalf("wrote %+v / logged %+v for an unlinked run", r.tny.sets, r.store.log)
	}
}

func TestRecordResultsValidatesTheWholeBatchBeforeWriting(t *testing.T) {
	for name, tc := range map[string]struct {
		results []domain.TestinyResult
		says    string
	}{
		"a case not in the run": {[]domain.TestinyResult{{CaseID: 7201, Status: "PASSED"}, {CaseID: 9999, Status: "PASSED"}}, "TC-9999 is not in TR-640"},
		"failed, no comment":    {[]domain.TestinyResult{{CaseID: 7201, Status: "PASSED"}, {CaseID: 7202, Status: "FAILED"}}, "TC-7202"},
		"unknown status":        {[]domain.TestinyResult{{CaseID: 7201, Status: "DONE"}}, "TC-7201"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newResultsRig(t)
			_, err := r.svc.RecordResults(context.Background(), "app-1", 640, tc.results, "", "")
			if !errors.Is(err, domain.ErrBadTestinyResult) || !strings.Contains(err.Error(), tc.says) {
				t.Fatalf("err = %v, want ErrBadTestinyResult saying %q", err, tc.says)
			}
			if len(r.tny.sets) != 0 || len(r.store.log) != 0 {
				t.Fatalf("wrote %+v / logged %+v for a batch that failed validation", r.tny.sets, r.store.log)
			}
		})
	}
}

func TestOnlyQARecordsWhenTheTaskHasOne(t *testing.T) {
	results := []domain.TestinyResult{{CaseID: 7201, Status: "PASSED"}}
	for name, tc := range map[string]struct {
		setup func(*rig)
		by    string
		want  error
	}{
		"solo worker on its own task": {func(*rig) {}, "app-1", nil},
		"a person":                    {crew, "", nil},
		"qa":                          {crew, "app-2", nil},
		"dev while the task has qa":   {crew, "app-1", ErrWriteNotYours},
		"dev after qa was terminated": {func(r *rig) {
			crew(r)
			qa := r.store.sessions["app-2"]
			qa.IsTerminated = true
			r.store.sessions["app-2"] = qa
		}, "app-1", nil},
		"an agent from another task": {func(r *rig) {
			r.store.sessions["app-9"] = domain.SessionRecord{ID: "app-9", ProjectID: "app"}
		}, "app-9", ErrWriteNotYours},
		"an unknown session": {func(*rig) {}, "app-404", ErrWriteNotYours},
	} {
		t.Run(name, func(t *testing.T) {
			r := newResultsRig(t)
			tc.setup(r)
			_, err := r.svc.RecordResults(context.Background(), "app-1", 640, results, tc.by, "")
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if wrote := len(r.tny.sets) > 0; wrote != (tc.want == nil) {
				t.Fatalf("wrote = %v with err %v", wrote, err)
			}
		})
	}
}

// The overwrite guard: an agent never overwrites a status a person set, and a
// case set back to NOTRUN invites a re-run.
func TestAnAgentNeverOverwritesAPersonsVerdict(t *testing.T) {
	agent := func(status domain.TestinyCaseStatus) *domain.TestinyResultEntry {
		return &domain.TestinyResultEntry{TestinyResult: domain.TestinyResult{CaseID: 7201, Status: status}, SetBy: "app-1"}
	}
	person := func(status domain.TestinyCaseStatus) *domain.TestinyResultEntry {
		return &domain.TestinyResultEntry{TestinyResult: domain.TestinyResult{CaseID: 7201, Status: status}}
	}
	for name, tc := range map[string]struct {
		last    *domain.TestinyResultEntry
		current domain.TestinyCaseStatus
		by      string
		refused bool
	}{
		"never played":                          {nil, domain.TestinyNotRun, "app-1", false},
		"set in Testiny, never through AO":      {nil, domain.TestinyPassed, "app-1", true},
		"the agent's own earlier result":        {agent(domain.TestinyFailed), domain.TestinyFailed, "app-1", false},
		"changed in Testiny after the agent":    {agent(domain.TestinyFailed), domain.TestinyPassed, "app-1", true},
		"reset in Testiny after the agent":      {agent(domain.TestinyFailed), domain.TestinyNotRun, "app-1", false},
		"set by a person in the app":            {person(domain.TestinyPassed), domain.TestinyPassed, "app-1", true},
		"reset by a person in the app":          {person(domain.TestinyNotRun), domain.TestinyNotRun, "app-1", false},
		"a person overwrites a person":          {person(domain.TestinyPassed), domain.TestinyPassed, "", false},
		"a person overwrites a Testiny change":  {agent(domain.TestinyFailed), domain.TestinyPassed, "", false},
		"a person overwrites an unlogged value": {nil, domain.TestinyBlocked, "", false},
	} {
		t.Run(name, func(t *testing.T) {
			r := newResultsRig(t)
			if tc.last != nil {
				e := *tc.last
				e.SessionID, e.RunID = "app-1", 640
				r.store.log = append(r.store.log, e)
			}
			r.tny.setStatus(640, 7201, tc.current)
			_, err := r.svc.RecordResults(context.Background(), "app-1", 640, []domain.TestinyResult{
				{CaseID: 7201, Status: "FAILED", Comment: "ปุ่มไม่แสดง"},
			}, tc.by, "")
			if tc.refused != errors.Is(err, ErrSetByPerson) {
				t.Fatalf("err = %v, want refused = %v", err, tc.refused)
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

func TestARefusedCaseStopsTheWholeBatchAndEveryRefusalIsNamed(t *testing.T) {
	r := newResultsRig(t)
	r.tny.setStatus(640, 7202, domain.TestinyPassed)
	r.tny.setStatus(640, 7203, domain.TestinyBlocked)
	_, err := r.svc.RecordResults(context.Background(), "app-1", 640, []domain.TestinyResult{
		{CaseID: 7201, Status: "PASSED"},
		{CaseID: 7202, Status: "FAILED", Comment: "x"},
		{CaseID: 7203, Status: "PASSED"},
	}, "app-1", "")
	if !errors.Is(err, ErrSetByPerson) || !strings.Contains(err.Error(), "TC-7202 (PASSED)") || !strings.Contains(err.Error(), "TC-7203 (BLOCKED)") {
		t.Fatalf("err = %v, want ErrSetByPerson naming TC-7202 and TC-7203", err)
	}
	if strings.Contains(err.Error(), "TC-7201") {
		t.Fatalf("err names TC-7201, which was free to write: %v", err)
	}
	if len(r.tny.sets) != 0 || len(r.store.log) != 0 {
		t.Fatalf("wrote %+v / logged %+v despite a refusal", r.tny.sets, r.store.log)
	}
}

func TestAPartialWriteLogsWhatReachedTestiny(t *testing.T) {
	r := newResultsRig(t)
	one := 1
	r.tny.failAfter = &one
	_, err := r.svc.RecordResults(context.Background(), "app-1", 640, []domain.TestinyResult{
		{CaseID: 7201, Status: "PASSED"},
		{CaseID: 7202, Status: "PASSED"},
	}, "app-1", "4f2c9e1")
	if !errors.Is(err, testinyadapter.ErrUnavailable) {
		t.Fatalf("err = %v, want the adapter's ErrUnavailable", err)
	}
	if len(r.store.log) != 1 || r.store.log[0].CaseID != 7201 || r.store.log[0].SetBy != "app-1" {
		t.Fatalf("log = %+v, want only TC-7201, which reached Testiny", r.store.log)
	}

	// The case that landed is the agent's own, so the agent can write it again.
	r.tny.failAfter = nil
	if _, err := r.svc.RecordResults(context.Background(), "app-1", 640, []domain.TestinyResult{
		{CaseID: 7201, Status: "PASSED"},
		{CaseID: 7202, Status: "PASSED"},
	}, "app-1", "4f2c9e1"); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

func TestRecordResultsAnswersOffAndUnknownSession(t *testing.T) {
	r := newRig(t, domain.ProjectConfig{})
	if _, err := r.svc.RecordResults(context.Background(), "app-1", 640, []domain.TestinyResult{{CaseID: 1, Status: "PASSED"}}, "", ""); !errors.Is(err, ErrOff) {
		t.Fatalf("err = %v, want ErrOff", err)
	}
	r = newResultsRig(t)
	if _, err := r.svc.RecordResults(context.Background(), "nope-1", 640, []domain.TestinyResult{{CaseID: 1, Status: "PASSED"}}, "", ""); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("err = %v, want ErrSessionNotFound", err)
	}
}
