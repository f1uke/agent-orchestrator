package smoke

import (
	"context"
	"errors"
	"fmt"
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
		// smoke_check.id is a global primary key, so reusing an id another
		// session already owns fails the real insert. Model that here, or a
		// test would pass against a collision the daemon 500s on.
		if prior, ok := f.checks[c.ID]; ok && prior.SessionID != sessionID {
			return nil, nil, fmt.Errorf("UNIQUE constraint failed: smoke_check.id (%s owned by %s)", c.ID, prior.SessionID)
		}
	}
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

func (f *fakeStore) DeleteSmokeEvidence(_ context.Context, id string) (bool, error) {
	if _, ok := f.evidence[id]; !ok {
		return false, nil
	}
	delete(f.evidence, id)
	return true, nil
}

func (f *fakeStore) ListSmokeEvidenceCreatedBefore(_ context.Context, before time.Time) ([]domain.SmokeEvidence, error) {
	var out []domain.SmokeEvidence
	for _, ev := range f.evidence {
		if ev.CreatedAt.Before(before) {
			out = append(out, ev)
		}
	}
	return out, nil
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

// TestDerivedCaseIDCompatibility pins the ids ASCII names have produced since
// the feature shipped. A checklist already stored keeps its ids only if these
// stay byte-identical, so a verdict never detaches from its case.
func TestDerivedCaseIDCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name string
		want string
	}{
		{"A fresh MR shows up", "a-fresh-mr-shows-up"},
		{"Build passes", "build-passes"},
		{"Tests tab: verdict sticks (pass/fail)", "tests-tab-verdict-sticks-pass-fail"},
		{"  padded  ", "padded"},
		{"MiXeD CaSe 123", "mixed-case-123"},
		{"worker เขียน smoke case", "worker-smoke-case"},
		{
			"a name that is considerably longer than the sixty four character cap imposed on ids",
			"a-name-that-is-considerably-longer-than-the-sixty-four-character",
		},
	} {
		if got := derivedCaseID(tc.name); got != tc.want {
			t.Errorf("derivedCaseID(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestDerivedCaseIDNonASCII covers names the slug reduces to nothing: every
// non-ASCII script, and ASCII that is pure punctuation. Each must still get a
// usable id, and it must be deterministic so a re-author reproduces it.
func TestDerivedCaseIDNonASCII(t *testing.T) {
	// Oracle: first 8 hex of sha256(name), computed independently via shasum.
	for _, tc := range []struct {
		name string
		want string
	}{
		{"เปิดแอปแล้วเห็นหน้าแรก", "case-d531e72c"},
		{"ลบรายการแล้วหายจากลิสต์", "case-cea420ac"},
		{"---", "case-cb3f91d5"},
	} {
		got := derivedCaseID(tc.name)
		if got != tc.want {
			t.Errorf("derivedCaseID(%q) = %q, want %q", tc.name, got, tc.want)
		}
		if again := derivedCaseID(tc.name); again != got {
			t.Errorf("derivedCaseID(%q) not deterministic: %q then %q", tc.name, got, again)
		}
	}
	// Distinct names must not share an id.
	if derivedCaseID("เปิดแอปแล้วเห็นหน้าแรก") == derivedCaseID("ลบรายการแล้วหายจากลิสต์") {
		t.Fatal("distinct non-ASCII names collapsed to the same id")
	}
}

// TestAuthorThaiChecklistNoCollision is the reproduction: a Thai checklist
// authored while ANOTHER session already owns ids derived the same way. The
// pre-fix code derived the constant "case"/"case-2" for every such checklist,
// so the second session's insert hit the global primary key and 500'd.
func TestAuthorThaiChecklistNoCollision(t *testing.T) {
	store := newFakeStore()
	store.sessions["w1"] = domain.SessionRecord{ID: "w1", ProjectID: "proj"}
	store.sessions["w2"] = domain.SessionRecord{ID: "w2", ProjectID: "proj"}
	svc := newTestService(t, store, nil)

	cases := []domain.SmokeAuthoredCase{
		{Name: "เปิดแอปแล้วเห็นหน้าแรก"},
		{Name: "กดปุ่มบันทึกแล้วขึ้นข้อความสำเร็จ"},
	}
	if _, err := svc.Author(context.Background(), "w1", cases); err != nil {
		t.Fatalf("first session author: %v", err)
	}
	// A different session, different Thai names: must not collide.
	if _, err := svc.Author(context.Background(), "w2", []domain.SmokeAuthoredCase{
		{Name: "ลบรายการแล้วหายจากลิสต์"},
	}); err != nil {
		t.Fatalf("second session author: %v", err)
	}
	for _, c := range store.lastCases {
		if c.ID == "" {
			t.Fatal("derived an empty id")
		}
	}
}

// TestAuthorAvoidsIDOwnedByAnotherSession covers the general mechanism, not the
// non-ASCII trigger: two sessions picking the SAME case name. The id column is
// global, so the second session must land on a different id rather than fail.
func TestAuthorAvoidsIDOwnedByAnotherSession(t *testing.T) {
	store := newFakeStore()
	store.sessions["w1"] = domain.SessionRecord{ID: "w1", ProjectID: "proj"}
	store.sessions["w2"] = domain.SessionRecord{ID: "w2", ProjectID: "proj"}
	svc := newTestService(t, store, nil)

	if _, err := svc.Author(context.Background(), "w1", []domain.SmokeAuthoredCase{{Name: "Build passes"}}); err != nil {
		t.Fatalf("first session: %v", err)
	}
	if store.lastCases[0].ID != "build-passes" {
		t.Fatalf("first session id = %q, want unchanged slug", store.lastCases[0].ID)
	}
	if _, err := svc.Author(context.Background(), "w2", []domain.SmokeAuthoredCase{{Name: "Build passes"}}); err != nil {
		t.Fatalf("second session: %v", err)
	}
	second := store.lastCases[0].ID
	if second == "build-passes" {
		t.Fatal("second session reused the first session's id")
	}
	if second == "" {
		t.Fatal("second session got an empty id")
	}
}

// TestAuthorIDsStableAcrossReauthor is the whole point of a derived id: the
// user's verdict, note and evidence survive the worker rewriting the checklist.
func TestAuthorIDsStableAcrossReauthor(t *testing.T) {
	store := newFakeStore()
	store.sessions["w1"] = domain.SessionRecord{ID: "w1", ProjectID: "proj"}
	store.sessions["other"] = domain.SessionRecord{ID: "other", ProjectID: "proj"}
	svc := newTestService(t, store, nil)

	// Another session holds the ids w1 would otherwise derive, forcing w1 down
	// the collision path — the ids must still be reproducible.
	if _, err := svc.Author(context.Background(), "other", []domain.SmokeAuthoredCase{
		{Name: "เปิดแอปแล้วเห็นหน้าแรก"},
		{Name: "Shared name"},
	}); err != nil {
		t.Fatalf("other session: %v", err)
	}

	cases := []domain.SmokeAuthoredCase{
		{Name: "เปิดแอปแล้วเห็นหน้าแรก"},
		{Name: "Shared name"},
		{Name: "กดปุ่มบันทึกแล้วขึ้นข้อความสำเร็จ"},
	}
	if _, err := svc.Author(context.Background(), "w1", cases); err != nil {
		t.Fatalf("author: %v", err)
	}
	first := make([]string, 0, len(store.lastCases))
	for _, c := range store.lastCases {
		first = append(first, c.ID)
	}
	// Re-author the identical checklist: same ids, or verdicts detach.
	if _, err := svc.Author(context.Background(), "w1", cases); err != nil {
		t.Fatalf("re-author: %v", err)
	}
	for i, c := range store.lastCases {
		if c.ID != first[i] {
			t.Errorf("case %d id shifted on re-author: %q then %q", i, first[i], c.ID)
		}
	}
}

// TestAuthorDedupesNonASCIIDuplicatesWithinChecklist keeps the within-payload
// dedupe working for names that share a derived id.
func TestAuthorDedupesNonASCIIDuplicatesWithinChecklist(t *testing.T) {
	store := newFakeStore()
	store.sessions["w1"] = domain.SessionRecord{ID: "w1", ProjectID: "proj"}
	svc := newTestService(t, store, nil)

	if _, err := svc.Author(context.Background(), "w1", []domain.SmokeAuthoredCase{
		{Name: "เปิดแอปแล้วเห็นหน้าแรก"},
		{Name: "เปิดแอปแล้วเห็นหน้าแรก"},
		{Name: "---"},
	}); err != nil {
		t.Fatalf("author: %v", err)
	}
	seen := map[string]struct{}{}
	for i, c := range store.lastCases {
		if c.ID == "" {
			t.Fatalf("case %d got an empty id", i)
		}
		if _, dup := seen[c.ID]; dup {
			t.Fatalf("case %d reused id %q within one checklist", i, c.ID)
		}
		seen[c.ID] = struct{}{}
	}
}

// TestAuthorExplicitIDWithoutASCII falls back to the name when a supplied id
// slugs away to nothing, instead of the old shared "case" constant.
func TestAuthorExplicitIDWithoutASCII(t *testing.T) {
	store := newFakeStore()
	store.sessions["w1"] = domain.SessionRecord{ID: "w1", ProjectID: "proj"}
	svc := newTestService(t, store, nil)

	if _, err := svc.Author(context.Background(), "w1", []domain.SmokeAuthoredCase{
		{ID: "***", Name: "เปิดแอปแล้วเห็นหน้าแรก"},
	}); err != nil {
		t.Fatalf("author: %v", err)
	}
	if got := store.lastCases[0].ID; got != "case-d531e72c" {
		t.Fatalf("id = %q, want the name-derived id", got)
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

func TestRemoveEvidence(t *testing.T) {
	store := newFakeStore()
	store.checks["c1"] = domain.SmokeCheck{ID: "c1", SessionID: "w1"}
	svc := newTestService(t, store, nil)
	ctx := context.Background()

	ev, err := svc.AttachEvidence(ctx, "w1", "c1", EvidenceUpload{Filename: "shot.png", Mime: "image/png", Reader: strings.NewReader("PNGDATA")})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	blob, err := svc.OpenEvidence(ctx, "w1", "c1", ev.ID)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// Foreign session → ErrNotFound (requireCheck rejects), row + blob untouched.
	if _, err := svc.RemoveEvidence(ctx, "other", "c1", ev.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign session err = %v, want ErrNotFound", err)
	}
	// Unknown evidence id under a valid case → ErrNotFound.
	if _, err := svc.RemoveEvidence(ctx, "w1", "c1", "ev_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id err = %v, want ErrNotFound", err)
	}
	if len(store.evidence) != 1 {
		t.Fatalf("evidence row removed prematurely: have %d, want 1", len(store.evidence))
	}
	if _, err := os.Stat(blob.Path); err != nil {
		t.Fatalf("blob removed prematurely: %v", err)
	}

	// Materialize an export copy so we can prove RemoveEvidence drops it too.
	exportPath, err := svc.ExportEvidence(ctx, "w1", "c1", ev.ID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if _, err := os.Stat(exportPath); err != nil {
		t.Fatalf("export copy missing: %v", err)
	}

	// Valid removal → DB row gone and on-disk blob + export copy deleted.
	if _, err := svc.RemoveEvidence(ctx, "w1", "c1", ev.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(store.evidence) != 0 {
		t.Fatalf("evidence row not removed: have %d", len(store.evidence))
	}
	if _, err := os.Stat(blob.Path); !os.IsNotExist(err) {
		t.Fatalf("blob still on disk after remove: err = %v", err)
	}
	if _, err := os.Stat(exportPath); !os.IsNotExist(err) {
		t.Fatalf("export copy still on disk after remove: err = %v", err)
	}
}

func TestExportEvidenceNamesAndExtension(t *testing.T) {
	store := newFakeStore()
	store.checks["login-check"] = domain.SmokeCheck{ID: "login-check", SessionID: "w1"}
	svc := newTestService(t, store, nil)
	ctx := context.Background()

	// Image with a display filename → "<case>-<stem>.png", opens by content type.
	img, err := svc.AttachEvidence(ctx, "w1", "login-check", EvidenceUpload{Filename: "Screen Shot.png", Mime: "image/png", Reader: strings.NewReader("PNGDATA")})
	if err != nil {
		t.Fatalf("attach image: %v", err)
	}
	path, err := svc.ExportEvidence(ctx, "w1", "login-check", img.ID)
	if err != nil {
		t.Fatalf("export image: %v", err)
	}
	base := filepath.Base(path)
	if filepath.Ext(base) != ".png" {
		t.Fatalf("export ext = %q, want .png (path %s)", filepath.Ext(base), path)
	}
	if !strings.HasPrefix(base, "login-check-") {
		t.Fatalf("export base %q missing case prefix", base)
	}
	if !strings.Contains(path, string(filepath.Separator)+openExportSubdir+string(filepath.Separator)) {
		t.Fatalf("export path %q not under _open/", path)
	}
	if got, _ := os.ReadFile(path); string(got) != "PNGDATA" {
		t.Fatalf("export content = %q, want PNGDATA", got)
	}

	// quicktime MIME → .mov even though the stored filename is empty (stem falls
	// back to the evidence id); the MIME, not the filename, drives the extension.
	vid, err := svc.AttachEvidence(ctx, "w1", "login-check", EvidenceUpload{Mime: "video/quicktime", Reader: strings.NewReader("MOVDATA")})
	if err != nil {
		t.Fatalf("attach video: %v", err)
	}
	vpath, err := svc.ExportEvidence(ctx, "w1", "login-check", vid.ID)
	if err != nil {
		t.Fatalf("export video: %v", err)
	}
	if filepath.Ext(vpath) != ".mov" {
		t.Fatalf("video export ext = %q, want .mov", filepath.Ext(vpath))
	}
	if !strings.Contains(filepath.Base(vpath), vid.ID) {
		t.Fatalf("empty-filename export %q should fall back to the evidence id", filepath.Base(vpath))
	}

	// Idempotent: a repeat export returns the same path and the file still exists.
	again, err := svc.ExportEvidence(ctx, "w1", "login-check", img.ID)
	if err != nil || again != path {
		t.Fatalf("repeat export = (%q, %v), want (%q, nil)", again, err, path)
	}

	// Foreign session is rejected before any copy is made.
	if _, err := svc.ExportEvidence(ctx, "other", "login-check", img.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign export err = %v, want ErrNotFound", err)
	}
}

func TestPurgeEvidenceOlderThan(t *testing.T) {
	store := newFakeStore()
	store.checks["c1"] = domain.SmokeCheck{ID: "c1", SessionID: "w1"}
	store.checks["c2"] = domain.SmokeCheck{ID: "c2", SessionID: "w2"}
	base := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	now := base
	svc := New(store, t.TempDir(), nil, WithClock(func() time.Time { return now }))
	ctx := context.Background()

	now = base.Add(-40 * 24 * time.Hour) // 40 days old → should expire
	old, err := svc.AttachEvidence(ctx, "w1", "c1", EvidenceUpload{Filename: "old.png", Mime: "image/png", Reader: strings.NewReader("OLDDATA")})
	if err != nil {
		t.Fatalf("attach old: %v", err)
	}
	oldBlob, _ := svc.OpenEvidence(ctx, "w1", "c1", old.ID)
	oldExport, err := svc.ExportEvidence(ctx, "w1", "c1", old.ID)
	if err != nil {
		t.Fatalf("export old: %v", err)
	}

	now = base.Add(-5 * 24 * time.Hour) // 5 days old, different session → kept
	recent, err := svc.AttachEvidence(ctx, "w2", "c2", EvidenceUpload{Filename: "recent.mov", Mime: "video/quicktime", Reader: strings.NewReader("MOVDATA")})
	if err != nil {
		t.Fatalf("attach recent: %v", err)
	}
	recentBlob, _ := svc.OpenEvidence(ctx, "w2", "c2", recent.ID)

	cutoff := base.Add(-30 * 24 * time.Hour) // 30-day TTL
	res, err := svc.PurgeEvidenceOlderThan(ctx, cutoff)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if res.Purged != 1 || res.FreedBytes != int64(len("OLDDATA")) {
		t.Fatalf("purge result = %+v, want {1, %d}", res, len("OLDDATA"))
	}
	// Old item: row + blob + export copy all gone.
	if _, ok := store.evidence[old.ID]; ok {
		t.Fatal("old evidence row not purged")
	}
	if _, err := os.Stat(oldBlob.Path); !os.IsNotExist(err) {
		t.Fatalf("old blob still on disk: %v", err)
	}
	if _, err := os.Stat(oldExport); !os.IsNotExist(err) {
		t.Fatalf("old export copy still on disk: %v", err)
	}
	// Recent item in the OTHER session: fully intact — the sweep never touched it.
	if _, ok := store.evidence[recent.ID]; !ok {
		t.Fatal("recent evidence row wrongly purged")
	}
	if _, err := os.Stat(recentBlob.Path); err != nil {
		t.Fatalf("recent blob wrongly removed: %v", err)
	}

	// Idempotent: re-running purges nothing new.
	res2, err := svc.PurgeEvidenceOlderThan(ctx, cutoff)
	if err != nil || res2.Purged != 0 {
		t.Fatalf("second purge = (%+v, %v), want ({0,0}, nil)", res2, err)
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
