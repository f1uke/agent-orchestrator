package controllers_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// POST /sessions with startImmediately=false routes to PrepareTodo and returns a
// TODO session carrying the spec (isTodo, prTarget, createdBy, prompt, branch).
func TestSpawnSession_DeferredCreatesTodo(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions",
		`{"projectId":"mer","kind":"worker","harness":"codex","branch":"feature/x","baseBranch":"main-fluke","prTarget":"main-fluke","prompt":"do it","displayName":"todo task","createdBy":"mer-1","startImmediately":false}`)
	if status != http.StatusCreated {
		t.Fatalf("POST deferred session = %d, want 201; body=%s", status, body)
	}
	if !svc.prepared {
		t.Fatal("startImmediately=false did not route to PrepareTodo")
	}
	if svc.preparedCfg.PRTarget != "main-fluke" || svc.preparedCfg.CreatedBy != "mer-1" || svc.preparedCfg.Prompt != "do it" {
		t.Fatalf("prepared cfg missing spec: %#v", svc.preparedCfg)
	}
	var resp struct {
		Session map[string]any `json:"session"`
	}
	mustJSON(t, body, &resp)
	if resp.Session["status"] != "todo" || resp.Session["isTodo"] != true {
		t.Fatalf("session not a todo: %v", resp.Session)
	}
	if resp.Session["prTarget"] != "main-fluke" || resp.Session["createdBy"] != "mer-1" || resp.Session["prompt"] != "do it" {
		t.Fatalf("todo spec not surfaced: %v", resp.Session)
	}
}

// startImmediately absent keeps the current spawn-now behavior (unchanged).
func TestSpawnSession_DefaultStartsImmediately(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	_, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions",
		`{"projectId":"mer","kind":"worker","harness":"codex","prompt":"go"}`)
	if status != http.StatusCreated {
		t.Fatalf("POST = %d, want 201", status)
	}
	if svc.prepared {
		t.Fatal("default spawn wrongly routed to PrepareTodo")
	}
}

func TestStartTodoSession(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/mer-7/start", "")
	if status != http.StatusOK {
		t.Fatalf("POST start = %d, want 200; body=%s", status, body)
	}
	if svc.startedID != "mer-7" {
		t.Fatalf("startedID = %q, want mer-7", svc.startedID)
	}
}

func TestUpdateTodoSpec(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "PATCH", "/api/v1/sessions/mer-7/spec",
		`{"displayName":"renamed","branch":"feature/y","baseBranch":"main-fluke","prTarget":"main-fluke","prompt":"new"}`)
	if status != http.StatusOK {
		t.Fatalf("PATCH spec = %d, want 200; body=%s", status, body)
	}
	if svc.updatedID != "mer-7" {
		t.Fatalf("updatedID = %q, want mer-7", svc.updatedID)
	}
	if svc.updatedPatch.DisplayName == nil || *svc.updatedPatch.DisplayName != "renamed" ||
		svc.updatedPatch.Branch == nil || *svc.updatedPatch.Branch != "feature/y" ||
		svc.updatedPatch.PRTarget == nil || *svc.updatedPatch.PRTarget != "main-fluke" ||
		svc.updatedPatch.Prompt == nil || *svc.updatedPatch.Prompt != "new" {
		t.Fatalf("patch not decoded: %#v", svc.updatedPatch)
	}
}

func TestUpdateTodoSpec_RejectsTooLongName(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	longName, _ := json.Marshal("this-name-is-way-too-long-for-twenty")
	body, status, _ := doRequest(t, srv, "PATCH", "/api/v1/sessions/mer-7/spec",
		`{"displayName":`+string(longName)+`}`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "DISPLAY_NAME_TOO_LONG")
}
