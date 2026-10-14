package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
)

const lateHit = "B1~v/21-2026/da"

func (f *boardFixture) seedLateAndroid() {
	f.t.Helper()
	ctx := context.Background()
	start, due := rt.Day{Y: 2026, M: 10, D: 9}, rt.Day{Y: 2026, M: 10, D: 16}
	ms := rt.SpecFor(rt.KindMilestones, rt.ProjectScope(1002), "da")
	mrs := rt.SpecFor(rt.KindMRs, rt.ProjectScope(1002), "da")
	body, err := rt.EncodeItem(rt.KindMilestones, rt.MilestoneFact{Project: 1002, ID: 21, Title: "Release 3.5.0",
		Description: "Release Train #21/2026", State: "active", Start: &start, Due: &due}, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.store.PlanReleaseResources(ctx, []rt.ResourceSpec{ms, mrs}, f.now); err != nil {
		f.t.Fatal(err)
	}
	if err := f.store.WriteReleaseSnapshot(ctx, ms, []rt.Item{body}, f.now); err != nil {
		f.t.Fatal(err)
	}
	if err := f.store.WriteReleaseSnapshot(ctx, mrs, nil, f.now); err != nil {
		f.t.Fatal(err)
	}
}

func (f *boardFixture) do(method, target, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	r := chi.NewRouter()
	(&ReleaseTrainsController{Svc: f.svc}).Register(r)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, target, strings.NewReader(body)))
	return w
}

func hidePath(id string) string { return "/release-trains/hides/" + url.PathEscape(id) }

func errCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var e struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &e)
	return e.Code
}

