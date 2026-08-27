package smoke

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// fakeStore is an in-memory Store for exercising service logic in isolation.
type fakeStore struct {
	checks    map[string]domain.SmokeCheck
	sessions  map[domain.SessionID]domain.SessionRecord
	evidence  map[string]domain.SmokeEvidence
	lastCases []domain.SmokeAuthoredCase
	reported  map[domain.SessionID]time.Time
	standDown map[domain.SessionID]domain.SmokeStandDown
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		checks:    map[string]domain.SmokeCheck{},
		sessions:  map[domain.SessionID]domain.SessionRecord{},
		evidence:  map[string]domain.SmokeEvidence{},
		reported:  map[domain.SessionID]time.Time{},
		standDown: map[domain.SessionID]domain.SmokeStandDown{},
	}
}

func (f *fakeStore) ListSmokeChecksBySession(_ context.Context, id domain.SessionID) ([]domain.SmokeCheck, error) {
	var out []domain.SmokeCheck
	for _, c := range f.checks {
		if c.SessionID == id {
			f.loadEvidence(&c)
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Retired() != out[j].Retired() {
			return !out[i].Retired()
		}
		return out[i].Seq < out[j].Seq
	})
	return out, nil
}

func (f *fakeStore) GetSmokeCheck(_ context.Context, id string) (domain.SmokeCheck, bool, error) {
	c, ok := f.checks[id]
	if ok {
		f.loadEvidence(&c)
	}
	return c, ok, nil
}

