package toolcurate

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// canary is planted in every field the whitelist must NEVER read. If it ever
// reaches a curated detail, a secret has left the process.
const canary = "CANARY-SECRET-9f3a2b"

// Credential-shaped fixtures, assembled from fragments on purpose. They are
// fabricated, but a verbatim token literal in the source trips the repo's secret
// scanner (gitleaks) and a test fixture is not worth a false positive in CI.
// The split must fall inside the token's PREFIX, not its payload: some rules
// (Slack) make the payload optional and match the bare prefix alone. The
// assembled value is unchanged, so it still exercises the real redaction rule.
const (
	fakeGitHubToken = "gh" + "p_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	fakeOpenAIKey   = "s" + "k-abcdefghijklmnopqrstuvwxyz0123456789"
	fakeAWSKeyID    = "AK" + "IAIOSFODNN7EXAMPLE"
	fakeSlackToken  = "xo" + "xb-1234567890-abcdefghij"
	fakeHexDigest   = "0123456789abcdef0123" + "456789abcdef0123"
)

// TestCurate_NeverLeaksNonWhitelistedFields is THE safety test. For every tool
// AO knows about (and one it doesn't), the raw tool_input carries the canary in
// every non-whitelisted field — a Write's whole file body, a Bash command with a
// token in it, an Edit's old/new strings, the tool_response, plus keys AO has
// never heard of. Nothing curated may contain it.
//
// Mutation check: drop the per-tool whitelist (pass tool_input through) and this
// goes red on the first case.
func TestCurate_NeverLeaksNonWhitelistedFields(t *testing.T) {
	tools := []string{
		"Bash", "Read", "Edit", "Write", "NotebookEdit", "Glob", "Grep",
		"WebFetch", "WebSearch", "TodoWrite", "Task",
		"mcp__private__deploy", "SomeToolAOHasNeverSeen",
	}
	for _, tool := range tools {
		t.Run(tool, func(t *testing.T) {
			raw := mustJSON(t, map[string]any{
				// The Write tool's tool_input IS the entire file body.
				"content": "package main\n\nconst apiKey = \"" + canary + "\"\n",
				// A Bash command can carry a token inline.
				"command":                    "TOKEN=" + canary + " curl https://example.test",
				"old_string":                 canary,
				"new_string":                 canary,
				"prompt":                     canary,
				"input":                      canary,
				"body":                       canary,
				"env":                        map[string]string{"SECRET": canary},
				"totally_unknown_future_key": canary,
			})
			got := Curate(domain.ActivityEventToolStart, tool, raw)
			assertNoCanary(t, got)
		})
	}
}

// The PostToolUse payload also carries tool_response (command output, file
// contents). Curate is only ever handed tool_input, but assert the contract
// holds if a caller ever hands it a whole payload by mistake.
func TestCurate_NeverLeaksToolResponse(t *testing.T) {
	raw := mustJSON(t, map[string]any{
		"tool_response": map[string]any{"stdout": canary, "file": map[string]string{"content": canary}},
		"description":   "Running the test suite",
	})
	got := Curate(domain.ActivityEventToolEnd, "Bash", raw)
	assertNoCanary(t, got)
	if got.Text != "Running the test suite" {
		t.Errorf("Text = %q, want the model-authored description to survive", got.Text)
	}
}

// The whitelist table itself: for each tool, exactly which small safe field
// becomes bubble content.
func TestCurate_WhitelistTable(t *testing.T) {
	cases := []struct {
		name       string
		tool       string
		input      map[string]any
		wantTool   string
		wantTarget string
		wantText   string
	}{
		{
			name:     "Bash takes the model-authored description, never the command",
			tool:     "Bash",
			input:    map[string]any{"command": "pnpm test --token=" + canary, "description": "Running the test suite"},
			wantTool: "Bash", wantText: "Running the test suite",
		},
		{
			name:     "Bash without a description degrades to the bare tool name",
			tool:     "Bash",
			input:    map[string]any{"command": "pnpm test"},
			wantTool: "Bash",
		},
		{
			name:     "Read takes only the file base name",
			tool:     "Read",
			input:    map[string]any{"file_path": "/Users/someone/Documents/Projects/ao/backend/internal/cli/hooks.go"},
			wantTool: "Read", wantTarget: "hooks.go",
		},
		{
			name:     "Edit takes only the file base name",
			tool:     "Edit",
			input:    map[string]any{"file_path": "/tmp/x/FileTree.tsx", "old_string": canary, "new_string": canary},
			wantTool: "Edit", wantTarget: "FileTree.tsx",
		},
		{
			name:     "Write takes the base name and never the content",
			tool:     "Write",
			input:    map[string]any{"file_path": "/tmp/x/secrets.env", "content": canary},
			wantTool: "Write", wantTarget: "secrets.env",
		},
		{
			name:     "NotebookEdit takes the notebook base name",
			tool:     "NotebookEdit",
			input:    map[string]any{"notebook_path": "/tmp/x/analysis.ipynb", "new_source": canary},
			wantTool: "NotebookEdit", wantTarget: "analysis.ipynb",
		},
		{
			name:     "Grep takes the pattern",
			tool:     "Grep",
			input:    map[string]any{"pattern": "PostToolUse", "path": "/Users/someone/private"},
			wantTool: "Grep", wantTarget: "PostToolUse",
		},
		{
			name:     "Glob takes the pattern",
			tool:     "Glob",
			input:    map[string]any{"pattern": "**/*.go", "path": "/Users/someone/private"},
			wantTool: "Glob", wantTarget: "**/*.go",
		},
		{
			name:     "WebFetch takes the host only, never the query string",
			tool:     "WebFetch",
			input:    map[string]any{"url": "https://api.example.test/v1/thing?access_token=" + canary, "prompt": canary},
			wantTool: "WebFetch", wantTarget: "api.example.test",
		},
		{
			name:     "WebSearch takes the query",
			tool:     "WebSearch",
			input:    map[string]any{"query": "go sse contract"},
			wantTool: "WebSearch", wantTarget: "go sse contract",
		},
		{
			name:     "TodoWrite is whitelisted but contributes no field",
			tool:     "TodoWrite",
			input:    map[string]any{"todos": []any{map[string]string{"content": canary}}},
			wantTool: "TodoWrite",
		},
		{
			name:     "Task takes the description, never the sub-agent prompt",
			tool:     "Task",
			input:    map[string]any{"description": "Investigate the feed", "prompt": canary},
			wantTool: "Task", wantText: "Investigate the feed",
		},
		{
			name:  "an unknown tool contributes nothing at all, not even its name",
			tool:  "mcp__private-server__deploy_prod",
			input: map[string]any{"description": "Deploying", "file_path": "/tmp/a.go"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Curate(domain.ActivityEventToolStart, tc.tool, mustJSON(t, tc.input))
			if got.Tool != tc.wantTool {
				t.Errorf("Tool = %q, want %q", got.Tool, tc.wantTool)
			}
			if got.Target != tc.wantTarget {
				t.Errorf("Target = %q, want %q", got.Target, tc.wantTarget)
			}
			if got.Text != tc.wantText {
				t.Errorf("Text = %q, want %q", got.Text, tc.wantText)
			}
			if got.Kind != domain.ActivityEventToolStart {
				t.Errorf("Kind = %q, want tool_start", got.Kind)
			}
			assertNoCanary(t, got)
		})
	}
}

