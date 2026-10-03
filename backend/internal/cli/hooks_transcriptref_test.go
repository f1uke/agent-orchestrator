package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/learn/fingerprint"
)

// A prompt submit reports the conversation file and the prompt's fingerprint
// on their own route - and the prompt text itself never leaves the hook
// process, on either route.
func TestHooks_PromptSubmitReportsTranscriptRefWithoutThePrompt(t *testing.T) {
	var (
		mu     sync.Mutex
		bodies = map[string]string{}
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies[r.URL.Path] = string(body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("AO_SESSION_ID", "ao-7")
	cfg := setConfigEnv(t)
	writeRunFileFor(t, cfg, srv)

	secret := "the deploy password is hunter22, use the staging script"
	payload := `{"session_id":"4983f40a-5e7d-5092-ac33-9c6f9f318cff","transcript_path":"/h/.claude/projects/-wt/4983f40a-5e7d-5092-ac33-9c6f9f318cff.jsonl","prompt":"` + secret + `"}`
	if _, errOut, err := executeCLI(t, Deps{In: strings.NewReader(payload), ProcessAlive: func(int) bool { return true }}, "hooks", "claude-code", "user-prompt-submit"); err != nil {
		t.Fatalf("hook failed: %v\nstderr=%s", err, errOut)
	}

	mu.Lock()
	defer mu.Unlock()
	ref := bodies["/api/v1/sessions/ao-7/transcript-ref"]
	if ref == "" {
		t.Fatalf("no transcript ref posted; got %v", bodies)
	}
	fp, _ := fingerprint.Of(secret)
	if !strings.Contains(ref, fp) || !strings.Contains(ref, "4983f40a-5e7d-5092-ac33-9c6f9f318cff.jsonl") {
		t.Errorf("ref body = %s", ref)
	}
	for path, body := range bodies {
		if strings.Contains(body, "hunter22") {
			t.Errorf("the prompt text crossed into the daemon on %s: %s", path, body)
		}
	}
	if act := bodies["/api/v1/sessions/ao-7/activity"]; strings.Contains(act, "transcript") || strings.Contains(act, "4983f40a") {
		t.Errorf("the activity signal carries a path or native id, which its contract forbids: %s", act)
	}
}
