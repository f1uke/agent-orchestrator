package chatproto

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

const fixtureBashID = "b7d1c2a4-5e6f-4a1b-9c3d-2e4f6a8b0c1d"

type sentBashResult struct {
	Type        string `json:"type"`
	UUID        string `json:"uuid"`
	ShouldQuery *bool  `json:"shouldQuery"`
	Message     struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
}

func output(s string) BashOutput {
	var o BashOutput
	_, _ = o.Write([]byte(s))
	return o
}

// sendBash encodes r and returns the two text blocks claude reads.
func sendBash(t *testing.T, r BashResult) (input, result string) {
	t.Helper()
	b, err := Encode(r)
	if err != nil {
		t.Fatal(err)
	}
	var m sentBashResult
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	if m.Type != "user" || m.UUID != r.UUID || m.ShouldQuery == nil || *m.ShouldQuery || m.Message.Role != "user" {
		t.Fatalf("message head = %s, want a user message with uuid %s and shouldQuery false", b, r.UUID)
	}
	if len(m.Message.Content) != 2 || m.Message.Content[0].Type != "text" || m.Message.Content[1].Type != "text" {
		t.Fatalf("content = %s, want two text blocks", b)
	}
	return m.Message.Content[0].Text, m.Message.Content[1].Text
}

func bashTags(stdout, stderr string, code int) string {
	return "<bash-stdout>" + stdout + "</bash-stdout><bash-stderr>" + stderr + "</bash-stderr><bash-exit-code>" + fmt.Sprint(code) + "</bash-exit-code>"
}

func cutMarker(n int) string { return fmt.Sprintf("\n... [%d characters cut] ...\n", n) }

func TestEncodeBashResult(t *testing.T) {
	h20k, t20k := strings.Repeat("H", 20000), strings.Repeat("T", 20000)
	h25k, t25k := strings.Repeat("H", 25000), strings.Repeat("T", 25000)
	tests := []struct {
		name       string
		in         BashResult
		wantInput  string
		wantResult string
	}{
		{"a command that printed", BashResult{UUID: "b1", Command: "echo hi", Stdout: output("hi\n")},
			"<bash-input>echo hi</bash-input>", bashTags("hi\n", "", 0)},
		{"tag characters are escaped in the command and the output",
			BashResult{UUID: "b1", Command: "a && b < c > d", Stdout: output("<x>&"), Stderr: output("1 > 0"), ExitCode: 1},
			"<bash-input>a &amp;&amp; b &lt; c &gt; d</bash-input>", bashTags("&lt;x&gt;&amp;", "1 &gt; 0", 1)},
		{"a signal's code passes through", BashResult{UUID: "b1", Command: "kill -TERM $$", ExitCode: 143},
			"<bash-input>kill -TERM $$</bash-input>", bashTags("", "", 143)},
		{"stopped reads as 130 with [stopped] after the stderr", BashResult{UUID: "b1", Command: "sleep 9", Stderr: output("partial"), ExitCode: 143, Stopped: true},
			"<bash-input>sleep 9</bash-input>", bashTags("", "partial\n[stopped]", 130)},
		{"stopped with no stderr", BashResult{UUID: "b1", Command: "sleep 9", Stdout: output("tick\n"), Stopped: true},
			"<bash-input>sleep 9</bash-input>", bashTags("tick\n", "[stopped]", 130)},
		{"a long stdout keeps its head and tail", BashResult{UUID: "b1", Command: "big", Stdout: output(h20k + t20k)},
			"<bash-input>big</bash-input>", bashTags(strings.Repeat("H", 15000)+cutMarker(10000)+strings.Repeat("T", 15000), "", 0)},
		{"a short stderr survives a long stdout", BashResult{UUID: "b1", Command: "big", Stdout: output(h25k + t25k), Stderr: output("boom\n"), ExitCode: 2},
			"<bash-input>big</bash-input>", bashTags(strings.Repeat("H", 14997)+cutMarker(20005)+strings.Repeat("T", 14998), "boom\n", 2)},
		{"two long streams share the cap", BashResult{UUID: "b1", Command: "big", Stdout: output(h20k + t20k), Stderr: output(h20k + t20k)},
			"<bash-input>big</bash-input>",
			bashTags(strings.Repeat("H", 7500)+cutMarker(25000)+strings.Repeat("T", 7500), strings.Repeat("H", 7500)+cutMarker(25000)+strings.Repeat("T", 7500), 0)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input, result := sendBash(t, tc.in)
			if input != tc.wantInput {
				t.Errorf("input block = %q, want %q", input, tc.wantInput)
			}
			if result != tc.wantResult {
				t.Errorf("result block = %.300q, want %.300q", result, tc.wantResult)
			}
		})
	}
}

