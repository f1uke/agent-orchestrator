package testiny

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	testinyadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/testiny"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var (
	mob  = testinyadapter.Project{ID: 1, Name: "MOBILITY", Key: "MOB"}
	kern = testinyadapter.Project{ID: 2, Name: "KERN", Key: "KERN"}
)

// fakeTestiny stands in for the adapter: runs by id, each answer swappable,
// every call counted.
type fakeTestiny struct {
	mu       sync.Mutex
	runs     map[domain.TestinyRunID]testinyadapter.Run
	results  map[domain.TestinyRunID]testinyadapter.Results
	plans    map[int64]string
	ms       map[int64]string
	projects []testinyadapter.Project
	fail     error
	delay    map[domain.TestinyRunID]time.Duration
	calls    map[string]int
	sets     []setCall
	// failAfter, when set, fails SetResults once that many results are written.
	failAfter *int
	// details are the cases Case answers with, by id; any other id gets a
	// case with no steps.
	details map[int64]domain.TestinyCaseDetail
}

func newFakeTestiny() *fakeTestiny {
	return &fakeTestiny{
		runs: map[domain.TestinyRunID]testinyadapter.Run{
			632: {ID: 632, Title: "MOBILITY-4839 Chat notice disclaimer - iOS", ProjectID: 1},
			625: {ID: 625, Title: "MOBILITY-4901 Chat logout storm - iOS", ProjectID: 1, PlanID: 193, MilestoneID: 80},
			900: {ID: 900, Title: "KERN smoke", ProjectID: 2},
		},
		results: map[domain.TestinyRunID]testinyadapter.Results{
			632: {Cases: []testinyadapter.Case{{ID: 7166, Title: "Fund disclaimer", Status: "PASSED"}, {ID: 7167, Title: "Bond disclaimer", Status: "FAILED"}}, Summary: map[string]int{"PASSED": 1, "FAILED": 1}},
			625: {Cases: []testinyadapter.Case{{ID: 3818, Title: "Login", Status: "UNTESTED_NEW"}}, Summary: map[string]int{"UNTESTED_NEW": 1}},
			900: {Cases: []testinyadapter.Case{}, Summary: map[string]int{}},
		},
		plans:    map[int64]string{193: "Chat session logout"},
		ms:       map[int64]string{80: "Sprint 2026-20"},
		projects: []testinyadapter.Project{mob, kern},
		delay:    map[domain.TestinyRunID]time.Duration{},
		calls:    map[string]int{},
		details:  map[int64]domain.TestinyCaseDetail{},
	}
}

func (f *fakeTestiny) note(call string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[call]++
	return f.fail
}

func (f *fakeTestiny) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for k, v := range f.calls {
		if strings.HasPrefix(k, prefix) {
			n += v
		}
	}
	return n
}

func (f *fakeTestiny) setFail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = err
}

func (f *fakeTestiny) Run(_ context.Context, id domain.TestinyRunID) (testinyadapter.Run, error) {
	if err := f.note(fmt.Sprintf("run show %d", id)); err != nil {
		return testinyadapter.Run{}, err
	}
	f.mu.Lock()
	d := f.delay[id]
	f.mu.Unlock()
	time.Sleep(d)
	run, ok := f.runs[id]
	if !ok {
		return testinyadapter.Run{}, fmt.Errorf("%w: the entity with id %d was not found", testinyadapter.ErrNotFound, id)
	}
	return run, nil
}

func (f *fakeTestiny) Results(_ context.Context, id domain.TestinyRunID) (testinyadapter.Results, error) {
	if err := f.note(fmt.Sprintf("run results %d", id)); err != nil {
		return testinyadapter.Results{}, err
	}
	return f.results[id], nil
}

func (f *fakeTestiny) Plan(_ context.Context, id int64) (testinyadapter.Ref, error) {
	if err := f.note(fmt.Sprintf("plan show %d", id)); err != nil {
		return testinyadapter.Ref{}, err
	}
	return testinyadapter.Ref{ID: id, Title: f.plans[id]}, nil
}

