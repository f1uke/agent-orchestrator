// Package llm runs the one-shot model calls learning makes, sealed off from
// everything a Claude Code session normally carries.
//
// It drives the human's own `claude` CLI rather than an API key: the human
// runs Claude Code on a subscription login, and the CLI is what holds it. A
// plain `claude -p` loads the user's CLAUDE.md, every skill and plugin (and
// their hooks), MCP servers and auto-memory, and writes a transcript of the
// call - about 34k tokens of someone else's context, and a copy of the human's
// words left on disk. The flags below were measured (2026-10-03, Claude Code
// 2.1.287) to remove all of it while keeping the subscription login working:
//
//   - --setting-sources "" keeps user CLAUDE.md, plugins and their hooks out;
//     without it the user's CLAUDE.md still loads.
//   - --tools "" leaves the model no tool at all, so a prompt-injected turn
//     cannot make it act; --strict-mcp-config with an empty config and
//     --disable-slash-commands close the rest.
//   - --no-session-persistence writes no transcript.
//   - DISABLE_PROMPT_CACHING=1 cut a measured batch from $0.121 to $0.078: the
//     prompt is still cached, at the cheaper short-lived rate.
//
// The call runs from an empty temporary directory with AO's own session
// variables removed from its environment, so nothing ties it to a session.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"
)

// Request is one call.
type Request struct {
	Model        string
	Effort       string
	SystemPrompt string
	// Schema is the JSON Schema the answer must satisfy.
	Schema json.RawMessage
	// Input is the user turn, sent on stdin.
	Input string
}

// Result is a successful call.
type Result struct {
	// Output is the structured answer, valid against the request's schema.
	Output json.RawMessage
	// CostUSD is what the harness reports the call cost at API prices. On a
	// subscription it is quota, not money, but it is the same measure for the
	// daily budget either way.
	CostUSD      float64
	InputTokens  int64
	OutputTokens int64
	DurationMS   int64
}

// Error is a failed call, carrying the end of what the CLI wrote to stderr:
// a silent model failure is how a learning pipeline dies without anyone
// noticing.
type Error struct {
	Err        error
	StderrTail string
	// CostUSD is what the call cost before it failed, when the CLI said.
	CostUSD float64
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// Runner makes one call.
type Runner interface {
	Run(ctx context.Context, req Request) (Result, error)
}

// ClaudeCLI is the Runner over the `claude` binary.
type ClaudeCLI struct {
	// Binary resolves the claude executable.
	Binary func(ctx context.Context) (string, error)
	// Timeout bounds one call. Zero uses DefaultTimeout.
	Timeout time.Duration
	// Environ is the base environment. Nil uses os.Environ.
	Environ func() []string
}

// DefaultTimeout bounds one call. A batch measured 5-7 s; the bound is for a
// CLI that hangs, not for a slow answer.
const DefaultTimeout = 5 * time.Minute

// maxStderrTail bounds the stderr kept on a failure.
const maxStderrTail = 2000

// Args is the sealed command line for req, without the binary.
func Args(req Request) []string {
	args := []string{
		"-p",
		"--model", req.Model,
		"--output-format", "json",
		"--json-schema", string(req.Schema),
		"--tools", "",
		"--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`,
		"--disable-slash-commands",
		"--no-session-persistence",
		"--setting-sources", "",
		"--system-prompt", req.SystemPrompt,
	}
	if req.Effort != "" {
		args = append(args, "--effort", req.Effort)
	}
	return args
}

// Env is the environment for a call: the base without AO's session variables,
// with the cheaper cache mode.
func Env(base []string) []string {
	out := make([]string, 0, len(base)+1)
	for _, kv := range base {
		if strings.HasPrefix(kv, "AO_") || strings.HasPrefix(kv, "DISABLE_PROMPT_CACHING=") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "DISABLE_PROMPT_CACHING=1")
}

// cliResult is the CLI's --output-format json envelope.
type cliResult struct {
	IsError          bool            `json:"is_error"`
	Result           string          `json:"result"`
	StructuredOutput json.RawMessage `json:"structured_output"`
	TotalCostUSD     float64         `json:"total_cost_usd"`
	DurationMS       int64           `json:"duration_ms"`
	Usage            struct {
		InputTokens              int64 `json:"input_tokens"`
		CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		OutputTokens             int64 `json:"output_tokens"`
	} `json:"usage"`
}

// Run makes the call.
func (c ClaudeCLI) Run(ctx context.Context, req Request) (Result, error) {
	if c.Binary == nil {
		return Result{}, &Error{Err: errors.New("no claude binary resolver")}
	}
	binary, err := c.Binary(ctx)
	if err != nil {
		return Result{}, &Error{Err: fmt.Errorf("resolve claude: %w", err)}
	}
	dir, err := os.MkdirTemp("", "ao-learn-")
	if err != nil {
		return Result{}, &Error{Err: fmt.Errorf("temp dir: %w", err)}
	}
	defer func() { _ = os.RemoveAll(dir) }()

	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, Args(req)...) //nolint:gosec // the resolved claude binary, with flags this package builds
	cmd.Dir = dir
	environ := c.Environ
	if environ == nil {
		environ = os.Environ
	}
	cmd.Env = Env(environ())
	cmd.Stdin = strings.NewReader(req.Input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	return parse(stdout.Bytes(), tail(stderr.String()), runErr, ctx.Err())
}

func parse(stdout []byte, stderrTail string, runErr, ctxErr error) (Result, error) {
	var r cliResult
	decodeErr := json.Unmarshal(bytes.TrimSpace(stdout), &r)
	switch {
	case ctxErr != nil:
		return Result{}, &Error{Err: fmt.Errorf("claude call: %w", ctxErr), StderrTail: stderrTail}
	case decodeErr != nil && runErr != nil:
		return Result{}, &Error{Err: fmt.Errorf("claude call: %w", runErr), StderrTail: stderrTail}
	case decodeErr != nil:
		return Result{}, &Error{Err: fmt.Errorf("claude output is not JSON: %w", decodeErr), StderrTail: stderrTail}
	case r.IsError:
		return Result{}, &Error{Err: fmt.Errorf("claude reported an error: %s", strings.TrimSpace(r.Result)), StderrTail: stderrTail, CostUSD: r.TotalCostUSD}
	case len(r.StructuredOutput) == 0 || string(r.StructuredOutput) == "null":
		return Result{}, &Error{Err: errors.New("claude returned no structured output"), StderrTail: stderrTail, CostUSD: r.TotalCostUSD}
	}
	return Result{
		Output:       r.StructuredOutput,
		CostUSD:      r.TotalCostUSD,
		InputTokens:  r.Usage.InputTokens + r.Usage.CacheCreationInputTokens + r.Usage.CacheReadInputTokens,
		OutputTokens: r.Usage.OutputTokens,
		DurationMS:   r.DurationMS,
	}, nil
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxStderrTail {
		return s
	}
	start := len(s) - maxStderrTail
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:]
}
