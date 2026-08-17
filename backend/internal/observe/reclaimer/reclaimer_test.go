package reclaimer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/reclaimlog"
	"github.com/aoagents/agent-orchestrator/backend/internal/reclaimsettings"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
)

type fakeSvc struct {
	candidates []sessionsvc.ReclaimCandidate
	reclaimed  []domain.SessionID
	// outcome, when set, is returned for every Reclaim call.
	outcome *sessionsvc.ReclaimOutcome
	err     error
}

func (f *fakeSvc) ListReclaimable(context.Context) ([]sessionsvc.ReclaimCandidate, error) {
	return f.candidates, nil
}

func (f *fakeSvc) Reclaim(_ context.Context, id domain.SessionID) (sessionsvc.ReclaimOutcome, error) {
	if f.err != nil {
		return sessionsvc.ReclaimOutcome{}, f.err
	}
	f.reclaimed = append(f.reclaimed, id)
	if f.outcome != nil {
		return *f.outcome, nil
	}
	return sessionsvc.ReclaimOutcome{Freed: true, BytesFreed: 1234}, nil
}

type fakeSettings struct{ s reclaimsettings.Settings }

func (f fakeSettings) Get() reclaimsettings.Settings { return f.s }

// memLog captures audit entries in order.
type memLog struct{ entries []reclaimlog.Entry }

func (m *memLog) Append(e reclaimlog.Entry) error {
	m.entries = append(m.entries, e)
	return nil
}

func (m *memLog) actions() []string {
	out := make([]string, 0, len(m.entries))
	for _, e := range m.entries {
		out = append(out, e.Action)
	}
	return out
}

func on(grace int) fakeSettings {
	return fakeSettings{reclaimsettings.Settings{Enabled: true, GraceMinutes: grace}}
}

// runPast ticks once to stamp the in-memory debounce, advances the clock past
// the grace, then ticks again — the two-pass sequence the loop requires before
// it will act on any candidate.
func runPast(t *testing.T, r *Reclaimer, now *time.Time, grace time.Duration) {
	t.Helper()
	_ = r.Tick(context.Background())
	*now = now.Add(grace + time.Minute)
	_ = r.Tick(context.Background())
}

// candidate builds a candidate that became terminal long ago, so the DURABLE
// age gate is satisfied and a test exercises only the behaviour it is about.
func candidate(id string, now time.Time) sessionsvc.ReclaimCandidate {
	return sessionsvc.ReclaimCandidate{
		ID:            domain.SessionID(id),
		ProjectID:     "demo",
		Branch:        "feature/" + id,
		WorkspacePath: filepath.Join(string(filepath.Separator), "wt", id),
		Status:        "terminated",
		Since:         now.Add(-30 * 24 * time.Hour),
	}
}

func TestTick_ReclaimsOnlyAfterGrace(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	svc := &fakeSvc{candidates: []sessionsvc.ReclaimCandidate{candidate("sess-1", now)}}
	r := New(svc, on(15), Config{Clock: func() time.Time { return now }, SelfPath: "/elsewhere"})

	// First tick: stamps first-seen, does NOT reclaim.
	_ = r.Tick(context.Background())
	if len(svc.reclaimed) != 0 {
		t.Fatalf("reclaimed too early: %v", svc.reclaimed)
	}

	// Advance past grace, tick again: reclaims.
	now = now.Add(16 * time.Minute)
	_ = r.Tick(context.Background())
	if len(svc.reclaimed) != 1 || svc.reclaimed[0] != "sess-1" {
		t.Fatalf("want reclaim sess-1, got %v", svc.reclaimed)
	}
}

// TestTick_DurableAgeGateHoldsAcrossRestarts: the age threshold runs on the
// record's own timestamp, not only on an in-memory stamp. A session that became
// terminal one minute ago must not be reclaimed by a freshly-started daemon
// however many ticks it runs.
func TestTick_DurableAgeGateHoldsAcrossRestarts(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	fresh := candidate("sess-fresh", now)
	fresh.Since = now.Add(-time.Minute) // only just finished
	svc := &fakeSvc{candidates: []sessionsvc.ReclaimCandidate{fresh}}
	r := New(svc, on(60), Config{Clock: func() time.Time { return now }, SelfPath: "/elsewhere"})

	for i := 0; i < 5; i++ {
		now = now.Add(20 * time.Minute)
		_ = r.Tick(context.Background())
	}
	if len(svc.reclaimed) != 0 {
		t.Fatalf("a session terminal for 1 minute must not be reclaimed, got %v", svc.reclaimed)
	}
}

