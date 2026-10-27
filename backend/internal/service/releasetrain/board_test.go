package releasetrain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

type boardWorld struct {
	t      *testing.T
	store  *sqlite.Store
	path   string
	people string
	now    time.Time
	svc    *BoardService
	builds int
}

func newBoardWorld(t *testing.T) *boardWorld {
	t.Helper()
	dir := t.TempDir()
	w := &boardWorld{t: t, path: filepath.Join(dir, ConfigFileName), people: filepath.Join(dir, PeopleFileName), now: time.Date(2026, 10, 14, 3, 0, 0, 0, time.UTC)}
	writeConfig(t, w.path, rt.ExampleConfigJSON, w.now)
	st, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	w.store = st
	w.svc = NewBoardService(BoardDeps{Config: NewConfigLoader(w.path), People: NewPeopleLoader(w.people), Store: st, Clock: func() time.Time { return w.now }})
	w.svc.onBuild = func() { w.builds++ }
	return w
}

func (w *boardWorld) write(k rt.KindName, scope rt.Scope, items ...rt.Item) rt.ResourceSpec {
	w.t.Helper()
	info, _ := rt.LookupKind(k)
	spec := rt.ResourceSpec{Kind: k, Scope: scope, App: "di", Mode: info.Mode, Version: info.Version, Budget: info.Budget}
	ctx := context.Background()
	if err := w.store.PlanReleaseResources(ctx, []rt.ResourceSpec{spec}, w.now); err != nil {
		w.t.Fatal(err)
	}
	if err := w.store.WriteReleaseSnapshot(ctx, spec, items, w.now); err != nil {
		w.t.Fatal(err)
	}
	return spec
}

func item[T rt.Keyed](t *testing.T, k rt.Kind[T], v T) rt.Item {
	t.Helper()
	it, err := rt.EncodeItem(k, v, nil)
	if err != nil {
		t.Fatal(err)
	}
	return it
}

func (w *boardWorld) seed() {
	t := w.t
	start, due := rt.Day{Y: 2026, M: 10, D: 12}, rt.Day{Y: 2026, M: 10, D: 19}
	ms := 11
	w.write(rt.KindMilestones.Name, rt.ProjectScope(1001), item(t, rt.KindMilestones, rt.MilestoneFact{
		Project: 1001, ID: ms, Title: "Release 3.5.0", Description: "Release Train #21/2026", State: "active", Start: &start, Due: &due}))
	w.write(rt.KindMRs.Name, rt.ProjectScope(1001), item(t, rt.KindMRs, rt.MRFact{
		Project: 1001, IID: 301, Title: "Release 3.5.0", SourceBranch: "release/3.5.0", TargetBranch: "main", State: rt.MROpened,
		Author: "Rae Example", AuthorUsername: "rae", CreatedOn: start, MilestoneID: &ms}))
	w.write(rt.KindMRDetail.Name, rt.MRScope(1001, 301), item(t, rt.KindMRDetail, rt.MRDetailFact{
		Project: 1001, IID: 301, Pipeline: &rt.PipelineFact{ID: 9001, Status: "success"}}))
	w.write(rt.KindJobs.Name, rt.PipelineScope(1001, 9001), item(t, rt.KindJobs, rt.JobsFact{
		Project: 1001, Pipeline: 9001, Jobs: []rt.JobFact{{Name: "semgrep-sast", Status: "success"}}}))
}

func (w *boardWorld) board(ifNoneMatch string) ([]byte, string, error) {
	w.t.Helper()
	return w.svc.Board(context.Background(), ifNoneMatch, "")
}

type viewed struct {
	People PeopleReport `json:"people"`
	Board  struct {
		Viewer struct {
			Kind   string `json:"kind"`
			Reason string `json:"reason"`
			Person struct {
				ID    string `json:"id"`
				Match string `json:"match"`
			} `json:"person"`
		} `json:"viewer"`
	} `json:"board"`
}

func (w *boardWorld) viewedBy(person string) (viewed, string) {
	w.t.Helper()
	body, etag, err := w.svc.Board(context.Background(), "", person)
	if err != nil {
		w.t.Fatal(err)
	}
	var v viewed
	if err := json.Unmarshal(body, &v); err != nil {
		w.t.Fatal(err)
	}
	return v, etag
}

func (w *boardWorld) mustBoard() ([]byte, string) {
	w.t.Helper()
	body, etag, err := w.board("")
	if err != nil {
		w.t.Fatal(err)
	}
	return body, etag
}

