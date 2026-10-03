package controllers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/learndecide"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/learning"
)

type fakeDecide struct {
	err     error
	gotTask string
	gotAll  bool
}

func (f *fakeDecide) StartDecide(_ context.Context, _, task string, _ float64) error {
	f.gotTask = task
	return f.err
}
func (f *fakeDecide) DecideProgress() (learndecide.Progress, error) {
	return learndecide.Progress{Proposals: 2}, nil
}
func (f *fakeDecide) Proposals(_ context.Context, _ domain.ProjectID, all bool) ([]domain.LearnProposal, error) {
	f.gotAll = all
	return []domain.LearnProposal{{ID: 1, Title: "t"}}, f.err
}
func (f *fakeDecide) Proposal(context.Context, int64) (domain.LearnProposal, []domain.LearnDraft, error) {
	return domain.LearnProposal{ID: 1}, []domain.LearnDraft{{ID: 9, Quote: "q"}}, f.err
}

func serveDecide(t *testing.T, svc LearningDecideService, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	(&LearningDecideController{Svc: svc}).Register(r)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
	return w
}

func TestLearningDecide_Routes(t *testing.T) {
	f := &fakeDecide{}
	if w := serveDecide(t, f, http.MethodPost, "/learning/decide", `{"task":"solo:x","budgetUsd":1}`); w.Code != http.StatusAccepted || f.gotTask != "solo:x" {
		t.Errorf("start -> %d %q", w.Code, f.gotTask)
	}
	if w := serveDecide(t, f, http.MethodGet, "/learning/proposals?all=true", ""); w.Code != http.StatusOK || !f.gotAll ||
		!strings.Contains(w.Body.String(), `"evidenceIds":[]`) || !strings.Contains(w.Body.String(), `"proposals":2`) {
		t.Errorf("list -> %d %s", w.Code, w.Body)
	}
	if w := serveDecide(t, f, http.MethodGet, "/learning/proposals/1", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"quote":"q"`) {
		t.Errorf("show -> %d %s", w.Code, w.Body)
	}
	if w := serveDecide(t, f, http.MethodGet, "/learning/proposals/x", ""); w.Code != http.StatusBadRequest {
		t.Errorf("bad id -> %d", w.Code)
	}
	for err, code := range map[error]int{
		learning.ErrDecideUnavailable: http.StatusNotImplemented, learning.ErrUnknownProposal: http.StatusNotFound,
		learning.ErrNotLearning: http.StatusConflict, learndecide.ErrBusy: http.StatusConflict, learning.ErrInvalidBudget: http.StatusBadRequest,
	} {
		if w := serveDecide(t, &fakeDecide{err: err}, http.MethodPost, "/learning/decide", `{"budgetUsd":1}`); w.Code != code {
			t.Errorf("%v -> %d, want %d", err, w.Code, code)
		}
	}
}