func TestTick_DisabledSetting_NoReclaim(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	svc := &fakeSvc{candidates: []sessionsvc.ReclaimCandidate{candidate("sess-1", now)}}
	r := New(svc, fakeSettings{reclaimsettings.Settings{Enabled: false}},
		Config{Clock: func() time.Time { return now }, SelfPath: "/elsewhere"})
	_ = r.Tick(context.Background())
	now = now.Add(time.Hour)
	_ = r.Tick(context.Background())
	if len(svc.reclaimed) != 0 {
		t.Fatalf("reclaimed while disabled: %v", svc.reclaimed)
	}
}

// TestTick_NeverReclaimsItsOwnWorktree: the sweep runs INSIDE AO, so its own
// workspace can match its own criteria. A candidate that contains the running
// process's directory must be refused however old it is.
func TestTick_NeverReclaimsItsOwnWorktree(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	self := t.TempDir()
	c := candidate("sess-self", now)
	c.WorkspacePath = self

	log := &memLog{}
	svc := &fakeSvc{candidates: []sessionsvc.ReclaimCandidate{c}}
	r := New(svc, on(15), Config{Clock: func() time.Time { return now }, SelfPath: self, Audit: log})

	now = now.Add(time.Hour)
	_ = r.Tick(context.Background())
	_ = r.Tick(context.Background())

	if len(svc.reclaimed) != 0 {
		t.Fatalf("the sweep deleted the ground it stands on: %v", svc.reclaimed)
	}
	if len(log.entries) == 0 || log.entries[0].Reason != reasonSelf {
		t.Fatalf("the refusal must be logged with the self reason, got %+v", log.entries)
	}
}

// TestTick_SelfGuardCoversASubdirectory: a sweep launched from a nested
// directory inside a qualifying worktree is still standing on it.
func TestTick_SelfGuardCoversASubdirectory(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	wt := t.TempDir()
	nested := filepath.Join(wt, "backend", "internal")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	c := candidate("sess-self", now)
	c.WorkspacePath = wt

	svc := &fakeSvc{candidates: []sessionsvc.ReclaimCandidate{c}}
	r := New(svc, on(15), Config{Clock: func() time.Time { return now }, SelfPath: nested})

	now = now.Add(time.Hour)
	_ = r.Tick(context.Background())
	_ = r.Tick(context.Background())

	if len(svc.reclaimed) != 0 {
		t.Fatalf("a sweep running inside the worktree must not reclaim it: %v", svc.reclaimed)
	}
}

// TestTick_SelfGuardDoesNotOverreach: a sibling worktree under the same parent
// is NOT the one we are standing in and must still be reclaimable, or the guard
// would disable the whole feature.
func TestTick_SelfGuardDoesNotOverreach(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	root := t.TempDir()
	self := filepath.Join(root, "mine")
	sibling := filepath.Join(root, "theirs")
	for _, d := range []string{self, sibling} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	c := candidate("sess-sibling", now)
	c.WorkspacePath = sibling

	svc := &fakeSvc{candidates: []sessionsvc.ReclaimCandidate{c}}
	r := New(svc, on(15), Config{Clock: func() time.Time { return now }, SelfPath: self})

	runPast(t, r, &now, 15*time.Minute)

	if len(svc.reclaimed) != 1 {
		t.Fatalf("a sibling worktree must still be reclaimable, got %v", svc.reclaimed)
	}
}

// TestTick_PreservedWorkspaceIsNotRecordedAsReclaimed is the regression test
// for the original bug: a refusal used to be logged as a success and then
// suppressed forever. It must be logged as a skip AND retried later.
func TestTick_PreservedWorkspaceIsNotRecordedAsReclaimed(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	log := &memLog{}
	svc := &fakeSvc{
		candidates: []sessionsvc.ReclaimCandidate{candidate("sess-dirty", now)},
		outcome:    &sessionsvc.ReclaimOutcome{Freed: false, Reason: "workspace_dirty"},
	}
	r := New(svc, on(15), Config{Clock: func() time.Time { return now }, SelfPath: "/elsewhere", Audit: log})

	runPast(t, r, &now, 15*time.Minute)

	if got := log.actions(); len(got) != 1 || got[0] != reclaimlog.ActionSkipped {
		t.Fatalf("a refusal must be logged as a skip, got %v", got)
	}
	if log.entries[0].BytesFreed != 0 {
		t.Fatal("a refusal must not claim to have freed bytes")
	}

	// It must be retried on a later pass, not suppressed forever.
	now = now.Add(time.Hour)
	_ = r.Tick(context.Background())
	now = now.Add(time.Hour)
	_ = r.Tick(context.Background())
	if len(svc.reclaimed) < 2 {
		t.Fatalf("a preserved workspace must be retried, attempts=%d", len(svc.reclaimed))
	}
}