func TestReleaseTrainRuleRoute(t *testing.T) {
	f := newBoardFixture(t)
	w := f.do(http.MethodPut, "/release-trains/rules/C2", `{"enabled":true}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"id":"C2"`) || !strings.Contains(w.Body.String(), `"on":true`) {
		t.Fatalf("enable C2 = %d %s", w.Code, w.Body)
	}
	if w := f.do(http.MethodPut, "/release-trains/rules/C2", `{"enabled":null}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"on":false`) {
		t.Fatalf("reset C2 = %d %s", w.Code, w.Body)
	}
	if w := f.do(http.MethodPut, "/release-trains/rules/Z9", `{"enabled":true}`); w.Code != http.StatusNotFound || errCode(t, w) != "RELEASE_RULE_NOT_FOUND" {
		t.Fatalf("unknown rule = %d %s", w.Code, w.Body)
	}
	if w := f.do(http.MethodPut, "/release-trains/rules/C2", `{"enabled":"yes"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad body = %d %s", w.Code, w.Body)
	}
}

func TestReleaseTrainHideRoutes(t *testing.T) {
	f := newBoardFixture(t)
	f.seedLateAndroid()
	etag := f.get("").Header().Get("ETag")

	w := f.do(http.MethodPut, hidePath(lateHit), `{"reason":"release MR tomorrow"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"severity":"risk"`) || !strings.Contains(w.Body.String(), `"hitId":"B1~v/21-2026/da"`) {
		t.Fatalf("hide = %d %s", w.Code, w.Body)
	}
	board := f.get(etag)
	if board.Code != http.StatusOK || !strings.Contains(board.Body.String(), `"bucket":"hidden"`) {
		t.Fatalf("board after hide = %d, want a new body with the hit hidden", board.Code)
	}

	if w := f.do(http.MethodPut, hidePath("B1~v/99-2026/da"), ``); w.Code != http.StatusNotFound || errCode(t, w) != "RELEASE_HIT_NOT_FOUND" {
		t.Fatalf("hide of a hit not on the board = %d %s", w.Code, w.Body)
	}
	if w := f.do(http.MethodPut, "/release-trains/hides/nothing", ``); w.Code != http.StatusBadRequest {
		t.Fatalf("hide of a malformed id = %d %s", w.Code, w.Body)
	}

	if w := f.do(http.MethodDelete, hidePath(lateHit), ``); w.Code != http.StatusNoContent {
		t.Fatalf("unhide = %d %s", w.Code, w.Body)
	}
	if w := f.do(http.MethodDelete, hidePath(lateHit), ``); w.Code != http.StatusNotFound || errCode(t, w) != "RELEASE_HIDE_NOT_FOUND" {
		t.Fatalf("second unhide = %d %s", w.Code, w.Body)
	}
}

func TestReleaseTrainUnhideStaleRoute(t *testing.T) {
	f := newBoardFixture(t)
	f.seedLateAndroid()
	if err := f.store.HideReleaseHit(context.Background(), "X1~v/21-2026/di", rt.Hide{Fingerprint: "x", Severity: rt.SevBlock, HiddenAt: f.now}); err != nil {
		t.Fatal(err)
	}
	if w := f.do(http.MethodDelete, "/release-trains/hides", ``); w.Code != http.StatusBadRequest || errCode(t, w) != "STALE_REQUIRED" {
		t.Fatalf("bulk delete without stale = %d %s", w.Code, w.Body)
	}
	w := f.do(http.MethodDelete, "/release-trains/hides?stale=true", ``)
	if w.Code != http.StatusOK || w.Body.String() != `{"removed":["X1~v/21-2026/di"]}`+"\n" {
		t.Fatalf("unhide stale = %d %q", w.Code, w.Body)
	}
	if w := f.do(http.MethodDelete, "/release-trains/hides?stale=true", ``); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"removed":[]`) {
		t.Fatalf("unhide stale again = %d %s", w.Code, w.Body)
	}
}

func TestReleaseTrainPlanRoutes(t *testing.T) {
	f := newBoardFixture(t)
	if w := f.do(http.MethodGet, "/release-trains/llb-plans", ``); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"plans":[]`) {
		t.Fatalf("no plans = %d %s", w.Code, w.Body)
	}
	plan := `{"via":"cli","name":"Demo cards","targetKind":"train","targetTrain":"22-2026","branches":[{"app":"di","branch":"feature/demo-llb"}],"accepted":[{"mrRef":"di!52","reason":"reviewed"}]}`
	w := f.do(http.MethodPut, "/release-trains/llb-plans/demo-llb", plan)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"version":1`) || !strings.Contains(w.Body.String(), `"updatedBy":"cli"`) ||
		!strings.Contains(w.Body.String(), `"targetTrain":"22-2026"`) {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	if w := f.do(http.MethodPut, "/release-trains/llb-plans/demo-llb", plan); w.Code != http.StatusConflict || errCode(t, w) != "PLAN_VERSION_CONFLICT" ||
		!strings.Contains(w.Body.String(), `"currentVersion":1`) {
		t.Fatalf("stale ifVersion = %d %s", w.Code, w.Body)
	}
	edit := strings.Replace(plan, `"via":"cli"`, `"via":"cli","session":"demo-7","ifVersion":1`, 1)
	if w := f.do(http.MethodPut, "/release-trains/llb-plans/demo-llb", edit); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"version":2`) ||
		!strings.Contains(w.Body.String(), `"updatedBy":"cli:demo-7"`) {
		t.Fatalf("edit = %d %s", w.Code, w.Body)
	}
	other := `{"name":"Other","targetKind":"dropped","branches":[{"app":"di","branch":"feature/demo-llb"}]}`
	if w := f.do(http.MethodPut, "/release-trains/llb-plans/other-llb", other); w.Code != http.StatusConflict || errCode(t, w) != "PLAN_BRANCH_TAKEN" ||
		!strings.Contains(w.Body.String(), `"planId":"demo-llb"`) {
		t.Fatalf("held branch = %d %s", w.Code, w.Body)
	}
	for _, bad := range []string{
		`{"name":"x","targetKind":"train"}`,
		`{"name":"x","targetKind":"train","targetTrain":"#22"}`,
		`{"name":"x","targetKind":"someday"}`,
		`{"name":"x","targetKind":"dropped","extra":1}`,
	} {
		if w := f.do(http.MethodPut, "/release-trains/llb-plans/bad", bad); w.Code != http.StatusBadRequest {
			t.Fatalf("bad plan %s = %d %s", bad, w.Code, w.Body)
		}
	}
	w = f.do(http.MethodGet, "/release-trains/llb-plans", ``)
	var got ReleaseTrainLlbPlansResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || len(got.Plans) != 1 || got.Plans[0].Version != 2 || len(got.Plans[0].Accepted) != 1 {
		t.Fatalf("plans = %s (%v)", w.Body, err)
	}
}
