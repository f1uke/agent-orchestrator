package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestArgs_AreSealed(t *testing.T) {
	got := strings.Join(Args(Request{Model: "claude-sonnet-5-5", Effort: "low", SystemPrompt: "sys", Schema: []byte(`{}`)}), " ")
	for _, want := range []string{
		"-p", "--model claude-sonnet-5-5", "--effort low", "--output-format json", "--tools ",
		"--strict-mcp-config", "--disable-slash-commands", "--no-session-persistence", "--setting-sources ",
		"--system-prompt sys",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("args lack %q: %s", want, got)
		}
	}
	if strings.Contains(got, "dangerously") || strings.Contains(got, "--allowedTools") {
		t.Errorf("args grant permissions: %s", got)
	}
}

func TestEnv_DropsAOSessionVariables(t *testing.T) {
	got := Env([]string{"HOME=/h", "AO_SESSION_ID=p-1", "AO_RUN_FILE=/x", "PATH=/bin", "DISABLE_PROMPT_CACHING=0"})
	joined := strings.Join(got, " ")
	if strings.Contains(joined, "AO_") || !strings.Contains(joined, "HOME=/h") || !strings.HasSuffix(joined, "DISABLE_PROMPT_CACHING=1") || strings.Count(joined, "DISABLE_PROMPT_CACHING") != 1 {
		t.Errorf("env = %v", got)
	}
}

func TestParse(t *testing.T) {
	ok := `{"type":"result","is_error":false,"result":"","structured_output":{"candidates":[]},"total_cost_usd":0.078,"duration_ms":7263,"usage":{"input_tokens":2,"cache_creation_input_tokens":28833,"cache_read_input_tokens":722,"output_tokens":538}}`
	res, err := parse([]byte(ok), "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Output) != `{"candidates":[]}` || res.CostUSD != 0.078 || res.InputTokens != 29557 || res.OutputTokens != 538 || res.DurationMS != 7263 {
		t.Errorf("result = %+v", res)
	}

	cases := map[string]struct {
		stdout string
		runErr error
		ctxErr error
		want   string
	}{
		"harness error":   {stdout: `{"is_error":true,"result":"Not logged in · Please run /login","total_cost_usd":0}`, runErr: errors.New("exit status 1"), want: "Not logged in"},
		"no structured":   {stdout: `{"is_error":false,"result":"hi","total_cost_usd":0.01}`, want: "no structured output"},
		"crash, no json":  {stdout: ``, runErr: errors.New("exit status 2"), want: "exit status 2"},
		"garbage, exit 0": {stdout: `oops`, want: "not JSON"},
		"timeout":         {stdout: ``, runErr: errors.New("killed"), ctxErr: context.DeadlineExceeded, want: "deadline"},
	}
	for name, c := range cases {
		_, err := parse([]byte(c.stdout), "stderr says why", c.runErr, c.ctxErr)
		var e *Error
		if !errors.As(err, &e) || !strings.Contains(err.Error(), c.want) || e.StderrTail != "stderr says why" {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestTail_KeepsTheEndWithoutSplittingACharacter(t *testing.T) {
	s := strings.Repeat("ก", 1000) // 3 bytes each
	got := tail(s)
	if len(got) > maxStderrTail || strings.ToValidUTF8(got, "?") != got {
		t.Errorf("tail is %d bytes, valid=%v", len(got), strings.ToValidUTF8(got, "?") == got)
	}
}