func (f *fakeTestiny) Milestone(_ context.Context, id int64) (testinyadapter.Ref, error) {
	if err := f.note(fmt.Sprintf("milestone show %d", id)); err != nil {
		return testinyadapter.Ref{}, err
	}
	return testinyadapter.Ref{ID: id, Title: f.ms[id]}, nil
}

func (f *fakeTestiny) Project(_ context.Context, ref string) (testinyadapter.Project, error) {
	if err := f.note("project ls"); err != nil {
		return testinyadapter.Project{}, err
	}
	for _, p := range f.projects {
		if strings.EqualFold(p.Key, ref) || strings.EqualFold(p.Name, ref) || fmt.Sprint(p.ID) == ref {
			return p, nil
		}
	}
	return testinyadapter.Project{}, fmt.Errorf("%w: no Testiny project %q", testinyadapter.ErrNotFound, ref)
}

func (f *fakeTestiny) ProjectByID(ctx context.Context, id int64) (testinyadapter.Project, error) {
	return f.Project(ctx, fmt.Sprint(id))
}

// fakeStore is the links table and the session/project lookup.
type fakeStore struct {
	mu       sync.Mutex
	links    []domain.TestinyRunLink
	log      []domain.TestinyResultEntry
	sessions map[domain.SessionID]domain.SessionRecord
	projects map[string]domain.ProjectRecord
}

func newFakeStore(cfg domain.ProjectConfig) *fakeStore {
	return &fakeStore{
		sessions: map[domain.SessionID]domain.SessionRecord{"app-1": {ID: "app-1", ProjectID: "app"}},
		projects: map[string]domain.ProjectRecord{"app": {ID: "app", Config: cfg}},
	}
}

func (s *fakeStore) InsertTestinyRunLink(_ context.Context, l domain.TestinyRunLink) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, have := range s.links {
		if have.SessionID == l.SessionID && have.RunID == l.RunID {
			return nil
		}
	}
	s.links = append(s.links, l)
	return nil
}

func (s *fakeStore) DeleteTestinyRunLink(_ context.Context, sid domain.SessionID, id domain.TestinyRunID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.links[:0]
	for _, l := range s.links {
		if l.SessionID != sid || l.RunID != id {
			kept = append(kept, l)
		}
	}
	s.links = kept
	return nil
}

func (s *fakeStore) ListTestinyRunLinks(_ context.Context, sid domain.SessionID) ([]domain.TestinyRunLink, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.TestinyRunLink
	for _, l := range s.links {
		if l.SessionID == sid {
			out = append(out, l)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *fakeStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	r, ok := s.sessions[id]
	return r, ok, nil
}

func (s *fakeStore) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	r, ok := s.projects[id]
	return r, ok, nil
}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type rig struct {
	svc   *Service
	tny   *fakeTestiny
	store *fakeStore
	clock *clock
	home  string
}

func newRig(t *testing.T, cfg domain.ProjectConfig) *rig {
	t.Helper()
	r := &rig{
		tny:   newFakeTestiny(),
		store: newFakeStore(cfg),
		clock: &clock{now: time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)},
		home:  t.TempDir(),
	}
	r.svc = New(r.tny, r.store, r.store, Options{Home: r.home, Now: r.clock.Now})
	return r
}

var on = domain.ProjectConfig{TestinyProject: "MOB"}

