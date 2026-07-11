package smoke

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// fakeStore is an in-memory Store for exercising service logic in isolation.
type fakeStore struct {
	checks    map[string]domain.SmokeCheck
	sessions  map[domain.SessionID]domain.SessionRecord
	evidence  map[string]domain.SmokeEvidence
	lastCases []domain.SmokeAuthoredCase
	reported  map[domain.SessionID]time.Time
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		checks:   map[string]domain.SmokeCheck{},
		sessions: map[domain.SessionID]domain.SessionRecord{},
		evidence: map[string]domain.SmokeEvidence{},
		reported: map[domain.SessionID]time.Time{},
	}
}

func (f *fakeStore) ListSmokeChecksBySession(_ context.Context, id domain.SessionID) ([]domain.SmokeCheck, error) {
	var out []domain.SmokeCheck
	for _, c := range f.checks {
		if c.SessionID == id {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeStore) GetSmokeCheck(_ context.Context, id string) (domain.SmokeCheck, bool, error) {
	c, ok := f.checks[id]
	return c, ok, nil
}

func (f *fakeStore) ReplaceSmokeChecks(_ context.Context, sessionID domain.SessionID, projectID domain.ProjectID, cases []domain.SmokeAuthoredCase, now time.Time) ([]domain.SmokeCheck, []string, error) {
	f.lastCases = cases
	out := make([]domain.SmokeCheck, 0, len(cases))
	for _, c := range cases {
		check := domain.SmokeCheck{ID: c.ID, SessionID: sessionID, ProjectID: projectID, Seq: c.Seq, Name: c.Name, Steps: c.Steps, Verdict: domain.SmokePending, Evidence: []domain.SmokeEvidence{}, CreatedAt: now, UpdatedAt: now}
		f.checks[c.ID] = check
		out = append(out, check)
	}
	return out, nil, nil
}

func (f *fakeStore) SetSmokeVerdict(_ context.Context, id string, verdict domain.SmokeVerdict, note string, decidedAt, now time.Time) (bool, error) {
	c, ok := f.checks[id]
	if !ok {
		return false, nil
	}
	c.Verdict, c.Note, c.DecidedAt, c.UpdatedAt = verdict, note, &decidedAt, now
	f.checks[id] = c
	return true, nil
}

func (f *fakeStore) ResetSmokeCheck(_ context.Context, id string, now time.Time) (bool, error) {
	c, ok := f.checks[id]
	if !ok {
		return false, nil
	}
	c.Verdict, c.Note, c.DecidedAt, c.Evidence, c.UpdatedAt = domain.SmokePending, "", nil, nil, now
	f.checks[id] = c
	return true, nil
}

func (f *fakeStore) MarkSmokeReported(_ context.Context, id domain.SessionID, reportedAt, _ time.Time) (int64, error) {
	f.reported[id] = reportedAt
	return 1, nil
}

func (f *fakeStore) InsertSmokeEvidence(_ context.Context, ev domain.SmokeEvidence) error {
	f.evidence[ev.ID] = ev
	return nil
}

func (f *fakeStore) GetSmokeEvidence(_ context.Context, id string) (domain.SmokeEvidence, bool, error) {
	ev, ok := f.evidence[id]
	return ev, ok, nil
}

func (f *fakeStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	rec, ok := f.sessions[id]
	return rec, ok, nil
}

func (f *fakeStore) ListSessions(_ context.Context, projectID domain.ProjectID) ([]domain.SessionRecord, error) {
	var out []domain.SessionRecord
	for _, rec := range f.sessions {
		if rec.ProjectID == projectID {
			out = append(out, rec)
		}
	}
	return out, nil
}

type fakeMessenger struct {
	sent map[domain.SessionID]string
	err  error
}

func (m *fakeMessenger) Send(_ context.Context, id domain.SessionID, message string) error {
	if m.err != nil {
		return m.err
	}
	if m.sent == nil {
		m.sent = map[domain.SessionID]string{}
	}
	m.sent[id] = message
	return nil
}

func newTestService(t *testing.T, store Store, msg Messenger) *Service {
	t.Helper()
	return New(store, t.TempDir(), msg, WithClock(func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }))
}

func TestAuthorResolvesIdsAndSeq(t *testing.T) {
	store := newFakeStore()
	store.sessions["w1"] = domain.SessionRecord{ID: "w1", ProjectID: "proj", Kind: domain.KindWorker}
	svc := newTestService(t, store, nil)

	_, err := svc.Author(context.Background(), "w1", []domain.SmokeAuthoredCase{
		{Name: "A fresh MR shows up"},
		{Name: "A fresh MR shows up"}, // duplicate name → deduped id
		{ID: "explicit-id", Name: "Third"},
	})
	if err != nil {
		t.Fatalf("author: %v", err)
	}
	if len(store.lastCases) != 3 {
		t.Fatalf("cases = %d, want 3", len(store.lastCases))
	}
	if store.lastCases[0].ID != "a-fresh-mr-shows-up" {
		t.Fatalf("case 0 id = %q, want slug", store.lastCases[0].ID)
	}
	if store.lastCases[1].ID != "a-fresh-mr-shows-up-2" {
		t.Fatalf("case 1 id = %q, want deduped slug", store.lastCases[1].ID)
	}
	if store.lastCases[2].ID != "explicit-id" {
		t.Fatalf("case 2 id = %q, want explicit", store.lastCases[2].ID)
	}
	for i, c := range store.lastCases {
		if c.Seq != i+1 {
			t.Fatalf("case %d seq = %d, want %d", i, c.Seq, i+1)
		}
	}
}

func TestAuthorRejectsEmptyNameAndEmptyList(t *testing.T) {
	store := newFakeStore()
	store.sessions["w1"] = domain.SessionRecord{ID: "w1", ProjectID: "proj"}
	svc := newTestService(t, store, nil)

	if _, err := svc.Author(context.Background(), "w1", nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty list err = %v, want ErrInvalid", err)
	}
	if _, err := svc.Author(context.Background(), "w1", []domain.SmokeAuthoredCase{{Name: "  "}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty name err = %v, want ErrInvalid", err)
	}
}

func TestAttachEvidenceValidatesTypeAndSize(t *testing.T) {
	store := newFakeStore()
	store.checks["c1"] = domain.SmokeCheck{ID: "c1", SessionID: "w1"}
	svc := newTestService(t, store, nil)
	ctx := context.Background()

	// Unsupported type → ErrInvalid.
	if _, err := svc.AttachEvidence(ctx, "w1", "c1", EvidenceUpload{Mime: "application/pdf", Reader: strings.NewReader("x")}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad type err = %v, want ErrInvalid", err)
	}

	// Oversize image → ErrInvalid, no row, no leftover file.
	big := strings.NewReader(strings.Repeat("x", int(maxImageBytes)+1024))
	if _, err := svc.AttachEvidence(ctx, "w1", "c1", EvidenceUpload{Mime: "image/png", Reader: big}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("oversize err = %v, want ErrInvalid", err)
	}
	if len(store.evidence) != 0 {
		t.Fatalf("oversize should not record a row, have %d", len(store.evidence))
	}

	// Valid small image → recorded, file present, kind derived.
	ev, err := svc.AttachEvidence(ctx, "w1", "c1", EvidenceUpload{Filename: "shot.png", Mime: "image/png; charset=binary", Reader: strings.NewReader("PNGDATA")})
	if err != nil {
		t.Fatalf("valid attach: %v", err)
	}
	if ev.Kind != "image" || ev.Mime != "image/png" || ev.SizeBytes != int64(len("PNGDATA")) {
		t.Fatalf("evidence metadata wrong: %+v", ev)
	}
	blob, err := svc.OpenEvidence(ctx, "w1", "c1", ev.ID)
	if err != nil {
		t.Fatalf("open evidence: %v", err)
	}
	if _, err := os.Stat(blob.Path); err != nil {
		t.Fatalf("blob not on disk: %v", err)
	}
}

func TestAttachEvidenceRejectsForeignCheck(t *testing.T) {
	store := newFakeStore()
	store.checks["c1"] = domain.SmokeCheck{ID: "c1", SessionID: "other"}
	svc := newTestService(t, store, nil)
	if _, err := svc.AttachEvidence(context.Background(), "w1", "c1", EvidenceUpload{Mime: "image/png", Reader: strings.NewReader("x")}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign check err = %v, want ErrNotFound", err)
	}
}

func TestReportPrefersLiveWorker(t *testing.T) {
	store := newFakeStore()
	store.sessions["w1"] = domain.SessionRecord{ID: "w1", ProjectID: "proj", Kind: domain.KindWorker, IsTerminated: false}
	store.sessions["orch"] = domain.SessionRecord{ID: "orch", ProjectID: "proj", Kind: domain.KindOrchestrator}
	store.checks["c1"] = domain.SmokeCheck{ID: "c1", SessionID: "w1", Seq: 1, Name: "A", Verdict: domain.SmokePass}
	msg := &fakeMessenger{}
	svc := newTestService(t, store, msg)

	out, err := svc.Report(context.Background(), "w1")
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if out.Target != "worker" || !out.Delivered {
		t.Fatalf("outcome = %+v, want delivered to worker", out)
	}
	if _, ok := msg.sent["w1"]; !ok {
		t.Fatal("expected a message to the worker")
	}
	if !strings.Contains(msg.sent["w1"], "[smoke results]") {
		t.Fatalf("worker message missing prefix: %q", msg.sent["w1"])
	}
	if _, ok := store.reported["w1"]; !ok {
		t.Fatal("expected reported_at stamped")
	}
}

func TestReportFallsBackToOrchestrator(t *testing.T) {
	store := newFakeStore()
	store.sessions["w1"] = domain.SessionRecord{ID: "w1", ProjectID: "proj", Kind: domain.KindWorker, IsTerminated: true}
	store.sessions["orch"] = domain.SessionRecord{ID: "orch", ProjectID: "proj", Kind: domain.KindOrchestrator, IsTerminated: false}
	store.checks["c1"] = domain.SmokeCheck{ID: "c1", SessionID: "w1", Seq: 1, Name: "A", Verdict: domain.SmokeFail}
	msg := &fakeMessenger{}
	svc := newTestService(t, store, msg)

	out, err := svc.Report(context.Background(), "w1")
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if out.Target != "orchestrator" || !out.Delivered {
		t.Fatalf("outcome = %+v, want delivered to orchestrator", out)
	}
	if !strings.Contains(msg.sent["orch"], "[smoke results for @w1]") {
		t.Fatalf("orchestrator wrapper missing: %q", msg.sent["orch"])
	}
}

func TestReportPersistOnlyWhenNoLiveTarget(t *testing.T) {
	store := newFakeStore()
	store.sessions["w1"] = domain.SessionRecord{ID: "w1", ProjectID: "proj", Kind: domain.KindWorker, IsTerminated: true}
	store.checks["c1"] = domain.SmokeCheck{ID: "c1", SessionID: "w1", Seq: 1, Name: "A", Verdict: domain.SmokeSkip}
	msg := &fakeMessenger{}
	svc := newTestService(t, store, msg)

	out, err := svc.Report(context.Background(), "w1")
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if out.Target != "persisted" || out.Delivered {
		t.Fatalf("outcome = %+v, want persist-only", out)
	}
	if len(msg.sent) != 0 {
		t.Fatalf("expected no sends, got %v", msg.sent)
	}
	if _, ok := store.reported["w1"]; !ok {
		t.Fatal("persist-only must still stamp reported_at")
	}
}

func TestReportRejectsEmptyChecklist(t *testing.T) {
	store := newFakeStore()
	store.sessions["w1"] = domain.SessionRecord{ID: "w1", ProjectID: "proj"}
	svc := newTestService(t, store, &fakeMessenger{})
	if _, err := svc.Report(context.Background(), "w1"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty checklist report err = %v, want ErrInvalid", err)
	}
}

func TestPurgeSessionEvidenceRemovesTree(t *testing.T) {
	store := newFakeStore()
	svc := New(store, t.TempDir(), nil)
	dir := svc.sessionDir("w1")
	if err := os.MkdirAll(filepath.Join(dir, "c1"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "c1", "ev"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := svc.PurgeSessionEvidence(context.Background(), "w1"); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("session evidence dir still present: %v", err)
	}
}