func TestEncodeBashResultKeepsTheTrueTailOfAStreamFarPastTheCap(t *testing.T) {
	var o BashOutput
	const lines = 400_000
	for i := range lines {
		_, _ = fmt.Fprintf(&o, "line %07d\n", i)
	}
	_, result := sendBash(t, BashResult{UUID: "b1", Command: "seq", Stdout: o})
	stdout := strings.TrimSuffix(strings.TrimPrefix(result, "<bash-stdout>"), "</bash-stdout><bash-stderr></bash-stderr><bash-exit-code>0</bash-exit-code>")
	if !strings.HasPrefix(stdout, "line 0000000\nline 0000001\n") {
		t.Errorf("stdout starts %q, want the first lines", stdout[:40])
	}
	if !strings.HasSuffix(stdout, fmt.Sprintf("line %07d\n", lines-1)) {
		t.Errorf("stdout ends %q, want the last line", stdout[len(stdout)-40:])
	}
	if want := cutMarker(lines*13 - BashModelCap); !strings.Contains(stdout, want) {
		t.Errorf("stdout has no %q marker", want)
	}
	if n := len(stdout) - len(cutMarker(lines*13-BashModelCap)); n != BashModelCap {
		t.Errorf("stdout keeps %d bytes, want %d", n, BashModelCap)
	}
}

func TestEncodeBashResultCutsOnlyBetweenCharacters(t *testing.T) {
	_, result := sendBash(t, BashResult{UUID: "b1", Command: "thai", Stdout: output(strings.Repeat("ก", 20001))})
	if !utf8.ValidString(result) || strings.ContainsRune(result, utf8.RuneError) {
		t.Errorf("the cut split a character: %q", result[len(result)-200:])
	}
}

func TestEncodeBashResultNeedsAUUIDAndACommand(t *testing.T) {
	for _, r := range []BashResult{{Command: "ls"}, {UUID: "b1"}} {
		if _, err := Encode(r); err == nil {
			t.Errorf("Encode(%+v) = nil error", r)
		}
	}
}

func TestFoldBashShowsOneRowAndHidesItsInjection(t *testing.T) {
	tr := foldEntries(t, loadFixture(t, "bash_session.jsonl"))
	var bash []BashItem
	var users []UserItem
	var turnEnds int
	for _, it := range tr.Items {
		switch it := it.(type) {
		case BashItem:
			bash = append(bash, it)
		case UserItem:
			users = append(users, it)
		case TurnEnd:
			turnEnds++
		}
	}
	code := 0
	want := BashItem{
		ID: fixtureBashID, Command: "git status --short && echo done", CWD: "/work/project",
		Output: []BashChunk{{BashStdout, " M a.txt\n"}, {BashStderr, "warning: example\n"}, {BashStdout, "done\n"}},
		Phase:  BashSettled, End: &BashEnd{Code: &code},
	}
	if len(bash) != 1 || !reflect.DeepEqual(bash[0], want) {
		t.Errorf("bash rows = %+v, want one %+v", bash, want)
	}
	if len(users) != 1 || users[0].Text != "Reply with the word ok." || users[0].State != CommandCompleted {
		t.Errorf("user rows = %+v, want only the real prompt, completed", users)
	}
	if turnEnds != 1 || tr.CostUSD != 0.0123 {
		t.Errorf("turn ends = %d, cost %v; want the real prompt's turn only", turnEnds, tr.CostUSD)
	}
	if tr.Bash != nil || tr.Busy {
		t.Errorf("Bash = %+v, Busy = %v after everything settled", tr.Bash, tr.Busy)
	}
}