func TestLinkConfirmsTheRunAndStoresIt(t *testing.T) {
	r := newRig(t, on)
	view, err := r.svc.Link(context.Background(), "app-1", "TR-625", "app-2")
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if view.Title != "MOBILITY-4901 Chat logout storm - iOS" || view.URL != "https://app.testiny.io/MOB/testruns/tr/625" {
		t.Fatalf("view = %+v", view)
	}
	if view.Plan == nil || *view.Plan != (domain.TestinyRef{ID: 193, Title: "Chat session logout"}) ||
		view.Milestone == nil || view.Milestone.Title != "Sprint 2026-20" {
		t.Fatalf("plan/milestone = %+v / %+v", view.Plan, view.Milestone)
	}
	if !reflect.DeepEqual(view.Counts, map[domain.TestinyCaseStatus]int{"UNTESTED_NEW": 1}) || view.Cases[0].Status != "UNTESTED_NEW" {
		t.Fatalf("an unknown status did not pass through raw: %+v %+v", view.Counts, view.Cases)
	}
	want := domain.TestinyRunLink{SessionID: "app-1", RunID: 625, LinkedBy: "app-2", CreatedAt: r.clock.Now()}
	if view.Link != want || !reflect.DeepEqual(r.store.links, []domain.TestinyRunLink{want}) {
		t.Fatalf("link = %+v, stored %+v, want %+v", view.Link, r.store.links, want)
	}

	// Linking again is a no-op that keeps who linked it first.
	r.clock.advance(time.Hour)
	again, err := r.svc.Link(context.Background(), "app-1", "https://app.testiny.io/MOB/testruns/tr/625", "")
	if err != nil {
		t.Fatalf("re-link: %v", err)
	}
	if again.Link != want || len(r.store.links) != 1 {
		t.Fatalf("re-link changed the link: %+v (stored %d)", again.Link, len(r.store.links))
	}
}

func TestLinkRefusesARunTestinyCannotConfirm(t *testing.T) {
	r := newRig(t, on)
	_, err := r.svc.Link(context.Background(), "app-1", "999999", "")
	if !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("err = %v, want ErrRunNotFound", err)
	}
	if len(r.store.links) != 0 {
		t.Fatalf("stored %+v for a run that does not exist", r.store.links)
	}

	r.tny.setFail(fmt.Errorf("%w: Unauthenticated user", testinyadapter.ErrAuth))
	if _, err := r.svc.Link(context.Background(), "app-1", "632", ""); !errors.Is(err, testinyadapter.ErrAuth) {
		t.Fatalf("err = %v, want the adapter's ErrAuth", err)
	}
	if len(r.store.links) != 0 {
		t.Fatalf("stored %+v while Testiny could not confirm the run", r.store.links)
	}
}

func TestLinkRefusesARunFromAnotherProject(t *testing.T) {
	r := newRig(t, on)
	_, err := r.svc.Link(context.Background(), "app-1", "900", "")
	if !errors.Is(err, ErrWrongProject) || !strings.Contains(err.Error(), "TR-900 belongs to KERN; this project uses MOB") {
		t.Fatalf("by the run's project: err = %v", err)
	}
	_, err = r.svc.Link(context.Background(), "app-1", "https://app.testiny.io/KERN/testruns/tr/632", "")
	if !errors.Is(err, ErrWrongProject) || !strings.Contains(err.Error(), "KERN") {
		t.Fatalf("by the URL's key: err = %v", err)
	}
	if len(r.store.links) != 0 {
		t.Fatalf("stored %+v", r.store.links)
	}
}

func TestLinkAcceptsTheProjectByName(t *testing.T) {
	r := newRig(t, domain.ProjectConfig{TestinyProject: "mobility"})
	if _, err := r.svc.Link(context.Background(), "app-1", "https://app.testiny.io/MOB/testruns/tr/632", ""); err != nil {
		t.Fatalf("Link: %v", err)
	}
}

func TestLinkNamesAConfiguredProjectTestinyDoesNotHave(t *testing.T) {
	r := newRig(t, domain.ProjectConfig{TestinyProject: "MOBX"})
	if _, err := r.svc.Link(context.Background(), "app-1", "632", ""); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("err = %v, want ErrProjectNotFound", err)
	}
}

