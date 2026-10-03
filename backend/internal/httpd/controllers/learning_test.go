package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/learning"
)

type fakeLearning struct {
	gotRef     domain.HookTranscriptRef
	gotSession domain.SessionID
	refErr     error
	gotLimit   int
	forgetErr  error
}

func (f *fakeLearning) RecordTranscriptRef(_ context.Context, id domain.SessionID, ref domain.HookTranscriptRef) error {
	f.gotSession, f.gotRef = id, ref
	return f.refErr
}

func (f *fakeLearning) Status(context.Context) ([]learning.ProjectStatus, error) {
	return []learning.ProjectStatus{{
		ProjectID: "p", Enabled: true, Transcripts: 2, Excerpts: 5,
		BySourceClass: map[domain.LearnSourceClass]int{domain.LearnSourceTyped: 5},
		LastCaptureAt: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC),
		FailingFiles:  []learning.FailingFile{{Path: "/x.jsonl", Error: "boom"}},
	}}, nil
}

func (f *fakeLearning) Excerpts(_ context.Context, projectID domain.ProjectID, limit int) ([]domain.LearnExcerpt, error) {
	f.gotLimit = limit
	if projectID == "missing" {
		return nil, learning.ErrUnknownProject
	}
	return []domain.LearnExcerpt{{ID: 1, ProjectID: projectID, SessionID: "p-1", SourceClass: domain.LearnSourceTyped, HumanText: "use the script"}}, nil
}

func (f *fakeLearning) Forget(_ context.Context, projectID domain.ProjectID) (int, error) {
	return 3, f.forgetErr
}

func serveLearning(t *testing.T, svc LearningService, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	(&LearningController{Svc: svc}).Register(r)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestTranscriptRef_PassesTheReportThrough(t *testing.T) {
	f := &fakeLearning{}
	w := serveLearning(t, f, http.MethodPost, "/sessions/p-1/transcript-ref", `{"claudeSessionId":"c1","transcriptPath":"/a/b.jsonl","promptSha256":"ab","promptBytes":2}`)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d: %s", w.Code, w.Body)
	}
	if f.gotSession != "p-1" || f.gotRef.TranscriptPath != "/a/b.jsonl" || f.gotRef.PromptSHA256 != "ab" || f.gotRef.PromptBytes != 2 {
		t.Errorf("service got %s %+v", f.gotSession, f.gotRef)
	}
}

func TestTranscriptRef_MapsErrors(t *testing.T) {
	for err, code := range map[error]int{
		learning.ErrInvalidTranscriptPath: http.StatusBadRequest,
		ports.ErrSessionNotFound:          http.StatusNotFound,
	} {
		w := serveLearning(t, &fakeLearning{refErr: err}, http.MethodPost, "/sessions/p-1/transcript-ref", `{"transcriptPath":"/etc/passwd"}`)
		if w.Code != code {
			t.Errorf("%v -> %d, want %d", err, w.Code, code)
		}
	}
	if w := serveLearning(t, &fakeLearning{}, http.MethodPost, "/sessions/p-1/transcript-ref", `{`); w.Code != http.StatusBadRequest {
		t.Errorf("bad json -> %d", w.Code)
	}
}

func TestLearningStatus(t *testing.T) {
	w := serveLearning(t, &fakeLearning{}, http.MethodGet, "/learning/status", "")
	var got LearningStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusOK {
		t.Fatalf("code=%d err=%v body=%s", w.Code, err, w.Body)
	}
	p := got.Projects[0]
	if p.ProjectID != "p" || p.Excerpts != 5 || p.BySourceClass["typed"] != 5 || p.LastCaptureAt == nil || len(p.FailingFiles) != 1 {
		t.Errorf("status = %+v", p)
	}
}

func TestLearningExcerpts(t *testing.T) {
	f := &fakeLearning{}
	w := serveLearning(t, f, http.MethodGet, "/learning/excerpts?project=p&limit=7", "")
	var got ListLearningExcerptsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusOK {
		t.Fatalf("code=%d err=%v", w.Code, err)
	}
	if f.gotLimit != 7 || len(got.Excerpts) != 1 || got.Excerpts[0].HumanText != "use the script" || got.Excerpts[0].Before.Actions == nil {
		t.Errorf("excerpts = %+v (limit %d); an empty action list must encode as [] not null", got.Excerpts, f.gotLimit)
	}
	for path, code := range map[string]int{
		"/learning/excerpts":                   http.StatusBadRequest,
		"/learning/excerpts?project=p&limit=x": http.StatusBadRequest,
		"/learning/excerpts?project=missing":   http.StatusNotFound,
	} {
		if w := serveLearning(t, f, http.MethodGet, path, ""); w.Code != code {
			t.Errorf("%s -> %d, want %d", path, w.Code, code)
		}
	}
}

func TestForgetLearning(t *testing.T) {
	w := serveLearning(t, &fakeLearning{}, http.MethodDelete, "/learning/projects/p", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"deletedTurns":3`) {
		t.Fatalf("code=%d body=%s", w.Code, w.Body)
	}
	if w := serveLearning(t, &fakeLearning{forgetErr: learning.ErrStillLearning}, http.MethodDelete, "/learning/projects/p", ""); w.Code != http.StatusConflict {
		t.Errorf("still learning -> %d, want 409", w.Code)
	}
}
