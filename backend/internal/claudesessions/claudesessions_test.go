package claudesessions

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

type fixture struct{ dir string }

func newFixture(t *testing.T) fixture {
	t.Helper()
	return fixture{dir: t.TempDir()}
}

func (f fixture) write(t *testing.T, pid int, mutate func(m map[string]any)) {
	t.Helper()
	desc := map[string]any{
		"pid":                 pid,
		"sessionId":           "conv-" + strconv.Itoa(pid),
		"name":                "chat-unsafe-url-whitelist-f5",
		"peerProtocol":        1,
		"kind":                "interactive",
		"tmux":                "advisor-ios-app-9:@12.%12",
		"messagingSocketPath": "/tmp/cc-socks/" + strconv.Itoa(pid) + ".sock",
	}
	if mutate != nil {
		mutate(desc)
	}
	raw, err := json.Marshal(desc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(f.dir, strconv.Itoa(pid)+".json"), raw, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func (f fixture) registry(argv []string, alive bool) *Registry {
	return New(Options{
		Dir:      f.dir,
		PIDAlive: func(int) bool { return alive },
		ProcArgv: func(context.Context, int) ([]string, error) { return argv, nil },
	})
}

var claudeArgv = []string{"/Users/x/.local/bin/claude", "--session-id", "abc"}

func TestByTmuxSession(t *testing.T) {
	f := newFixture(t)
	f.write(t, 4242, nil)

	got, err := f.registry(claudeArgv, true).ByTmuxSession(context.Background(), "advisor-ios-app-9")
	if err != nil {
		t.Fatalf("ByTmuxSession: %v", err)
	}
	if got.PID != 4242 || got.TmuxSession != "advisor-ios-app-9" || got.Name != "chat-unsafe-url-whitelist-f5" {
		t.Fatalf("unexpected session %+v", got)
	}
}

func TestByName(t *testing.T) {
	f := newFixture(t)
	f.write(t, 4242, nil)

	got, err := f.registry(claudeArgv, true).ByName(context.Background(), "chat-unsafe-url-whitelist-f5")
	if err != nil {
		t.Fatalf("ByName: %v", err)
	}
	if got.TmuxSession != "advisor-ios-app-9" {
		t.Fatalf("name did not resolve to the owning pane: %+v", got)
	}
}

func TestByNameFallsBackToCaseInsensitive(t *testing.T) {
	f := newFixture(t)
	f.write(t, 4242, func(m map[string]any) { m["name"] = "Chat-Unsafe-URL-Whitelist" })

	if _, err := f.registry(claudeArgv, true).ByName(context.Background(), "chat-unsafe-url-whitelist"); err != nil {
		t.Fatalf("ByName (case-insensitive): %v", err)
	}
}

// Two Claude sessions in one worktree get names differing only by a random
// suffix, so an EXACT name must win over a case-insensitive one rather than
// turning a precise match into an ambiguous pair.
func TestByNamePrefersTheExactMatch(t *testing.T) {
	f := newFixture(t)
	f.write(t, 4242, func(m map[string]any) { m["name"] = "worktree-f5" })
	f.write(t, 4243, func(m map[string]any) {
		m["name"] = "WORKTREE-F5"
		m["tmux"] = "advisor-ios-app-10:@13.%13"
	})

	got, err := f.registry(claudeArgv, true).ByName(context.Background(), "worktree-f5")
	if err != nil {
		t.Fatalf("ByName: %v", err)
	}
	if got.PID != 4242 {
		t.Fatalf("exact match lost to the case-insensitive one: %+v", got)
	}
}

func TestLookupRejections(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(m map[string]any)
		argv   []string
		alive  bool
		target string
		reason string
	}{
		{name: "no session owns that pane", argv: claudeArgv, alive: true, target: "some-other-tmux", reason: "no-descriptor"},
		{name: "process is gone", argv: claudeArgv, alive: false, target: "advisor-ios-app-9", reason: "dead-pid"},
		{
			name: "pid was recycled by something else",
			argv: []string{"/usr/bin/vim", "notes.md"}, alive: true, target: "advisor-ios-app-9",
			reason: "recycled-pid",
		},
		{
			name:   "descriptor carries no pid",
			mutate: func(m map[string]any) { m["pid"] = 0 },
			argv:   claudeArgv, alive: true, target: "advisor-ios-app-9",
			reason: "no-descriptor",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.write(t, 4242, tc.mutate)
			_, err := f.registry(tc.argv, tc.alive).ByTmuxSession(context.Background(), tc.target)
			if err == nil {
				t.Fatalf("want rejection %q, got a session", tc.reason)
			}
			if got := Reason(err); got != tc.reason {
				t.Fatalf("reason %q, want %q", got, tc.reason)
			}
			var nf *NotFoundError
			if !errors.As(err, &nf) {
				t.Fatalf("error is not a *NotFoundError: %T", err)
			}
		})
	}
}

// A crew's two agents share a worktree, so only the pane tells them apart. Two
// records claiming ONE pane is the case where we must not guess.
func TestTwoRecordsForOnePaneIsAmbiguous(t *testing.T) {
	f := newFixture(t)
	f.write(t, 4242, nil)
	f.write(t, 4243, nil)

	_, err := f.registry(claudeArgv, true).ByTmuxSession(context.Background(), "advisor-ios-app-9")
	if got := Reason(err); got != "ambiguous-descriptor" {
		t.Fatalf("reason %q, want ambiguous-descriptor", got)
	}
}

func TestTmuxSessionName(t *testing.T) {
	tests := map[string]string{
		"ao-worker-7:@12.%12":        "ao-worker-7",
		"proj-feature-a-b:@892.%892": "proj-feature-a-b",
		"plain":                      "plain",
		"":                           "",
		// A tmux session name may itself contain a colon; the pane target is
		// always the last one.
		"weird:name:@1.%1": "weird:name",
	}
	for target, want := range tests {
		if got := TmuxSessionName(target); got != want {
			t.Fatalf("TmuxSessionName(%q) = %q, want %q", target, got, want)
		}
	}
}

func TestIsClaudeProcess(t *testing.T) {
	if !IsClaudeProcess([]string{"/Users/x/.local/bin/claude", "-p"}) {
		t.Fatal("an absolute claude path should count")
	}
	if !IsClaudeProcess([]string{"claude"}) {
		t.Fatal("a bare claude should count")
	}
	if IsClaudeProcess([]string{"/usr/bin/vim"}) || IsClaudeProcess(nil) {
		t.Fatal("a non-claude argv should not count")
	}
}