// TestTick_RepeatedRefusalLogsOncePerReason keeps the durable log readable: a
// permanently dirty worktree must not append a line every minute forever.
func TestTick_RepeatedRefusalLogsOncePerReason(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	log := &memLog{}
	svc := &fakeSvc{
		candidates: []sessionsvc.ReclaimCandidate{candidate("sess-dirty", now)},
		outcome:    &sessionsvc.ReclaimOutcome{Freed: false, Reason: "workspace_dirty"},
	}
	r := New(svc, on(15), Config{Clock: func() time.Time { return now }, SelfPath: "/elsewhere", Audit: log})

	for i := 0; i < 6; i++ {
		now = now.Add(time.Hour)
		_ = r.Tick(context.Background())
	}
	if len(log.entries) != 1 {
		t.Fatalf("want one line per distinct reason, got %d: %+v", len(log.entries), log.entries)
	}
}

// TestTick_LogRecordsEveryReclaim covers the audit deliverable: what went, why
// it qualified, how old it was, how much it freed, and the branch that is the
// recovery route.
func TestTick_LogRecordsEveryReclaim(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	log := &memLog{}
	c := candidate("sess-1", now)
	c.Status = "merged"
	svc := &fakeSvc{
		candidates: []sessionsvc.ReclaimCandidate{c},
		outcome:    &sessionsvc.ReclaimOutcome{Freed: true, BytesFreed: 8_589_934_592, WorkspacePath: c.WorkspacePath},
	}
	r := New(svc, on(15), Config{Clock: func() time.Time { return now }, SelfPath: "/elsewhere", Audit: log})

	runPast(t, r, &now, 15*time.Minute)

	if len(log.entries) != 1 {
		t.Fatalf("want exactly one entry, got %+v", log.entries)
	}
	e := log.entries[0]
	if e.Action != reclaimlog.ActionReclaimed {
		t.Errorf("action = %q", e.Action)
	}
	if e.SessionID != "sess-1" || e.ProjectID != "demo" {
		t.Errorf("identity missing: %+v", e)
	}
	if e.Branch != "feature/sess-1" {
		t.Errorf("branch missing — it is the recovery instruction: %+v", e)
	}
	if e.WorkspacePath == "" {
		t.Errorf("workspace path missing: %+v", e)
	}
	if e.Qualified != "merged" {
		t.Errorf("qualified = %q, want the status that made it eligible", e.Qualified)
	}
	if e.BytesFreed != 8_589_934_592 {
		t.Errorf("bytesFreed = %d", e.BytesFreed)
	}
	if e.AgeMinutes <= 0 {
		t.Errorf("ageMinutes = %d, want how long it had been finished", e.AgeMinutes)
	}
	if e.At.IsZero() {
		t.Error("timestamp missing")
	}
}

// TestTick_ReclaimedSessionIsNotReclaimedTwice: teardown deliberately leaves
// WorkspacePath set (Restore needs it), so the candidate keeps being listed.
func TestTick_ReclaimedSessionIsNotReclaimedTwice(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	svc := &fakeSvc{candidates: []sessionsvc.ReclaimCandidate{candidate("sess-1", now)}}
	r := New(svc, on(15), Config{Clock: func() time.Time { return now }, SelfPath: "/elsewhere"})

	for i := 0; i < 4; i++ {
		now = now.Add(time.Hour)
		_ = r.Tick(context.Background())
	}
	if len(svc.reclaimed) != 1 {
		t.Fatalf("want exactly one reclaim, got %v", svc.reclaimed)
	}
}

// TestTick_ErrorIsLoggedAndRetried: a teardown that errors must not be recorded
// as a reclaim.
func TestTick_ErrorIsLoggedAndRetried(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	log := &memLog{}
	svc := &fakeSvc{candidates: []sessionsvc.ReclaimCandidate{candidate("sess-1", now)}, err: errors.New("boom")}
	r := New(svc, on(15), Config{Clock: func() time.Time { return now }, SelfPath: "/elsewhere", Audit: log})

	runPast(t, r, &now, 15*time.Minute)

	if got := log.actions(); len(got) != 1 || got[0] != reclaimlog.ActionSkipped {
		t.Fatalf("an errored teardown must log a skip, not a reclaim: %v", got)
	}
}

// TestTick_LogWriteFailureDoesNotStopTheSweep: the audit log is best-effort at
// the point of writing; a full disk must not wedge reclamation.
type failingLog struct{}

func (failingLog) Append(reclaimlog.Entry) error { return errors.New("disk full") }

func TestTick_LogWriteFailureDoesNotStopTheSweep(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	svc := &fakeSvc{candidates: []sessionsvc.ReclaimCandidate{candidate("sess-1", now)}}
	r := New(svc, on(15), Config{Clock: func() time.Time { return now }, SelfPath: "/elsewhere", Audit: failingLog{}})

	_ = r.Tick(context.Background())
	now = now.Add(time.Hour)
	if err := r.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(svc.reclaimed) != 1 {
		t.Fatalf("want the reclaim to proceed, got %v", svc.reclaimed)
	}
}