func TestOffWithoutATestinyProject(t *testing.T) {
	r := newRig(t, domain.ProjectConfig{})
	if _, err := r.svc.Link(context.Background(), "app-1", "632", ""); !errors.Is(err, ErrOff) {
		t.Fatalf("Link err = %v, want ErrOff", err)
	}
	if _, err := r.svc.Runs(context.Background(), "app-1", false); !errors.Is(err, ErrOff) {
		t.Fatalf("Runs err = %v, want ErrOff", err)
	}
	if n := r.tny.count(""); n != 0 {
		t.Fatalf("called Testiny %d times on a project that does not use it", n)
	}
}

func TestBadRefAndUnknownSession(t *testing.T) {
	r := newRig(t, on)
	if _, err := r.svc.Link(context.Background(), "app-1", "TC-632", ""); !errors.Is(err, domain.ErrBadRunRef) {
		t.Fatalf("err = %v, want ErrBadRunRef", err)
	}
	if _, err := r.svc.Runs(context.Background(), "ghost-1", false); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("err = %v, want ErrSessionNotFound", err)
	}
}

func link(r *rig, ids ...domain.TestinyRunID) {
	for i, id := range ids {
		r.store.links = append(r.store.links, domain.TestinyRunLink{SessionID: "app-1", RunID: id, CreatedAt: r.clock.Now().Add(time.Duration(i) * time.Second)})
	}
}

func runIDs(views []domain.TestinyRunView) []domain.TestinyRunID {
	out := make([]domain.TestinyRunID, len(views))
	for i, v := range views {
		out[i] = v.Link.RunID
	}
	return out
}

func TestRunsKeepTheLinkOrderWhateverFinishesFirst(t *testing.T) {
	r := newRig(t, on)
	link(r, 632, 625, 999)
	r.tny.delay[632] = 30 * time.Millisecond
	res, err := r.svc.Runs(context.Background(), "app-1", false)
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if res.Project != "MOB" {
		t.Fatalf("project = %q", res.Project)
	}
	if got := runIDs(res.Runs); !reflect.DeepEqual(got, []domain.TestinyRunID{632, 625, 999}) {
		t.Fatalf("order = %v", got)
	}
	gone := res.Runs[2]
	if gone.FetchError == nil || gone.FetchError.Kind != domain.TestinyErrNotFound || gone.FetchedAt != nil || gone.Cases == nil {
		t.Fatalf("a deleted run = %+v, want an empty view with a not_found error", gone)
	}
	first := res.Runs[0]
	if first.FetchError != nil || first.FetchedAt == nil || !first.FetchedAt.Equal(r.clock.Now()) || first.Plan != nil {
		t.Fatalf("first run = %+v", first)
	}
}

func TestRunsServeFromCacheUntilItExpiresOrIsRefreshed(t *testing.T) {
	r := newRig(t, on)
	link(r, 632)
	ctx := context.Background()
	for range 2 {
		if _, err := r.svc.Runs(ctx, "app-1", false); err != nil {
			t.Fatal(err)
		}
	}
	if n := r.tny.count("run show"); n != 1 {
		t.Fatalf("run show ran %d times within 15 s, want 1", n)
	}
	if _, err := r.svc.Runs(ctx, "app-1", true); err != nil {
		t.Fatal(err)
	}
	if n := r.tny.count("run show"); n != 2 {
		t.Fatalf("refresh did not bypass the cache: run show ran %d times", n)
	}
	r.clock.advance(16 * time.Second)
	if _, err := r.svc.Runs(ctx, "app-1", false); err != nil {
		t.Fatal(err)
	}
	if n := r.tny.count("run show"); n != 3 {
		t.Fatalf("an expired entry was served: run show ran %d times", n)
	}
}