// Defence in depth: the whitelisted fields are model- or user-authored text, so
// a secret can in principle be typed into one. Redact secret-shaped runs.
func TestCurate_RedactsSecretShapedText(t *testing.T) {
	cases := []struct {
		name string
		desc string
		leak string
	}{
		{"github token", "Pushing with " + fakeGitHubToken, fakeGitHubToken},
		{"openai key", "Calling " + fakeOpenAIKey, fakeOpenAIKey},
		{"aws key id", "Using " + fakeAWSKeyID + " now", fakeAWSKeyID},
		{"slack token", "Posting via " + fakeSlackToken, fakeSlackToken},
		{"assignment", "Exporting API_KEY=hunter2hunter2hunter2", "hunter2hunter2hunter2"},
		{"long hex", "Checking " + fakeHexDigest, fakeHexDigest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Curate(domain.ActivityEventToolStart, "Bash", mustJSON(t, map[string]any{"description": tc.desc}))
			if strings.Contains(got.Text, tc.leak) {
				t.Errorf("secret survived redaction: %q", got.Text)
			}
			if !strings.Contains(got.Text, redactedMarker) {
				t.Errorf("Text = %q, want the redacted marker %q", got.Text, redactedMarker)
			}
		})
	}
}

func TestCurate_TruncatesAndFlattens(t *testing.T) {
	long := strings.Repeat("wordy ", 60)
	got := Curate(domain.ActivityEventToolStart, "Bash", mustJSON(t, map[string]any{"description": long}))
	if len([]rune(got.Text)) > maxTextRunes {
		t.Errorf("Text is %d runes, want <= %d", len([]rune(got.Text)), maxTextRunes)
	}
	if !strings.HasSuffix(got.Text, ellipsis) {
		t.Errorf("truncated text should be marked with %q: %q", ellipsis, got.Text)
	}

	multi := Curate(domain.ActivityEventToolStart, "Bash",
		mustJSON(t, map[string]any{"description": "first line\nsecond\tline\r\n  third"}))
	if strings.ContainsAny(multi.Text, "\n\r\t") {
		t.Errorf("bubble text must be one flat line: %q", multi.Text)
	}
	if multi.Text != "first line second line third" {
		t.Errorf("Text = %q, want collapsed whitespace", multi.Text)
	}

	longPattern := Curate(domain.ActivityEventToolStart, "Grep",
		mustJSON(t, map[string]any{"pattern": strings.Repeat("x", 200)}))
	if len([]rune(longPattern.Target)) > maxTargetRunes {
		t.Errorf("Target is %d runes, want <= %d", len([]rune(longPattern.Target)), maxTargetRunes)
	}
}

// A path must never splash the human's home directory onto a shared screen.
func TestCurate_NeverEmitsAPath(t *testing.T) {
	for _, tool := range []string{"Read", "Edit", "Write", "NotebookEdit"} {
		key := "file_path"
		if tool == "NotebookEdit" {
			key = "notebook_path"
		}
		got := Curate(domain.ActivityEventToolStart, tool,
			mustJSON(t, map[string]any{key: "/Users/realname/Documents/Projects/private/thing.go"}))
		if strings.Contains(got.Target, "/") || strings.Contains(got.Target, "realname") {
			t.Errorf("%s: Target = %q, want a bare base name", tool, got.Target)
		}
	}
}

func TestCurate_ToleratesGarbage(t *testing.T) {
	for _, raw := range []string{"", "null", "[]", `"a string"`, "{", `{"file_path":123}`} {
		got := Curate(domain.ActivityEventToolStart, "Read", json.RawMessage(raw))
		if got.Kind != domain.ActivityEventToolStart {
			t.Errorf("%q: Kind = %q, want the kind preserved", raw, got.Kind)
		}
		if got.Target != "" || got.Text != "" {
			t.Errorf("%q: want no detail from an unparseable input, got %+v", raw, got)
		}
	}
}

func assertNoCanary(t *testing.T, d domain.ActivityDetail) {
	t.Helper()
	blob, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), canary) {
		t.Fatalf("SECRET LEAK: curated detail contains the canary: %s", blob)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