func outcome(t *testing.T, body []byte, rule, app string) string {
	t.Helper()
	var probe struct {
		Board struct {
			Alerts struct {
				Rules []struct {
					Rule    string `json:"rule"`
					App     string `json:"app"`
					Outcome struct {
						Kind string `json:"kind"`
					} `json:"outcome"`
				} `json:"rules"`
			} `json:"alerts"`
		} `json:"board"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		t.Fatal(err)
	}
	for _, o := range probe.Board.Alerts.Rules {
		if o.Rule == rule && o.App == app {
			return o.Outcome.Kind
		}
	}
	return ""
}

func TestBoardServesTheSameBytesAndETagUntilSomethingChanges(t *testing.T) {
	w := newBoardWorld(t)
	w.seed()
	body, etag := w.mustBoard()
	if etag == "" || !strings.HasPrefix(etag, `"`) || w.builds != 1 {
		t.Fatalf("etag %q after %d builds", etag, w.builds)
	}
	w.now = w.now.Add(10 * time.Second)
	again, etag2 := w.mustBoard()
	if etag2 != etag || string(again) != string(body) || w.builds != 1 {
		t.Fatalf("second read rebuilt (%d builds) or changed: %q vs %q", w.builds, etag2, etag)
	}
	if _, _, err := w.board(etag); !errors.Is(err, ErrNotModified) {
		t.Fatalf("If-None-Match %s gave err %v, want ErrNotModified", etag, err)
	}
}