func TestAFailedReadKeepsTheLastGoodData(t *testing.T) {
	r := newRig(t, on)
	link(r, 632)
	ctx := context.Background()
	good, err := r.svc.Runs(ctx, "app-1", false)
	if err != nil {
		t.Fatal(err)
	}
	readAt := *good.Runs[0].FetchedAt

	r.clock.advance(3 * time.Minute)
	r.tny.setFail(fmt.Errorf("%w: Unauthenticated user (AUTH_ACCESS_DENIED)", testinyadapter.ErrAuth))
	stale, err := r.svc.Runs(ctx, "app-1", true)
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	v := stale.Runs[0]
	if v.FetchError == nil || v.FetchError.Kind != domain.TestinyErrAuth || !strings.Contains(v.FetchError.Message, "Unauthenticated user") {
		t.Fatalf("fetchError = %+v, want auth", v.FetchError)
	}
	if v.Title != "MOBILITY-4839 Chat notice disclaimer - iOS" || len(v.Cases) != 2 || v.FetchedAt == nil || !v.FetchedAt.Equal(readAt) {
		t.Fatalf("stale view = %+v, want the last good data as of %v", v, readAt)
	}
}

func TestFetchErrorKinds(t *testing.T) {
	for sentinel, kind := range map[error]domain.TestinyFetchErrorKind{
		testinyadapter.ErrAuth:          domain.TestinyErrAuth,
		testinyadapter.ErrNotFound:      domain.TestinyErrNotFound,
		testinyadapter.ErrUnavailable:   domain.TestinyErrUnavailable,
		testinyadapter.ErrRejected:      domain.TestinyErrRejected,
		testinyadapter.ErrBinaryMissing: domain.TestinyErrBinaryMissing,
		testinyadapter.ErrCLITooOld:     domain.TestinyErrCLITooOld,
	} {
		r := newRig(t, on)
		link(r, 632)
		r.tny.setFail(fmt.Errorf("%w: detail", sentinel))
		res, err := r.svc.Runs(context.Background(), "app-1", false)
		if err != nil {
			t.Fatal(err)
		}
		if fe := res.Runs[0].FetchError; fe == nil || fe.Kind != kind {
			t.Errorf("%v: fetchError = %+v, want %s", sentinel, fe, kind)
		}
	}
}

func TestUnlink(t *testing.T) {
	r := newRig(t, on)
	link(r, 632, 625)
	if err := r.svc.Unlink(context.Background(), "app-1", 632); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Unlink(context.Background(), "app-1", 632); err != nil {
		t.Fatalf("unlinking twice: %v", err)
	}
	if len(r.store.links) != 1 || r.store.links[0].RunID != 625 {
		t.Fatalf("links = %+v", r.store.links)
	}
}

