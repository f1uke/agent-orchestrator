package chatproto

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

const (
	longSessionID = "22cbfcdc-ec92-42c0-97a8-ac96adf4d02b"
	bashTaskPath  = "/private/tmp/tmp/-private-work-project/22cbfcdc-ec92-42c0-97a8-ac96adf4d02b/tasks/blaf8bxvg.output"
)

func logUpTo(t *testing.T, name string, last uint64) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	entries := loadFixture(t, name)
	lines := bytes.SplitAfter(data, []byte("\n"))
	var out []byte
	for i, e := range entries {
		if e.Seq <= last {
			out = append(out, lines[i]...)
		}
	}
	return out
}

func TestFindTaskOutput(t *testing.T) {
	cases := []struct {
		name     string
		lastSeq  uint64
		taskID   string
		wantPath string
	}{
		{"from the task_notification once the task ended", 1 << 62, "blaf8bxvg", bashTaskPath},
		{"from the tool_result while the task still runs", 14, "blaf8bxvg", bashTaskPath},
		{"nothing before the tool_result names the file", 12, "blaf8bxvg", ""},
		{"an agent task names its file in the notification", 1 << 62, "a5108821f9804b8ee",
			"/private/tmp/tmp/-private-work-project/22cbfcdc-ec92-42c0-97a8-ac96adf4d02b/tasks/a5108821f9804b8ee.output"},
		{"a foreground Bash has no output file", 1 << 62, "b9xi92xsa", ""},
		{"a task the log never names", 1 << 62, "nosuchtask", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := FindTaskOutput(bytes.NewReader(logUpTo(t, "long_session.jsonl", tc.lastSeq)), tc.taskID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Path != tc.wantPath {
				t.Errorf("Path = %q, want %q", got.Path, tc.wantPath)
			}
			if !slices.Equal(got.ClaudeSessionIDs, []string{longSessionID}) {
				t.Errorf("ClaudeSessionIDs = %q, want [%s]", got.ClaudeSessionIDs, longSessionID)
			}
		})
	}
}

func TestFindTaskOutputTrustsOnlyClaudesOwnLines(t *testing.T) {
	log := []byte(`{"seq":1,"ts":"2026-10-30T10:55:36Z","dir":"out","payload":{"type":"system","subtype":"init","session_id":"s1"}}
{"seq":2,"ts":"2026-10-30T10:55:36Z","dir":"out","payload":{"type":"system","subtype":"task_started","task_id":"t1","tool_use_id":"toolu_1","session_id":"s1"}}
{"seq":3,"ts":"2026-10-30T10:55:36Z","dir":"in","payload":{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"Command running in background with ID: t1. Output is being written to: /x/s1/tasks/t1.output. You will be notified."}]}}}
{"seq":4,"ts":"2026-10-30T10:55:36Z","dir":"out","payload":{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_2","content":"Command running in background with ID: t1. Output is being written to: /y/s1/tasks/t1.output. You will be notified."}]}}}
`)
	got, err := FindTaskOutput(bytes.NewReader(log), "t1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != "" {
		t.Errorf("Path = %q, want none: one result was written by the host, the other answers a different tool_use", got.Path)
	}
}

func TestFindTaskOutputReadsAnArrayToolResult(t *testing.T) {
	log := []byte(`{"seq":1,"ts":"2026-10-30T10:55:36Z","dir":"out","payload":{"type":"system","subtype":"task_started","task_id":"t1","tool_use_id":"toolu_1","session_id":"s1"}}
{"seq":2,"ts":"2026-10-30T10:55:36Z","dir":"out","payload":{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":"Command running in background with ID: t1. Output is being written to: /x/s1/tasks/t1.output. You will be notified."}]}]}}}
`)
	got, err := FindTaskOutput(bytes.NewReader(log), "t1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != "/x/s1/tasks/t1.output" {
		t.Errorf("Path = %q, want /x/s1/tasks/t1.output", got.Path)
	}
}