func TestBoardRecomputesOnAnItemWriteAndOnAStatusOnlyWrite(t *testing.T) {
	w := newBoardWorld(t)
	w.seed()
	_, etag := w.mustBoard()

	jobs := w.write(rt.KindJobs.Name, rt.PipelineScope(1001, 9001), item(t, rt.KindJobs, rt.JobsFact{
		Project: 1001, Pipeline: 9001, Jobs: []rt.JobFact{{Name: "semgrep-sast", Status: "success"}, {Name: "build_test", Status: "failed"}}}))
	body, afterWrite := w.mustBoard()
	if afterWrite == etag || w.builds != 2 || outcome(t, body, "C1", "di") != "fired" {
		t.Fatalf("after a job failed: etag same=%v builds=%d C1=%s", afterWrite == etag, w.builds, outcome(t, body, "C1", "di"))
	}

	err := w.store.MarkReleaseFailure(context.Background(), jobs, rt.Failure{At: w.now, Failures: 1, Class: rt.ErrUnavailable,
		Message: "503", NextAttemptAt: w.now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	_, afterFail := w.mustBoard()
	if afterFail == afterWrite || w.builds != 3 {
		t.Fatalf("a status-only write kept the ETag (builds %d)", w.builds)
	}
}

func TestBoardRecomputesAfterThirtySecondsSoABudgetCrossingTurnsCleanToUnknown(t *testing.T) {
	w := newBoardWorld(t)
	w.seed()
	body, etag := w.mustBoard()
	if got := outcome(t, body, "C5", "di"); got != "clean" {
		t.Fatalf("C5 with fresh mr_detail = %s, want clean", got)
	}

	w.now = w.now.Add(29 * time.Second)
	if _, e := w.mustBoard(); e != etag || w.builds != 1 {
		t.Fatalf("rebuilt before 30 s (builds %d)", w.builds)
	}
	w.now = w.now.Add(92 * time.Second)
	body, e := w.mustBoard()
	if e == etag || w.builds != 2 {
		t.Fatalf("after 121 s with no write: etag same=%v builds=%d", e == etag, w.builds)
	}
	if got := outcome(t, body, "C5", "di"); got != "unknown" {
		t.Fatalf("C5 over a 2 m read 121 s old = %s, want unknown", got)
	}
}

func TestBoardRecomputesWhenTheConfigChanges(t *testing.T) {
	w := newBoardWorld(t)
	w.seed()
	_, etag := w.mustBoard()
	writeConfig(t, w.path, strings.Replace(rt.ExampleConfigJSON, `"name": "Demo iOS"`, `"name": "Demo iPhone"`, 1), w.now.Add(time.Second))
	body, e := w.mustBoard()
	if e == etag || !strings.Contains(string(body), "Demo iPhone") {
		t.Fatalf("config edit not picked up: etag same=%v", e == etag)
	}
}

func TestBoardWithoutConfigSaysHowToCreateOne(t *testing.T) {
	w := newBoardWorld(t)
	if err := os.Remove(w.path); err != nil {
		t.Fatal(err)
	}
	body, etag := w.mustBoard()
	var r BoardResponse
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatal(err)
	}
	if r.Config.State != ConfigAbsent || !strings.Contains(r.Config.Message, "ao train config init") || r.Board != nil || etag == "" {
		t.Fatalf("absent config response = %s", body)
	}
}

func TestBoardWithAnInvalidEditKeepsServingTheLastGoodConfig(t *testing.T) {
	w := newBoardWorld(t)
	w.seed()
	w.mustBoard()
	writeConfig(t, w.path, "{not json", w.now.Add(time.Second))
	body, _ := w.mustBoard()
	var r struct {
		Config ConfigReport `json:"config"`
		Board  *struct {
			Trains []json.RawMessage `json:"trains"`
		} `json:"board"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatal(err)
	}
	if r.Config.State != ConfigInvalid || !strings.Contains(r.Config.Message, "JSON") || r.Board == nil || len(r.Board.Trains) != 1 {
		t.Fatalf("invalid edit response = %s", body)
	}
}

func TestBoardOfAnInvalidFirstConfigHasNoBoard(t *testing.T) {
	w := newBoardWorld(t)
	writeConfig(t, w.path, `{"timezone":"Mars/Olympus"}`, w.now.Add(time.Second))
	body, _ := w.mustBoard()
	var r BoardResponse
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatal(err)
	}
	if r.Config.State != ConfigInvalid || r.Board != nil {
		t.Fatalf("invalid first config = %s", body)
	}
}

func TestBoardKeepsEachPersonsViewSeparateAndCached(t *testing.T) {
	w := newBoardWorld(t)
	w.seed()
	own, ownTag := w.viewedBy("")
	if own.Board.Viewer.Kind != "unknown" {
		t.Fatalf("own viewer = %+v, want unknown while no identity is read", own.Board.Viewer)
	}
	rae, raeTag := w.viewedBy("rae")
	if rae.Board.Viewer.Kind != "person" || rae.Board.Viewer.Person.ID != "rae" || raeTag == ownTag || w.builds != 2 {
		t.Fatalf("rae viewer = %+v, etag same=%v, builds %d", rae.Board.Viewer, raeTag == ownTag, w.builds)
	}
	if _, tag := w.viewedBy(""); tag != ownTag || w.builds != 2 {
		t.Fatalf("asking for rae evicted the monitor's own view: etag same=%v, builds %d", tag == ownTag, w.builds)
	}
	if _, tag := w.viewedBy("rae"); tag != raeTag || w.builds != 2 {
		t.Fatalf("rae's view was rebuilt: builds %d", w.builds)
	}
	if _, _, err := w.svc.Board(context.Background(), raeTag, "rae"); !errors.Is(err, ErrNotModified) {
		t.Fatalf("If-None-Match %s for rae gave %v, want ErrNotModified", raeTag, err)
	}
	if _, _, err := w.svc.Board(context.Background(), raeTag, ""); errors.Is(err, ErrNotModified) {
		t.Fatal("rae's ETag answered 304 for the monitor's own view")
	}
}

func TestBoardForgetsOldViewsOnceTooManyPeopleAreAsked(t *testing.T) {
	w := newBoardWorld(t)
	w.seed()
	for i := range MaxCachedBoards + 5 {
		w.viewedBy(fmt.Sprintf("nobody-%d", i))
	}
	if n := len(w.svc.cached); n > MaxCachedBoards {
		t.Fatalf("%d views cached, want at most %d", n, MaxCachedBoards)
	}
	if v, _ := w.viewedBy("rae"); v.Board.Viewer.Person.ID != "rae" {
		t.Fatalf("a view built after the cache started over is wrong: %+v", v.Board.Viewer)
	}
}

func TestBoardRebuildsEveryViewWhenThePeopleFileChanges(t *testing.T) {
	w := newBoardWorld(t)
	w.seed()
	before, tag := w.viewedBy("rae")
	if before.People.State != PeopleAbsent || before.Board.Viewer.Person.Match != "gitlab_only" || before.People.Path != w.people {
		t.Fatalf("before the file: people %+v, viewer %+v", before.People, before.Board.Viewer)
	}
	builds := w.builds

	writeConfig(t, w.people, `{"people":[{"jiraAccountId":"acc-rae","gitlabUsername":"rae","name":"Rae Example"}]}`, w.now.Add(time.Second))
	after, tag2 := w.viewedBy("rae")
	if after.People.State != PeopleOK || after.Board.Viewer.Person.Match != "linked" || tag2 == tag || w.builds != builds+1 {
		t.Fatalf("after the link: people %+v, viewer %+v, etag same=%v, builds %d (was %d)", after.People, after.Board.Viewer, tag2 == tag, w.builds, builds)
	}

	writeConfig(t, w.people, `{"people": [{"jiraAccountId": "acc-rae"}]}`, w.now.Add(2*time.Second))
	broken, _ := w.viewedBy("rae")
	if broken.People.State != PeopleInvalid || !strings.Contains(broken.People.Message, "people[0]") || broken.Board.Viewer.Person.Match != "gitlab_only" {
		t.Fatalf("an invalid file: people %+v, viewer %+v; want the report and no link", broken.People, broken.Board.Viewer)
	}
}