func mkdir(t *testing.T, parts ...string) string {
	t.Helper()
	p := filepath.Join(parts...)
	if err := os.MkdirAll(p, 0o750); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEvidenceDirIsTheRunsFolderInTheQAEvidenceTree(t *testing.T) {
	r := newRig(t, on)
	link(r, 632, 625)
	tree := filepath.Join(r.home, "Desktop", "QA Evidence", "MOBILITY", "2026", "Sprint 2026-20")
	want := mkdir(t, tree, "TP-193 - Chat session logout", "TR-632 - MOBILITY-4839 Chat notice disclaimer - iOS")
	mkdir(t, tree, "TP-193 - Chat session logout", "TR-6320 - another run")
	if err := os.WriteFile(filepath.Join(tree, "TP-193 - Chat session logout", "TR-625 - a file, not a folder"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := r.svc.Runs(context.Background(), "app-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Runs[0].EvidenceDir != want {
		t.Fatalf("evidence dir = %q, want %q", res.Runs[0].EvidenceDir, want)
	}
	if res.Runs[1].EvidenceDir != "" {
		t.Fatalf("evidence dir for a run with no folder = %q, want empty", res.Runs[1].EvidenceDir)
	}
}

func writeScript(t *testing.T, store, rel, body string) {
	t.Helper()
	p := filepath.Join(store, rel)
	mkdir(t, filepath.Dir(p))
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCaseScriptsAreFoundByTheirTestinyHeader(t *testing.T) {
	cfg := on
	r := newRig(t, cfg)
	store := filepath.Join(r.home, "Documents", "Projects", "mobile-ui-scripts")
	cfg.MobileScripts = &domain.MobileScriptsConfig{Product: "nter", Platform: domain.MobilePlatformIOS}
	r.store.projects["app"] = domain.ProjectRecord{ID: "app", Config: cfg}
	cases := filepath.Join(store, "projects", "nter", "cases")
	writeScript(t, cases, "chat/fund_disclaimer.yaml", "name: fund disclaimer\n# testiny: MOB TC-7166\nappId: x\n---\n- runFlow: ../start/home.yaml\n")
	writeScript(t, cases, "chat/bond_and_list.yaml", "# Plays two cases.\n# testiny: mob TC-7167\n#  testiny: MOB TC-3818\n---\n")
	writeScript(t, cases, "chat/a_kern_case.yaml", "# testiny: KERN TC-7167\n---\n")
	writeScript(t, cases, "chat/no_header.yaml", "name: plain\n---\n- tapOn: x\n")
	writeScript(t, cases, "chat/notes.md", "# testiny: MOB TC-7166\n")

	link(r, 632, 625)
	res, err := r.svc.Runs(context.Background(), "app-1", false)
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64]string{}
	for _, v := range res.Runs {
		for _, c := range v.Cases {
			got[c.ID] = c.Script
		}
	}
	want := map[int64]string{
		7166: "projects/nter/cases/chat/fund_disclaimer.yaml",
		7167: "projects/nter/cases/chat/bond_and_list.yaml",
		3818: "projects/nter/cases/chat/bond_and_list.yaml",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scripts = %v, want %v", got, want)
	}
}

func TestCaseScriptsAreRescannedAfterAMinute(t *testing.T) {
	cfg := on
	r := newRig(t, cfg)
	store := filepath.Join(r.home, "scripts")
	cfg.MobileScripts = &domain.MobileScriptsConfig{Product: "nter", Platform: domain.MobilePlatformIOS, Store: store}
	r.store.projects["app"] = domain.ProjectRecord{ID: "app", Config: cfg}
	link(r, 632)
	ctx := context.Background()
	script := func() string {
		res, err := r.svc.Runs(ctx, "app-1", true)
		if err != nil {
			t.Fatal(err)
		}
		return res.Runs[0].Cases[0].Script
	}
	if s := script(); s != "" {
		t.Fatalf("script before any exists = %q", s)
	}
	writeScript(t, store, "projects/nter/cases/chat/fund.yaml", "# testiny: MOB TC-7166\n")
	if s := script(); s != "" {
		t.Fatalf("rescanned within the minute: %q", s)
	}
	r.clock.advance(61 * time.Second)
	if s := script(); s != "projects/nter/cases/chat/fund.yaml" {
		t.Fatalf("script after a minute = %q", s)
	}
}

func TestACachedRunIsNotChangedByTheScriptsOfAnotherView(t *testing.T) {
	cfg := on
	r := newRig(t, cfg)
	store := filepath.Join(r.home, "scripts")
	cfg.MobileScripts = &domain.MobileScriptsConfig{Product: "nter", Platform: domain.MobilePlatformIOS, Store: store}
	r.store.projects["app"] = domain.ProjectRecord{ID: "app", Config: cfg}
	writeScript(t, store, "projects/nter/cases/chat/fund.yaml", "# testiny: MOB TC-7166\n")
	link(r, 632)
	if _, err := r.svc.Runs(context.Background(), "app-1", false); err != nil {
		t.Fatal(err)
	}
	r.store.projects["app"] = domain.ProjectRecord{ID: "app", Config: on}
	res, err := r.svc.Runs(context.Background(), "app-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if s := res.Runs[0].Cases[0].Script; s != "" {
		t.Fatalf("a project without scripts shows %q from a cached view", s)
	}
}
