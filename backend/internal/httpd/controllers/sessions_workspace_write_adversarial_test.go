package controllers_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// The write route's confinement lives in the service. That is only a boundary if
// nothing between the wire and the service rewrites the path first: a layer that
// URL-decodes, cleans or unescapes it would hand the service a DIFFERENT path
// than the one the service was asked to judge, and every traversal test in the
// service package would be testing a string the client can no longer send.
//
// So: whatever bytes arrive in the JSON body must reach the service unchanged.
func TestSessionsAPI_WriteWorkspaceFilePassesThePathVerbatim(t *testing.T) {
	for _, tc := range []struct{ name, path string }{
		{"parent_segment", "../secret.txt"},
		{"percent_encoded_parent", "%2e%2e/secret.txt"},
		{"percent_encoded_slash", "..%2fsecret.txt"},
		{"absolute", "/etc/passwd"},
		{"tilde", "~/.ssh/authorized_keys"},
		{"nul_byte", "pkg/a.go\x00/../../secret.txt"},
		{"newline", "pkg/a.go\n../../secret.txt"},
		{"backslash", `..\secret.txt`},
		{"double_slash", "//secret.txt"},
		{"unicode", "pkg/héllo 🎉.txt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newFakeSessionService()
			srv := newSessionTestServer(t, svc)

			// json.Marshal so control bytes survive the wire exactly.
			payload, err := json.Marshal(map[string]string{
				"path": tc.path, "content": "x\n", "baseHash": "sha256:old",
			})
			if err != nil {
				t.Fatal(err)
			}
			body, status, _ := doRequest(t, srv, "PUT", "/api/v1/sessions/ao-1/workspace/file", string(payload))
			if status != http.StatusOK {
				t.Fatalf("status %d: %s", status, body)
			}
			if svc.workspaceWrite.Path != tc.path {
				t.Fatalf("service saw path %q, the client sent %q - a layer rewrote it", svc.workspaceWrite.Path, tc.path)
			}
		})
	}
}

// Content is written verbatim, so the transport must not touch it either: no EOL
// translation, no trimming, no unicode normalisation. A newline the wire adds or
// drops is a diff the user never made, in a worktree an agent is committing from.
func TestSessionsAPI_WriteWorkspaceFilePassesTheContentVerbatim(t *testing.T) {
	for _, tc := range []struct{ name, content string }{
		{"crlf", "alpha\r\nbeta\r\n"},
		{"lone_cr", "alpha\rbeta\n"},
		{"no_trailing_newline", "alpha\nbeta"},
		{"empty", ""},
		{"trailing_whitespace", "alpha   \n\t\n"},
		{"unicode", "héllo wörld 🎉 日本語\n"},
		{"blank_run", "a\n\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newFakeSessionService()
			srv := newSessionTestServer(t, svc)

			payload, err := json.Marshal(map[string]string{
				"path": "a.go", "content": tc.content, "baseHash": "sha256:old",
			})
			if err != nil {
				t.Fatal(err)
			}
			body, status, _ := doRequest(t, srv, "PUT", "/api/v1/sessions/ao-1/workspace/file", string(payload))
			if status != http.StatusOK {
				t.Fatalf("status %d: %s", status, body)
			}
			if svc.workspaceWrite.Content != tc.content {
				t.Fatalf("service saw content %q, the client sent %q", svc.workspaceWrite.Content, tc.content)
			}
		})
	}
}

// An explicit `"content": null` is the OTHER shape a JavaScript caller produces.
// An absent key comes from stringifying an `undefined`; a null comes from a state
// value initialised as null, which is an ordinary way to spell "the editor has
// not loaded a model yet" - and JSON.stringify keeps that key rather than
// dropping it. Both mean the same thing (the caller has no content to save) and
// both must be refused, or the pointer guard only covers half the bug it was
// added for.
func TestSessionsAPI_WriteWorkspaceFileRejectsExplicitNullContent(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "PUT", "/api/v1/sessions/ao-1/workspace/file",
		`{"path":"a.go","content":null,"baseHash":"sha256:old"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("a null content must be refused, got %d: %s", status, body)
	}
	var env map[string]any
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if code := errorCode(env); code != "WORKSPACE_FILE_CONTENT_REQUIRED" {
		t.Fatalf("code = %q: %s", code, body)
	}
	// The refusal has to happen BEFORE the service: a null that reaches it
	// arrives as "" and empties the user's file.
	if svc.workspaceWrite.Path != "" {
		t.Fatalf("the service was reached with %+v; the guard let a null through", svc.workspaceWrite)
	}
}

// A conflict is only resolvable if the caller gets back what is on disk NOW.
// Those details are the whole difference between "your save was refused" and a
// UI that can offer to reload or diff, so they must survive serialisation with
// their types intact - a size that arrives as a string is a size the client
// cannot compare.
func TestSessionsAPI_WriteWorkspaceFileConflictDetailsSurviveSerialisation(t *testing.T) {
	svc := newFakeSessionService()
	svc.workspaceWriteErr = conflictWithDetails()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "PUT", "/api/v1/sessions/ao-1/workspace/file",
		`{"path":"a.go","content":"x","baseHash":"sha256:stale"}`)
	if status != http.StatusConflict {
		t.Fatalf("status %d: %s", status, body)
	}

	var env struct {
		Code    string `json:"code"`
		Details struct {
			CurrentHash       string `json:"currentHash"`
			CurrentSize       int    `json:"currentSize"`
			CurrentModifiedAt string `json:"currentModifiedAt"`
		} `json:"details"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if env.Code != "WORKSPACE_FILE_CONFLICT" {
		t.Fatalf("code = %q", env.Code)
	}
	if env.Details.CurrentHash != "sha256:onDisk" {
		t.Fatalf("currentHash = %q", env.Details.CurrentHash)
	}
	if env.Details.CurrentSize != 4096 {
		t.Fatalf("currentSize = %d, want 4096 as a number", env.Details.CurrentSize)
	}
	if !strings.HasPrefix(env.Details.CurrentModifiedAt, "2026-") {
		t.Fatalf("currentModifiedAt = %q, want an RFC3339 timestamp", env.Details.CurrentModifiedAt)
	}
}

// conflictWithDetails mirrors the error WriteWorkspaceFile builds on a stale
// baseHash, with the three details the editor needs to resolve it.
func conflictWithDetails() error {
	return apierr.Conflict(
		"WORKSPACE_FILE_CONFLICT",
		"The file changed on disk since it was read",
		map[string]any{
			"currentHash":       "sha256:onDisk",
			"currentSize":       4096,
			"currentModifiedAt": "2026-08-25T12:00:00.123456789Z",
		},
	)
}
