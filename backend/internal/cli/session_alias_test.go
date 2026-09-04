package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A daemon that resolved a Claude session name says so in a header; the CLI has
// to pass that on, or the user keeps using an id that only works by accident
// and never learns the real one.
func TestSendReportsAResolvedSessionAlias(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "")
	cfg := setConfigEnv(t)
	const note = "mobility-4734-chat-unsafe-url-whitelist-f5 -> advisor-ios-app-9 (tmux advisor-ios-app-feature-MOBILITY-4734)"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(sessionResolvedHeader, note)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true,"sessionId":"advisor-ios-app-9","message":"hi"}`)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "send", "--session", "mobility-4734-chat-unsafe-url-whitelist-f5", "--message", "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(errOut, note) {
		t.Fatalf("stderr did not carry the resolution note.\nstderr=%s", errOut)
	}
	if !strings.Contains(errOut, "advisor-ios-app-9") {
		t.Fatalf("the note does not name the AO id to use next time.\nstderr=%s", errOut)
	}
}

// An ordinary call must stay quiet: the note is for the exceptional case only.
func TestSendSaysNothingWhenNoAliasWasResolved(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "")
	cfg := setConfigEnv(t)
	srv, _ := sendServer(t, http.StatusOK, `{"ok":true,"sessionId":"demo-1","message":"hi"}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "send", "--session", "demo-1", "--message", "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if strings.Contains(errOut, "session resolved") {
		t.Fatalf("a plain send announced a substitution.\nstderr=%s", errOut)
	}
}
