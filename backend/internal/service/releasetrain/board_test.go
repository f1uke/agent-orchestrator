package releasetrain

import (
	"context"
	"encoding/json"
	"errors"
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
	now    time.Time
	svc    *BoardService
	builds int
}

func newBoardWorld(t *testing.T) *boardWorld {
	t.Helper()
	dir := t.TempDir()
	w := &boardWorld{t: t, path: filepath.Join(dir, ConfigFileName), now: time.Date(2026, 10, 14, 3, 0, 0, 0, time.UTC)}
	writeConfig(t, w.path, rt.ExampleConfigJSON, w.now)
	st, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	w.store = st
	w.svc = NewBoardService(BoardDeps{Config: NewConfigLoader(w.path), Store: st, Clock: func() time.Time { return w.now }})
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
		Author: "Rae Example", CreatedOn: start, MilestoneID: &ms}))
	w.write(rt.KindMRDetail.Name, rt.MRScope(1001, 301), item(t, rt.KindMRDetail, rt.MRDetailFact{
		Project: 1001, IID: 301, Pipeline: &rt.PipelineFact{ID: 9001, Status: "success"}}))
	w.write(rt.KindJobs.Name, rt.PipelineScope(1001, 9001), item(t, rt.KindJobs, rt.JobsFact{
		Project: 1001, Pipeline: 9001, Jobs: []rt.JobFact{{Name: "semgrep-sast", Status: "success"}}}))
}

func (w *boardWorld) board(ifNoneMatch string) ([]byte, string, error) {
	w.t.Helper()
	return w.svc.Board(context.Background(), ifNoneMatch)
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
	w.now = w.now.Add(32 * time.Second)
	body, e := w.mustBoard()
	if e == etag || w.builds != 2 {
		t.Fatalf("after 61 s with no write: etag same=%v builds=%d", e == etag, w.builds)
	}
	if got := outcome(t, body, "C5", "di"); got != "unknown" {
		t.Fatalf("C5 over a 1 m read 61 s old = %s, want unknown", got)
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
