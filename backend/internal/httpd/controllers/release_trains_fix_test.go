package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
)

const shippedHit = "E1~v/20-2026/di"

func (f *boardFixture) put(spec rt.ResourceSpec, items ...rt.Item) {
	f.t.Helper()
	ctx := context.Background()
	if err := f.store.PlanReleaseResources(ctx, []rt.ResourceSpec{spec}, f.now); err != nil {
		f.t.Fatal(err)
	}
	if err := f.store.WriteReleaseSnapshot(ctx, spec, items, f.now); err != nil {
		f.t.Fatal(err)
	}
}

func encode[T rt.Keyed](t *testing.T, k rt.Kind[T], v T) rt.Item {
	t.Helper()
	it, err := rt.EncodeItem(k, v, nil)
	if err != nil {
		t.Fatal(err)
	}
	return it
}

func (f *boardFixture) seedShipped(jira rt.JiraVersionFact) {
	t := f.t
	start, due := rt.Day{Y: 2026, M: 9, D: 28}, rt.Day{Y: 2026, M: 10, D: 5}
	f.put(rt.SpecFor(rt.KindMilestones, rt.ProjectScope(1001), "di"), encode(t, rt.KindMilestones, rt.MilestoneFact{
		Project: 1001, ID: 10, Title: "Release 3.4.0", Description: "Release Train #20/2026", State: "active", Start: &start, Due: &due}))
	f.put(f.tags, encode(t, rt.KindTags, rt.TagFact{Project: 1001, Name: "3.4.0", CommitOn: due}))
	f.put(rt.SpecFor(rt.KindJiraVersions, "project", ""), encode(t, rt.KindJiraVersions, jira))
}

func unreleased() rt.JiraVersionFact {
	return rt.JiraVersionFact{ID: "5001", Name: "iOS 3.4.0", Description: "Release Train #20/2026", URL: "https://jira.example.com/versions/5001", Approvers: []rt.Approver{}}
}

func decodeAs[T any](t *testing.T, body []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return v
}

func TestFixRoutesPlanConfirmListAndWithdraw(t *testing.T) {
	f := newBoardFixture(t)
	f.seedShipped(unreleased())

	w := f.do(http.MethodPost, "/release-trains/fixes/plan", `{"hitId":"`+shippedHit+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("plan = %d %s", w.Code, w.Body)
	}
	plan := decodeAs[ReleaseTrainFixPlanResponse](t, w.Body.Bytes()).Plan
	if plan.Mode != rt.ModeQueue || plan.Steps[0].Request.URL != "https://jira.example.com/rest/api/3/version/5001" {
		t.Fatalf("plan = %+v", plan)
	}
	if fixes := decodeAs[ReleaseTrainFixesResponse](t, f.do(http.MethodGet, "/release-trains/fixes", "").Body.Bytes()).Fixes; len(fixes) != 0 {
		t.Fatalf("planning stored %d fixes", len(fixes))
	}

	confirm := `{"hitId":"` + shippedHit + `","planFingerprint":"` + plan.Fingerprint + `","via":"cli","session":"s-1"}`
	w = f.do(http.MethodPost, "/release-trains/fixes", confirm)
	if w.Code != http.StatusOK {
		t.Fatalf("confirm = %d %s", w.Code, w.Body)
	}
	fix := decodeAs[ReleaseTrainFixResponse](t, w.Body.Bytes()).Fix
	if fix.State != rt.FixDryRun || fix.Events[0].Actor != "cli:s-1" {
		t.Fatalf("fix = %+v", fix)
	}
	if again := decodeAs[ReleaseTrainFixResponse](t, f.do(http.MethodPost, "/release-trains/fixes", confirm).Body.Bytes()).Fix; again.ID != fix.ID {
		t.Fatalf("second confirm made %s, want %s", again.ID, fix.ID)
	}
	if fixes := decodeAs[ReleaseTrainFixesResponse](t, f.do(http.MethodGet, "/release-trains/fixes", "").Body.Bytes()).Fixes; len(fixes) != 1 {
		t.Fatalf("fixes = %d, want 1", len(fixes))
	}

	w = f.do(http.MethodPost, "/release-trains/fixes/"+fix.ID+"/undo", `{"via":"app"}`)
	if w.Code != http.StatusOK || decodeAs[ReleaseTrainFixResponse](t, w.Body.Bytes()).Fix.State != rt.FixWithdrawn {
		t.Fatalf("undo = %d %s", w.Code, w.Body)
	}
	if w = f.do(http.MethodPost, "/release-trains/fixes/"+fix.ID+"/undo", ""); w.Code != http.StatusConflict || errCode(t, w) != "FIX_STATE_CONFLICT" {
		t.Fatalf("second undo = %d %s", w.Code, w.Body)
	}
	if w = f.do(http.MethodPost, "/release-trains/fixes/fx-none/undo", ""); w.Code != http.StatusNotFound || errCode(t, w) != "RELEASE_FIX_NOT_FOUND" {
		t.Fatalf("undo of no fix = %d %s", w.Code, w.Body)
	}
}