// haltingSvc reclaims normally until it has done stopAfter sessions, then fails
// every subsequent call — standing in for the process dying mid-sweep.
type haltingSvc struct {
	fakeSvc
	stopAfter int
	halted    bool
}

func (h *haltingSvc) Reclaim(ctx context.Context, id domain.SessionID) (sessionsvc.ReclaimOutcome, error) {
	if len(h.reclaimed) >= h.stopAfter {
		h.halted = true
		return sessionsvc.ReclaimOutcome{}, errors.New("process died mid-sweep")
	}
	return h.fakeSvc.Reclaim(ctx, id)
}

// TestTick_InterruptedSweepResumesConsistently: a sweep that dies part-way
// through leaves no half-state. Teardown is per-session and each one either
// completed or did not, so a later pass finishes the survivors and never redoes
// a session already reclaimed.
func TestTick_InterruptedSweepResumesConsistently(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	cands := []sessionsvc.ReclaimCandidate{
		candidate("sess-1", now), candidate("sess-2", now), candidate("sess-3", now),
	}
	svc := &haltingSvc{fakeSvc: fakeSvc{candidates: cands}, stopAfter: 2}
	r := New(svc, on(15), Config{Clock: func() time.Time { return now }, SelfPath: "/elsewhere"})

	// The interrupted pass.
	runPast(t, r, &now, 15*time.Minute)
	if !svc.halted {
		t.Fatal("premise broken: the sweep was expected to fail part-way")
	}
	if len(svc.reclaimed) != 2 {
		t.Fatalf("want 2 completed before the halt, got %v", svc.reclaimed)
	}

	// The daemon comes back. Its in-memory state is gone, so a FRESH Reclaimer
	// over the same service is the honest simulation of a restart.
	svc.stopAfter = 99 // the transient failure is over
	r2 := New(svc, on(15), Config{Clock: func() time.Time { return now }, SelfPath: "/elsewhere"})
	runPast(t, r2, &now, 15*time.Minute)

	seen := map[domain.SessionID]int{}
	for _, id := range svc.reclaimed {
		seen[id]++
	}
	if len(seen) != 3 {
		t.Fatalf("the resumed sweep must finish the survivors, reached %d of 3: %v", len(seen), svc.reclaimed)
	}
	// A restarted daemon re-attempts sessions the previous one already
	// reclaimed, because teardown deliberately keeps WorkspacePath so Restore
	// can use it. That is harmless — and the real service short-circuits it to
	// ReasonAlreadyGone rather than logging a phantom reclaim, which
	// TestTick_AlreadyGoneIsSilentAndTerminal covers.
}

// TestTick_AlreadyGoneIsSilentAndTerminal: a candidate whose worktree is
// already off disk (reclaimed by an earlier daemon) must be dropped quietly.
// Writing a "reclaimed 0 bytes" line for it on every restart would fill the
// audit log with events that never happened.
func TestTick_AlreadyGoneIsSilentAndTerminal(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	log := &memLog{}
	svc := &fakeSvc{
		candidates: []sessionsvc.ReclaimCandidate{candidate("sess-old", now)},
		outcome:    &sessionsvc.ReclaimOutcome{Freed: false, Reason: sessionsvc.ReasonAlreadyGone},
	}
	r := New(svc, on(15), Config{Clock: func() time.Time { return now }, SelfPath: "/elsewhere", Audit: log})

	runPast(t, r, &now, 15*time.Minute)
	attemptsAfterFirst := len(svc.reclaimed)

	if len(log.entries) != 0 {
		t.Fatalf("nothing happened, so nothing may be logged: %+v", log.entries)
	}

	// And it stops being retried.
	for i := 0; i < 3; i++ {
		now = now.Add(time.Hour)
		_ = r.Tick(context.Background())
	}
	if len(svc.reclaimed) != attemptsAfterFirst {
		t.Fatalf("an already-gone workspace must not be retried, attempts=%d", len(svc.reclaimed))
	}
}

func TestPathContains(t *testing.T) {
	root := t.TempDir()
	inner := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(filepath.Dir(root), "definitely-not-here")

	if !pathContains(root, root) {
		t.Error("a path contains itself")
	}
	if !pathContains(root, inner) {
		t.Error("a path contains its descendant")
	}
	if pathContains(inner, root) {
		t.Error("a descendant must not contain its ancestor")
	}
	if pathContains(root, sibling) {
		t.Error("unrelated paths must not match")
	}
	if pathContains("", root) || pathContains(root, "") {
		t.Error("empty paths must never match")
	}
	// A prefix that is not a path boundary must not match: /wt/foo does not
	// contain /wt/foobar.
	if pathContains(filepath.Join(root, "foo"), filepath.Join(root, "foobar")) {
		t.Error("a string prefix is not a path prefix")
	}
}
