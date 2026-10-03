package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/rules"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/learnrules"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/learning"
)

type fakeRules struct {
	gotQuery   string
	gotLimit   int
	gotProtect learning.ProtectRequest
	err        error
}

func (f *fakeRules) Rules(_ context.Context, _ domain.ProjectID, q string, limit int) ([]learning.RuleHit, error) {
	f.gotQuery, f.gotLimit = q, limit
	return []learning.RuleHit{{LearnRule: domain.LearnRule{ID: "abc-0", LearnRuleAtom: domain.LearnRuleAtom{Text: "Rule."}, Scope: domain.LearnRuleGlobal, SourceKind: domain.LearnRuleSourceSkill}, Score: 1.5}}, f.err
}
func (f *fakeRules) RuleSources(context.Context, domain.ProjectID) (learning.RulesStatus, error) {
	return learning.RulesStatus{Progress: learnrules.Progress{Sources: 3}}, f.err
}
func (f *fakeRules) RefreshRules(context.Context, float64) error { return f.err }
func (f *fakeRules) ProtectedRules(context.Context, domain.ProjectID) ([]domain.LearnProtectedRule, error) {
	return nil, f.err
}
func (f *fakeRules) Protect(_ context.Context, req learning.ProtectRequest) (domain.LearnProtectedRule, error) {
	f.gotProtect = req
	return domain.LearnProtectedRule{ID: 7, Text: req.Text}, f.err
}
func (f *fakeRules) Unprotect(context.Context, int64) error { return f.err }
func (f *fakeRules) CheckForbidden(context.Context, domain.ProjectID, string) ([]rules.ForbiddenHit, error) {
	return []rules.ForbiddenHit{{RuleID: 1, Pattern: "p", Match: "m"}}, f.err
}

func serveRules(t *testing.T, svc LearningRulesService, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	(&LearningRulesController{Svc: svc}).Register(r)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
	return w
}

func TestLearningRules_Routes(t *testing.T) {
	f := &fakeRules{}
	w := serveRules(t, f, http.MethodGet, "/learning/rules?project=p&q=simulator&limit=5", "")
	var list ListLearningRulesResponse
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list.Rules) != 1 || list.Rules[0].Tags == nil {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	if f.gotQuery != "simulator" || f.gotLimit != 5 {
		t.Errorf("query passed as %q / %d", f.gotQuery, f.gotLimit)
	}
	if w := serveRules(t, f, http.MethodGet, "/learning/rules?limit=99999", ""); w.Code != http.StatusBadRequest {
		t.Errorf("oversized limit -> %d", w.Code)
	}
	if w := serveRules(t, f, http.MethodGet, "/learning/rules/sources", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"sources":3`) {
		t.Errorf("sources -> %d %s", w.Code, w.Body)
	}
	if w := serveRules(t, f, http.MethodPost, "/learning/protected-rules", `{"text":"No em dash.","patterns":["x"]}`); w.Code != http.StatusCreated || f.gotProtect.Text != "No em dash." {
		t.Errorf("protect -> %d %+v", w.Code, f.gotProtect)
	}
	if w := serveRules(t, f, http.MethodDelete, "/learning/protected-rules/abc", ""); w.Code != http.StatusBadRequest {
		t.Errorf("bad id -> %d", w.Code)
	}
	if w := serveRules(t, f, http.MethodPost, "/learning/protected-rules/check", `{"text":"t"}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"match":"m"`) {
		t.Errorf("check -> %d %s", w.Code, w.Body)
	}
}

func TestLearningRules_MapsErrors(t *testing.T) {
	cases := map[error]int{
		learning.ErrRulesUnavailable:     http.StatusNotImplemented,
		learning.ErrUnknownProject:       http.StatusNotFound,
		learning.ErrUnknownRule:          http.StatusNotFound,
		learning.ErrInvalidProtectedRule: http.StatusBadRequest,
		learning.ErrInvalidBudget:        http.StatusBadRequest,
		learnrules.ErrBusy:               http.StatusConflict,
	}
	for err, code := range cases {
		if w := serveRules(t, &fakeRules{err: err}, http.MethodPost, "/learning/rules/refresh", `{"budgetUsd":1}`); w.Code != code {
			t.Errorf("%v -> %d, want %d", err, w.Code, code)
		}
	}
	if w := serveRules(t, nil, http.MethodGet, "/learning/rules", ""); w.Code != http.StatusNotImplemented {
		t.Errorf("no service -> %d", w.Code)
	}
}