func TestFixConfirmOfAChangedPlanIs409WithTheNewPlan(t *testing.T) {
	f := newBoardFixture(t)
	f.seedShipped(unreleased())
	plan := decodeAs[ReleaseTrainFixPlanResponse](t, f.do(http.MethodPost, "/release-trains/fixes/plan", `{"hitId":"`+shippedHit+`"}`).Body.Bytes()).Plan
	moved := unreleased()
	moved.ReleaseDate = &rt.Day{Y: 2026, M: 10, D: 2}
	f.put(rt.SpecFor(rt.KindJiraVersions, "project", ""), encode(t, rt.KindJiraVersions, moved))

	w := f.do(http.MethodPost, "/release-trains/fixes", `{"hitId":"`+shippedHit+`","planFingerprint":"`+plan.Fingerprint+`"}`)
	if w.Code != http.StatusConflict || errCode(t, w) != "FIX_PLAN_CHANGED" {
		t.Fatalf("confirm = %d %s", w.Code, w.Body)
	}
	var e struct {
		Details struct {
			Plan rt.FixPlan `json:"plan"`
		} `json:"details"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || e.Details.Plan.Fingerprint == plan.Fingerprint || e.Details.Plan.Fingerprint == "" {
		t.Fatalf("409 details = %s; want the new plan", w.Body)
	}
}

func TestFixRoutesRefuseBadInput(t *testing.T) {
	f := newBoardFixture(t)
	f.seedShipped(unreleased())
	for _, tc := range []struct {
		method, path, body string
		code               int
		errCode            string
	}{
		{http.MethodPost, "/release-trains/fixes/plan", `{}`, http.StatusBadRequest, "INVALID_BODY"},
		{http.MethodPost, "/release-trains/fixes/plan", `{"hitId":"C5~v/20-2026/di"}`, http.StatusNotFound, "RELEASE_HIT_NOT_FOUND"},
		{http.MethodPost, "/release-trains/fixes/plan", `{"hitId":"` + shippedHit + `","choice":"jira"}`, http.StatusOK, ""},
		{http.MethodPost, "/release-trains/fixes", `{"hitId":"` + shippedHit + `"}`, http.StatusBadRequest, "INVALID_BODY"},
		{http.MethodPost, "/release-trains/fixes", `{"hitId":"` + shippedHit + `","planFingerprint":"x","extra":1}`, http.StatusBadRequest, "INVALID_BODY"},
	} {
		w := f.do(tc.method, tc.path, tc.body)
		if w.Code != tc.code || (tc.errCode != "" && errCode(t, w) != tc.errCode) {
			t.Errorf("%s %s %s = %d %s; want %d %s", tc.method, tc.path, tc.body, w.Code, w.Body, tc.code, tc.errCode)
		}
	}
	if !strings.Contains(f.do(http.MethodPost, "/release-trains/fixes/plan", `{"hitId":"`+shippedHit+`"}`).Body.String(), `"request"`) {
		t.Fatal("the plan carries no shown request")
	}
}
