package controllers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/learndecide"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/learning"
)

type fakeDecide struct {
	err        error
	gotTask    string
	gotAll     bool
	gotContent string
	gotRes     domain.LearnResolution
	gotReason  string
	gotUntil   time.Time
	gotBy      domain.LearnActor
	gotConfirm string
	called     string
}

func (f *fakeDecide) ProposalRules(context.Context, domain.LearnProposal) ([]learning.RuleRef, error) {
	return []learning.RuleRef{{ID: "protected-1", Text: "Drive simulators through scripts.", Protected: true}}, nil
}

func (f *fakeDecide) Approve(_ context.Context, id int64, content string, r domain.LearnResolution, by domain.LearnActor) (domain.LearnProposal, error) {
	f.gotContent, f.gotRes, f.gotBy = content, r, by
	return domain.LearnProposal{ID: id, Status: domain.LearnProposalApplied}, f.err
}
func (f *fakeDecide) Reject(_ context.Context, id int64, reason string, by domain.LearnActor) (domain.LearnProposal, error) {
	f.gotReason, f.gotBy = reason, by
	return domain.LearnProposal{ID: id, Status: domain.LearnProposalRejected, RejectReason: reason}, f.err
}
func (f *fakeDecide) Snooze(_ context.Context, id int64, until time.Time, by domain.LearnActor) (domain.LearnProposal, error) {
	f.gotUntil, f.gotBy = until, by
	return domain.LearnProposal{ID: id, Status: domain.LearnProposalPending, SnoozedUntil: until}, f.err
}
func (f *fakeDecide) Unsnooze(_ context.Context, id int64, by domain.LearnActor) (domain.LearnProposal, error) {
	f.called, f.gotBy = "unsnooze", by
	return domain.LearnProposal{ID: id, Status: domain.LearnProposalPending}, f.err
}
func (f *fakeDecide) Reopen(_ context.Context, id int64, by domain.LearnActor) (domain.LearnProposal, error) {
	f.called, f.gotBy = "reopen", by
	return domain.LearnProposal{ID: id, Status: domain.LearnProposalPending}, f.err
}
func (f *fakeDecide) Undo(_ context.Context, id int64, confirm string, by domain.LearnActor) (domain.LearnProposal, error) {
	f.called, f.gotConfirm, f.gotBy = "undo", confirm, by
	return domain.LearnProposal{ID: id, Status: domain.LearnProposalPending}, f.err
}
func (f *fakeDecide) EditApplied(_ context.Context, id int64, content, confirm string, by domain.LearnActor) (domain.LearnProposal, error) {
	f.called, f.gotContent, f.gotConfirm, f.gotBy = "edit", content, confirm, by
	return domain.LearnProposal{ID: id, Status: domain.LearnProposalApplied, NewContent: content}, f.err
}
func (f *fakeDecide) Written(context.Context, domain.LearnProposal) (learning.Written, bool, error) {
	return learning.Written{Path: "/m/feedback_x.md", Exists: true, Content: "now\n", Changed: true, Diff: "--- a/m\n", Token: "tok"}, true, nil
}
func (f *fakeDecide) History(context.Context, int64) ([]domain.LearnProposalEvent, error) {
	return []domain.LearnProposalEvent{{Kind: domain.LearnEventSnoozed, Status: domain.LearnProposalPending,
		SnoozedUntil: time.Date(2027, 1, 2, 0, 0, 0, 0, time.UTC), Actor: domain.LearnActor{Via: domain.LearnViaApp}}}, nil
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
	if w := serveDecide(t, f, http.MethodGet, "/learning/proposals/1", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"quote":"q"`) || !strings.Contains(w.Body.String(), `"protected":true`) ||
		!strings.Contains(w.Body.String(), `"token":"tok"`) || !strings.Contains(w.Body.String(), `"changed":true`) ||
		!strings.Contains(w.Body.String(), `"kind":"snoozed"`) || !strings.Contains(w.Body.String(), `"via":"app"`) {
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

func TestLearningDecide_Decisions(t *testing.T) {
	f := &fakeDecide{}
	if w := serveDecide(t, f, http.MethodPost, "/learning/proposals/3/approve", `{"content":"edited","resolution":"words_win"}`); w.Code != http.StatusOK ||
		f.gotContent != "edited" || f.gotRes != domain.LearnWordsWin || !strings.Contains(w.Body.String(), `"status":"applied"`) {
		t.Errorf("approve -> %d %s", w.Code, w.Body)
	}
	if w := serveDecide(t, f, http.MethodPost, "/learning/proposals/3/reject", `{"reason":"one-off","via":"cli","session":"ao-7"}`); w.Code != http.StatusOK || f.gotReason != "one-off" ||
		f.gotBy != (domain.LearnActor{Via: domain.LearnViaCLI, SessionID: "ao-7"}) ||
		!strings.Contains(w.Body.String(), `"rejectReason":"one-off"`) {
		t.Errorf("reject -> %d %s", w.Code, w.Body)
	}
	if w := serveDecide(t, f, http.MethodPost, "/learning/proposals/3/snooze", `{"until":"2026-10-11T00:00:00Z"}`); w.Code != http.StatusOK ||
		f.gotUntil.Day() != 11 || !strings.Contains(w.Body.String(), `"snoozedUntil":"2026-10-11T00:00:00Z"`) {
		t.Errorf("snooze -> %d %s", w.Code, w.Body)
	}
	for err, code := range map[error]int{
		learning.ErrProposalStale: http.StatusConflict, learning.ErrProposalNotPending: http.StatusConflict,
		learning.ErrInvalidDecision: http.StatusBadRequest, learning.ErrUnknownProposal: http.StatusNotFound,
	} {
		if w := serveDecide(t, &fakeDecide{err: err}, http.MethodPost, "/learning/proposals/3/approve", `{}`); w.Code != code {
			t.Errorf("%v -> %d, want %d", err, w.Code, code)
		}
	}
}

func TestLearningDecide_Redecisions(t *testing.T) {
	f := &fakeDecide{}
	for path, want := range map[string]string{"unsnooze": "unsnooze", "reopen": "reopen"} {
		// Nothing to say: no body at all is fine.
		if w := serveDecide(t, f, http.MethodPost, "/learning/proposals/3/"+path, ""); w.Code != http.StatusOK || f.called != want {
			t.Errorf("%s -> %d %s (%s)", path, w.Code, w.Body, f.called)
		}
	}
	if w := serveDecide(t, f, http.MethodPost, "/learning/proposals/3/undo", `{"confirmToken":"tok","via":"app"}`); w.Code != http.StatusOK ||
		f.called != "undo" || f.gotConfirm != "tok" || f.gotBy.Via != domain.LearnViaApp {
		t.Errorf("undo -> %d %s", w.Code, w.Body)
	}
	if w := serveDecide(t, f, http.MethodPost, "/learning/proposals/3/edit", `{"content":"mine","confirmToken":"tok"}`); w.Code != http.StatusOK ||
		f.called != "edit" || f.gotContent != "mine" || f.gotConfirm != "tok" {
		t.Errorf("edit -> %d %s", w.Code, w.Body)
	}
	changed := &fakeDecide{err: &learning.ChangedError{Path: "/m/feedback_x.md", Diff: "--- a/m/feedback_x.md\n", Token: "abc"}}
	if w := serveDecide(t, changed, http.MethodPost, "/learning/proposals/3/undo", `{}`); w.Code != http.StatusConflict ||
		!strings.Contains(w.Body.String(), `"code":"PROPOSAL_CHANGED"`) || !strings.Contains(w.Body.String(), `"token":"abc"`) ||
		!strings.Contains(w.Body.String(), `"diff":"--- a/m/feedback_x.md\n"`) {
		t.Errorf("changed -> %d %s", w.Code, w.Body)
	}
	for err, code := range map[error]string{
		learning.ErrProposalWrongState: "PROPOSAL_WRONG_STATE", domain.ErrLearnTargetPending: "TARGET_PENDING",
		learning.ErrMemoryExists: "MEMORY_EXISTS", learning.ErrNothingToRestore: "NOTHING_TO_RESTORE",
	} {
		if w := serveDecide(t, &fakeDecide{err: err}, http.MethodPost, "/learning/proposals/3/reopen", `{}`); w.Code != http.StatusConflict ||
			!strings.Contains(w.Body.String(), `"code":"`+code+`"`) {
			t.Errorf("%v -> %d %s, want %s", err, w.Code, w.Body, code)
		}
	}
}