func TestFoldBashPhases(t *testing.T) {
	entries := loadFixture(t, "bash_session.jsonl")
	for _, s := range []struct {
		seq  uint64
		want BashPhase // "" for no run the session is busy with
	}{
		{3, ""},
		{4, BashRunning},
		{7, BashRunning},
		{8, BashEnded},
		{9, BashInjected},
		{12, BashInjected},
		{13, ""},
		{14, ""},
	} {
		tr := foldEntries(t, upTo(entries, s.seq))
		got := BashPhase("")
		if tr.Bash != nil {
			got = tr.Bash.Phase
		}
		if got != s.want {
			t.Errorf("after seq %d the busy run is %q, want %q", s.seq, got, s.want)
		}
		for _, it := range tr.Items {
			if _, ok := it.(TurnEnd); ok {
				t.Errorf("after seq %d there is a TurnEnd before any turn ran", s.seq)
			}
		}
	}
	if out := foldEntries(t, upTo(entries, 6)).Bash.Output; len(out) != 2 {
		t.Errorf("output so far = %+v, want the two chunks logged by seq 6", out)
	}
}

func TestFoldBashSettlesWhenItsClaudeGoesAway(t *testing.T) {
	entries := loadFixture(t, "bash_session.jsonl")
	t.Run("a host that died mid-run leaves a stopped run on the next start", func(t *testing.T) {
		tr := foldEntries(t, extend(entries, 6, synth(100, DirHost, `{"kind":"spawn","argv":["claude"],"resume":"s"}`)))
		if tr.Bash != nil {
			t.Errorf("Bash = %+v after a new claude started, want nil", tr.Bash)
		}
		if run := itemsOf[BashItem](tr)[0]; run.End == nil || !run.End.Stopped || run.End.Code != nil {
			t.Errorf("run end = %+v, want stopped with no code", run.End)
		}
	})
	t.Run("claude exiting before it answered the injection", func(t *testing.T) {
		tr := foldEntries(t, extend(entries, 9,
			synth(100, DirHost, `{"kind":"exit","code":0}`),
			synth(101, DirOut, `{"type":"result","subtype":"success","num_turns":1,"total_cost_usd":0.5}`),
		))
		if tr.Bash != nil {
			t.Errorf("Bash = %+v after claude exited, want nil", tr.Bash)
		}
		if itemsOf[BashItem](tr)[0].End.Code == nil {
			t.Errorf("the run lost its real exit code")
		}
		if last, ok := tr.Items[len(tr.Items)-1].(TurnEnd); !ok || last.CostUSD != 0.5 {
			t.Errorf("a later result was not a turn end: %+v", tr.Items[len(tr.Items)-1])
		}
	})
}

func TestFoldBashIgnoresALifecycleForItsInjection(t *testing.T) {
	tr := foldEntries(t, extend(loadFixture(t, "bash_session.jsonl"), 9,
		synth(100, DirOut, `{"type":"command_lifecycle","command_uuid":"`+fixtureBashID+`","state":"started"}`)))
	if tr.Busy || len(tr.Queued) != 0 {
		t.Errorf("Busy = %v, Queued = %v from the injection's lifecycle", tr.Busy, tr.Queued)
	}
	if tr.Bash == nil || tr.Bash.Phase != BashInjected {
		t.Errorf("Bash = %+v, want injected", tr.Bash)
	}
}

func TestFoldShowsAQuietMessageThatIsNoBashResult(t *testing.T) {
	tr := foldEntries(t, []Entry{
		synth(1, DirIn, `{"type":"user","uuid":"u-q","shouldQuery":false,"message":{"role":"user","content":"note"}}`),
	})
	if len(tr.Items) != 1 {
		t.Fatalf("items = %+v, want one user row", tr.Items)
	}
	if u, ok := tr.Items[0].(UserItem); !ok || u.Text != "note" {
		t.Errorf("item = %+v, want the user row", tr.Items[0])
	}
}

func TestFoldBashSnapshotIsACopy(t *testing.T) {
	f := NewFolder()
	for _, e := range upTo(loadFixture(t, "bash_session.jsonl"), 5) {
		if err := f.Apply(e); err != nil {
			t.Fatal(err)
		}
	}
	before := f.Transcript()
	if err := f.Apply(synth(6, DirHost, `{"kind":"bash_output","id":"`+fixtureBashID+`","stream":"stdout","text":"more"}`)); err != nil {
		t.Fatal(err)
	}
	if len(before.Bash.Output) != 1 || len(itemsOf[BashItem](before)[0].Output) != 1 {
		t.Errorf("an earlier snapshot changed: %+v", before.Bash.Output)
	}
}

// extend is the log up to seq with more entries after it, in a new slice.
func extend(entries []Entry, seq uint64, more ...Entry) []Entry {
	return append(slices.Clone(upTo(entries, seq)), more...)
}