// loadEvidence mirrors the real store, which loads a case's evidence rows with
// the case itself and SPLITS them by provenance; the service reads
// len(check.Evidence) to decide whether the user has anything invested in a
// case, and len(check.AgentEvidence) to accept an evidence-only record. A fake
// that pooled them would hide exactly the mix-up the split exists to prevent.
func (f *fakeStore) loadEvidence(check *domain.SmokeCheck) {
	check.Evidence = []domain.SmokeEvidence{}
	check.AgentEvidence = []domain.SmokeEvidence{}
	var rows []domain.SmokeEvidence
	for _, ev := range f.evidence {
		if ev.CheckID == check.ID {
			rows = append(rows, ev)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	for _, ev := range rows {
		if ev.Source == domain.SmokeEvidenceAgent {
			check.AgentEvidence = append(check.AgentEvidence, ev)
			continue
		}
		check.Evidence = append(check.Evidence, ev)
	}
}

func (f *fakeStore) evidenceFor(checkID string) []domain.SmokeEvidence {
	check := domain.SmokeCheck{ID: checkID}
	f.loadEvidence(&check)
	return check.Evidence
}

func (f *fakeStore) ReplaceSmokeChecks(_ context.Context, sessionID domain.SessionID, projectID domain.ProjectID, cases []domain.SmokeAuthoredCase, author domain.SmokeAuthor, now time.Time) ([]domain.SmokeCheck, []string, error) {
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
	keep := make(map[string]struct{}, len(cases))
	for _, c := range cases {
		keep[c.ID] = struct{}{}
	}
	// The real store deletes a session's cases that the payload leaves out (their
	// evidence rows cascade) and reports them back so the caller can sweep the
	// blobs. A fake that skipped that could not express the loss this guards.
	var removed []string
	for id, prior := range f.checks {
		if prior.SessionID != sessionID {
			continue
		}
		// A retired case is invisible to the replace: never deleted for being
		// absent from the payload, never rewritten.
		if prior.Retired() {
			continue
		}
		if _, ok := keep[id]; !ok {
			delete(f.checks, id)
			for evID, ev := range f.evidence {
				if ev.CheckID == id {
					delete(f.evidence, evID)
				}
			}
			removed = append(removed, id)
		}
	}
	sort.Strings(removed)
	for _, c := range cases {
		// Every authored field, not a subset: a fake that drops one lets a service
		// test pass while the real path loses it (the same class of bug the CLI's
		// response DTO carries a comment about).
		check := domain.SmokeCheck{ID: c.ID, SessionID: sessionID, ProjectID: projectID, Seq: c.Seq, Name: c.Name, Why: c.Why, Steps: c.Steps, Expected: c.Expected, PRNum: c.PRNum, FileRef: c.FileRef, Verdict: domain.SmokePending, Evidence: []domain.SmokeEvidence{}, CreatedAt: now, UpdatedAt: now}
		// An id already present keeps what the user recorded on it (its evidence
		// rows are keyed to the id and joined on read, so they follow).
		if prior, ok := f.checks[c.ID]; ok {
			check.Verdict, check.Note, check.DecidedAt = prior.Verdict, prior.Note, prior.DecidedAt
			check.AgentVerdict, check.AgentNote = prior.AgentVerdict, prior.AgentNote
			check.AgentRanAt, check.AgentSHA = prior.AgentRanAt, prior.AgentSHA
			check.RetiredAt, check.RetiredReason = prior.RetiredAt, prior.RetiredReason
			check.CreatedAt = prior.CreatedAt
		}
		stampAuthor(&check, author, now)
		f.checks[c.ID] = check
		out = append(out, check)
	}
	delete(f.standDown, sessionID)
	return out, removed, nil
}

// UpsertSmokeChecks is the per-case write: it touches only the cases it names.
// The fake models exactly that, because "an author cannot reach a case it did
// not name" is the property the shared-checklist tests are asserting.
func (f *fakeStore) UpsertSmokeChecks(_ context.Context, sessionID domain.SessionID, projectID domain.ProjectID, cases []domain.SmokeAuthoredCase, author domain.SmokeAuthor, now time.Time) ([]domain.SmokeCheck, error) {
	f.lastCases = cases
	nextSeq := 0
	for _, prior := range f.checks {
		if prior.SessionID == sessionID && prior.Seq > nextSeq {
			nextSeq = prior.Seq
		}
	}
	for _, c := range cases {
		if prior, ok := f.checks[c.ID]; ok && prior.SessionID != sessionID {
			return nil, fmt.Errorf("UNIQUE constraint failed: smoke_check.id (%s owned by %s)", c.ID, prior.SessionID)
		}
	}
	for _, c := range cases {
		check := domain.SmokeCheck{ID: c.ID, SessionID: sessionID, ProjectID: projectID, Name: c.Name, Why: c.Why, Steps: c.Steps, Expected: c.Expected, PRNum: c.PRNum, FileRef: c.FileRef, Verdict: domain.SmokePending, Evidence: []domain.SmokeEvidence{}, CreatedAt: now, UpdatedAt: now}
		if prior, ok := f.checks[c.ID]; ok {
			check.Seq = prior.Seq
			check.Verdict, check.Note, check.DecidedAt = prior.Verdict, prior.Note, prior.DecidedAt
			check.AgentVerdict, check.AgentNote = prior.AgentVerdict, prior.AgentNote
			check.AgentRanAt, check.AgentSHA = prior.AgentRanAt, prior.AgentSHA
			check.RetiredAt, check.RetiredReason = prior.RetiredAt, prior.RetiredReason
			check.CreatedAt = prior.CreatedAt
		} else {
			nextSeq++
			check.Seq = nextSeq
		}
		stampAuthor(&check, author, now)
		f.checks[c.ID] = check
	}
	delete(f.standDown, sessionID)
	return f.ListSmokeChecksBySession(context.Background(), sessionID)
}

func (f *fakeStore) PatchSmokeCheckAuthored(_ context.Context, id string, patch domain.SmokeCasePatch, author domain.SmokeAuthor, now time.Time) (bool, error) {
	check, ok := f.checks[id]
	if !ok {
		return false, nil
	}
	if patch.Name != nil {
		check.Name = *patch.Name
	}
	if patch.Why != nil {
		check.Why = *patch.Why
	}
	if patch.Steps != nil {
		check.Steps = *patch.Steps
	}
	if patch.Expected != nil {
		check.Expected = *patch.Expected
	}
	if patch.PRNum != nil {
		check.PRNum = *patch.PRNum
	}
	if patch.FileRef != nil {
		check.FileRef = *patch.FileRef
	}
	check.UpdatedAt = now
	stampAuthor(&check, author, now)
	f.checks[id] = check
	return true, nil
}

func (f *fakeStore) DeleteSmokeCheck(_ context.Context, id string) (bool, error) {
	if _, ok := f.checks[id]; !ok {
		return false, nil
	}
	delete(f.checks, id)
	for evID, ev := range f.evidence {
		if ev.CheckID == id {
			delete(f.evidence, evID)
		}
	}
	return true, nil
}

func (f *fakeStore) GetSmokeChecklistStandDown(_ context.Context, sessionID domain.SessionID) (domain.SmokeStandDown, bool, error) {
	sd, ok := f.standDown[sessionID]
	return sd, ok, nil
}

func (f *fakeStore) SetSmokeChecklistStandDown(_ context.Context, sessionID domain.SessionID, reason string, author domain.SmokeAuthor, now time.Time) error {
	f.standDown[sessionID] = domain.SmokeStandDown{SessionID: sessionID, At: now, By: author.ID, ByRole: author.Role, Reason: reason, CreatedAt: now, UpdatedAt: now}
	return nil
}

func (f *fakeStore) ClearSmokeChecklistStandDown(_ context.Context, sessionID domain.SessionID) error {
	delete(f.standDown, sessionID)
	return nil
}

// stampAuthor mirrors the store: a write AO cannot attribute leaves all three
// fields empty rather than stamping a time with nobody's name on it.
func stampAuthor(check *domain.SmokeCheck, author domain.SmokeAuthor, now time.Time) {
	if author.ID == "" {
		return
	}
	at := now
	check.AuthoredBy, check.AuthoredByRole, check.AuthoredAt = author.ID, author.Role, &at
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
	// Like the real store: only the USER's evidence rows go.
	for evID, ev := range f.evidence {
		if ev.CheckID == id && ev.Source != domain.SmokeEvidenceAgent {
			delete(f.evidence, evID)
		}
	}
	return true, nil
}

func (f *fakeStore) SetSmokeAgentResult(_ context.Context, id string, res domain.SmokeAgentResult, ranAt, now time.Time) (bool, error) {
	c, ok := f.checks[id]
	if !ok || c.Retired() {
		return false, nil
	}
	c.AgentVerdict, c.AgentNote, c.AgentSHA = res.Verdict, res.Note, res.SHA
	c.AgentRanAt, c.UpdatedAt = &ranAt, now
	f.checks[id] = c
	return true, nil
}

func (f *fakeStore) RetireSmokeCheck(_ context.Context, id, reason string, retiredAt, now time.Time) (bool, error) {
	c, ok := f.checks[id]
	if !ok || c.Retired() {
		return false, nil
	}
	c.RetiredAt, c.RetiredReason, c.UpdatedAt = &retiredAt, reason, now
	f.checks[id] = c
	return true, nil
}

func (f *fakeStore) ListUserSmokeEvidence(_ context.Context, checkID string) ([]domain.SmokeEvidence, error) {
	return f.evidenceFor(checkID), nil
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
	outcome ports.SendOutcome
	sent    map[domain.SessionID]string
	err     error
}

func (m *fakeMessenger) Send(_ context.Context, id domain.SessionID, message string) (ports.SendOutcome, error) {
	if m.err != nil {
		return ports.SendOutcome{}, m.err
	}
	if m.sent == nil {
		m.sent = map[domain.SessionID]string{}
	}
	m.sent[id] = message
	return m.outcome, nil
}

func newTestService(t *testing.T, store Store, msg Messenger) *Service {
	t.Helper()
	return New(store, t.TempDir(), msg, WithClock(func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }))
}

func TestAuthorResolvesIdsAndSeq(t *testing.T) {
	store := newFakeStore()
	store.sessions["w1"] = domain.SessionRecord{ID: "w1", ProjectID: "proj", Kind: domain.KindWorker}
	svc := newTestService(t, store, nil)

	_, err := svc.Author(context.Background(), "", "w1", []domain.SmokeAuthoredCase{
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
	if _, err := svc.Author(context.Background(), "", "w1", cases); err != nil {
		t.Fatalf("first session author: %v", err)
	}
	// A different session, different Thai names: must not collide.
	if _, err := svc.Author(context.Background(), "", "w2", []domain.SmokeAuthoredCase{
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

	if _, err := svc.Author(context.Background(), "", "w1", []domain.SmokeAuthoredCase{{Name: "Build passes"}}); err != nil {
		t.Fatalf("first session: %v", err)
	}
	if store.lastCases[0].ID != "build-passes" {
		t.Fatalf("first session id = %q, want unchanged slug", store.lastCases[0].ID)
	}
	if _, err := svc.Author(context.Background(), "", "w2", []domain.SmokeAuthoredCase{{Name: "Build passes"}}); err != nil {
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
	if _, err := svc.Author(context.Background(), "", "other", []domain.SmokeAuthoredCase{
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
	if _, err := svc.Author(context.Background(), "", "w1", cases); err != nil {
		t.Fatalf("author: %v", err)
	}
	first := make([]string, 0, len(store.lastCases))
	for _, c := range store.lastCases {
		first = append(first, c.ID)
	}
	// Re-author the identical checklist: same ids, or verdicts detach.
	if _, err := svc.Author(context.Background(), "", "w1", cases); err != nil {
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

	if _, err := svc.Author(context.Background(), "", "w1", []domain.SmokeAuthoredCase{
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

	if _, err := svc.Author(context.Background(), "", "w1", []domain.SmokeAuthoredCase{
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

	if _, err := svc.Author(context.Background(), "", "w1", nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty list err = %v, want ErrInvalid", err)
	}
	if _, err := svc.Author(context.Background(), "", "w1", []domain.SmokeAuthoredCase{{Name: "  "}}); !errors.Is(err, ErrInvalid) {
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

// TestAuthorRefusesToDropPlayedCases pins the property: any one of verdict,
// note or evidence makes a case undroppable by an author call, and the refusal
// carries ErrResultsAtRisk so the HTTP layer answers 422 rather than 500.
func TestAuthorRefusesToDropPlayedCases(t *testing.T) {
	ctx := context.Background()
	cases := []domain.SmokeAuthoredCase{{ID: "played", Name: "Played case"}, {ID: "draft", Name: "Draft case"}}

	for _, tc := range []struct {
		name string
		play func(t *testing.T, svc *Service, store *fakeStore)
		want string
	}{
		{
			name: "verdict",
			play: func(t *testing.T, svc *Service, _ *fakeStore) {
				if _, err := svc.SetVerdict(ctx, "w1", "played", domain.SmokeFail, ""); err != nil {
					t.Fatalf("set verdict: %v", err)
				}
			},
			want: "verdict fail",
		},
		{
			name: "note only",
			play: func(t *testing.T, _ *Service, store *fakeStore) {
				c := store.checks["played"]
				c.Note = "the toast never showed"
				store.checks["played"] = c
			},
			want: "a note",
		},
		{
			name: "evidence only",
			play: func(t *testing.T, svc *Service, _ *fakeStore) {
				if _, err := svc.AttachEvidence(ctx, "w1", "played", EvidenceUpload{
					Filename: "shot.png", Mime: "image/png", Reader: strings.NewReader("PNG"),
				}); err != nil {
					t.Fatalf("attach evidence: %v", err)
				}
			},
			want: "1 evidence file",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeStore()
			store.sessions["w1"] = domain.SessionRecord{ID: "w1", ProjectID: "proj"}
			svc := newTestService(t, store, nil)
			if _, err := svc.Author(ctx, "", "w1", cases); err != nil {
				t.Fatalf("author: %v", err)
			}
			tc.play(t, svc, store)

			// The agent rewords the played case's name: a new derived id, so the
			// old case would fall out of the payload and be deleted.
			_, err := svc.Author(ctx, "", "w1", []domain.SmokeAuthoredCase{{Name: "Played case, reworded"}, {ID: "draft", Name: "Draft case"}})
			if !errors.Is(err, ErrResultsAtRisk) {
				t.Fatalf("re-author err = %v, want ErrResultsAtRisk", err)
			}
			for _, want := range []string{`"Played case"`, `"played"`, tc.want, `"id": "played"`} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal missing %s: %v", want, err)
				}
			}
			if strings.Contains(err.Error(), `"draft"`) {
				t.Errorf("refusal names the unplayed case too: %v", err)
			}
			if _, ok := store.checks["played"]; !ok {
				t.Fatal("the played case was deleted anyway")
			}
			if store.checks["played"].Name != "Played case" {
				t.Errorf("the refused payload was applied: name = %q", store.checks["played"].Name)
			}
		})
	}
}

// TestAuthorNamesEveryCaseAtRisk: the caller has to be able to fix the payload
// in one pass, so every played case that would be lost is listed, in seq order.
func TestAuthorNamesEveryCaseAtRisk(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	store.sessions["w1"] = domain.SessionRecord{ID: "w1", ProjectID: "proj"}
	svc := newTestService(t, store, nil)
	if _, err := svc.Author(ctx, "", "w1", []domain.SmokeAuthoredCase{
		{Name: "First case"}, {Name: "Second case"}, {Name: "Third case"},
	}); err != nil {
		t.Fatalf("author: %v", err)
	}
	for _, id := range []string{"first-case", "third-case"} {
		if _, err := svc.SetVerdict(ctx, "w1", id, domain.SmokePass, ""); err != nil {
			t.Fatalf("set verdict %s: %v", id, err)
		}
	}
	_, err := svc.Author(ctx, "", "w1", []domain.SmokeAuthoredCase{{Name: "Second case"}})
	if !errors.Is(err, ErrResultsAtRisk) {
		t.Fatalf("re-author err = %v, want ErrResultsAtRisk", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "2 cases the user already played are missing") {
		t.Errorf("refusal does not count the cases: %v", err)
	}
	first, third := strings.Index(msg, `"first-case"`), strings.Index(msg, `"third-case"`)
	if first < 0 || third < 0 {
		t.Fatalf("refusal does not name both cases: %v", err)
	}
	if first > third {
		t.Errorf("cases not listed in seq order: %v", err)
	}
}

// TestAuthorRevisesUnplayedCasesFreely: the guard must not freeze a draft
// checklist — an agent legitimately rewords, drops and adds cases nobody has
// played yet, and the blobs of a dropped case are still swept off disk.
func TestAuthorRevisesUnplayedCasesFreely(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	store.sessions["w1"] = domain.SessionRecord{ID: "w1", ProjectID: "proj"}
	dir := t.TempDir()
	svc := New(store, dir, nil, WithClock(func() time.Time { return time.Unix(0, 0).UTC() }))
	if _, err := svc.Author(ctx, "", "w1", []domain.SmokeAuthoredCase{{Name: "Draft one"}, {Name: "Draft two"}}); err != nil {
		t.Fatalf("author: %v", err)
	}
	// A dropped case's evidence dir is swept even when nothing played it (a
	// reset leaves the directory behind, and the export copies live inside it).
	stale := filepath.Join(dir, "evidence", "w1", "draft-two")
	if err := os.MkdirAll(stale, 0o750); err != nil {
		t.Fatalf("seed stale dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stale, "leftover"), []byte("bytes"), 0o600); err != nil {
		t.Fatalf("seed stale blob: %v", err)
	}

	res, err := svc.Author(ctx, "", "w1", []domain.SmokeAuthoredCase{{Name: "Draft one, reworded"}, {Name: "Draft three"}})
	if err != nil {
		t.Fatalf("re-author of an unplayed checklist: %v", err)
	}
	if len(res.Checks) != 2 {
		t.Fatalf("checks = %d, want 2", len(res.Checks))
	}
	if _, ok := store.checks["draft-two"]; ok {
		t.Error("dropped unplayed case survived")
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("dropped case's blobs not swept: %v", err)
	}
}

// TestAuthorKeepsResultsWhenTheIDIsSupplied: the way out of the refusal has to
// work — carrying the existing id across a rename keeps the user's results.
func TestAuthorKeepsResultsWhenTheIDIsSupplied(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	store.sessions["w1"] = domain.SessionRecord{ID: "w1", ProjectID: "proj"}
	svc := newTestService(t, store, nil)
	if _, err := svc.Author(ctx, "", "w1", []domain.SmokeAuthoredCase{{Name: "Played case"}}); err != nil {
		t.Fatalf("author: %v", err)
	}
	if _, err := svc.SetVerdict(ctx, "w1", "played-case", domain.SmokePass, "looked right"); err != nil {
		t.Fatalf("set verdict: %v", err)
	}
	res, err := svc.Author(ctx, "", "w1", []domain.SmokeAuthoredCase{{ID: "played-case", Name: "Played case, reworded"}})
	if err != nil {
		t.Fatalf("re-author with the id supplied: %v", err)
	}
	if len(res.Checks) != 1 {
		t.Fatalf("checks = %d, want 1", len(res.Checks))
	}
	got := res.Checks[0]
	if got.Name != "Played case, reworded" {
		t.Errorf("name = %q, want the reworded one", got.Name)
	}
	if got.Verdict != domain.SmokePass || got.Note != "looked right" {
		t.Errorf("results lost across the rename: verdict=%q note=%q", got.Verdict, got.Note)
	}
}
