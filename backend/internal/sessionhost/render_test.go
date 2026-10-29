package sessionhost

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
)

func TestRenderPane(t *testing.T) {
	tests := []struct {
		name    string
		dir     chatproto.Direction
		payload string
		want    []string
	}{
		{"user input", chatproto.DirIn,
			`{"type":"user","uuid":"u1","message":{"role":"user","content":"fix the bug\nplease"}}`,
			[]string{cyan("> fix the bug"), cyan("  please")}},
		{"user input with an image", chatproto.DirIn,
			`{"type":"user","uuid":"u1","message":{"role":"user","content":[{"type":"text","text":"look"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA"}}]}}`,
			[]string{cyan("> look"), cyan("  [1 image(s)]")}},
		{"assistant text", chatproto.DirOut,
			`{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"Done.\nAll good."}]}}`,
			[]string{"Done.", "All good."}},
		{"tool call", chatproto.DirOut,
			`{"type":"assistant","message":{"id":"m1","content":[{"type":"tool_use","id":"t1","name":"Write","input":{"content":"hi","file_path":"/w/hello.txt"}}]}}`,
			[]string{"● Write(/w/hello.txt)"}},
		{"tool call with no string argument", chatproto.DirOut,
			`{"type":"assistant","message":{"id":"m1","content":[{"type":"tool_use","id":"t1","name":"AskUserQuestion","input":{"questions":[]}}]}}`,
			[]string{"● AskUserQuestion"}},
		{"tool call with a long multi-line argument", chatproto.DirOut,
			`{"type":"assistant","message":{"id":"m1","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"echo one\necho two"}}]}}`,
			[]string{"● Bash(echo one…)"}},
		{"tool result ok", chatproto.DirOut,
			`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"File created"}]}}`,
			[]string{dim("  ⎿ ok")}},
		{"tool result error", chatproto.DirOut,
			`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":[{"type":"text","text":"permission denied\nmore detail"}]}]}}`,
			[]string{red("  ⎿ error: permission denied…")}},
		{"replayed user input", chatproto.DirOut,
			`{"type":"user","uuid":"u1","isReplay":true,"message":{"content":"fix the bug"}}`,
			nil},
		{"permission request", chatproto.DirOut,
			`{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Bash","tool_use_id":"t1","input":{"command":"rm hello.txt"}}}`,
			[]string{yellow("? Permission: Bash(rm hello.txt) waiting")}},
		{"permission allowed", chatproto.DirIn,
			`{"type":"control_response","response":{"subtype":"success","request_id":"r1","response":{"behavior":"allow","updatedInput":{}}}}`,
			[]string{dim("  → allowed")}},
		{"permission denied", chatproto.DirIn,
			`{"type":"control_response","response":{"subtype":"success","request_id":"r1","response":{"behavior":"deny","message":"not that file"}}}`,
			[]string{dim("  → denied: not that file")}},
		{"claude's echo of an answer", chatproto.DirOut,
			`{"type":"control_response","response":{"subtype":"success","request_id":"r1","response":{"behavior":"allow","updatedInput":{}}}}`,
			nil},
		{"turn end", chatproto.DirOut,
			`{"type":"result","subtype":"success","total_cost_usd":0.0368}`,
			[]string{dim("· done, $0.04")}},
		{"turn end with an error", chatproto.DirOut,
			`{"type":"result","subtype":"error_during_execution","is_error":true,"total_cost_usd":0.1}`,
			[]string{dim("· error_during_execution, $0.10")}},
		{"interrupt", chatproto.DirOut,
			`{"type":"user","message":{"content":[{"type":"text","text":"[Request interrupted by user]"}]}}`,
			[]string{red("! interrupted")}},
		{"compaction", chatproto.DirOut,
			`{"type":"system","subtype":"compact_boundary","session_id":"s"}`,
			[]string{dim("· compacted")}},
		{"other system event", chatproto.DirOut,
			`{"type":"system","subtype":"status","status":"thinking"}`,
			nil},
		{"spawn", chatproto.DirHost,
			`{"kind":"spawn","argv":["claude"],"resume":"","pid":4}`,
			[]string{dim("── claude started")}},
		{"resume", chatproto.DirHost,
			`{"kind":"spawn","argv":["claude"],"resume":"s-1","pid":4}`,
			[]string{dim("── claude resumed s-1")}},
		{"exit", chatproto.DirHost,
			`{"kind":"exit","code":0,"signal":null}`,
			[]string{dim("── claude exited 0")}},
		{"exit on a signal", chatproto.DirHost,
			`{"kind":"exit","code":null,"signal":"SIGKILL"}`,
			[]string{dim("── claude exited on SIGKILL")}},
		{"non-JSON stdout", chatproto.DirHost,
			`{"kind":"non_json","text":"Warning: something"}`,
			[]string{dim("Warning: something")}},
		{"user origin", chatproto.DirHost,
			`{"kind":"user_origin","uuid":"u1","origin":{"via":"ao_send","from":"w-1"}}`,
			nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RenderPane(chatproto.Entry{Seq: 1, Dir: tt.dir, Payload: json.RawMessage(tt.payload)})
			if !slices.Equal(got, tt.want) {
				t.Errorf("RenderPane = %q, want %q", got, tt.want)
			}
		})
	}
}
