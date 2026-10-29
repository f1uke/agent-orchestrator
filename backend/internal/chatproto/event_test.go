package chatproto

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseLineRejects(t *testing.T) {
	for name, line := range map[string]string{
		"invalid json":  `{"type":`,
		"empty":         ``,
		"not an object": `[1,2]`,
		"missing type":  `{"subtype":"init"}`,
		"empty type":    `{"type":""}`,
	} {
		t.Run(name, func(t *testing.T) {
			if ev, err := ParseLine([]byte(line)); err == nil {
				t.Fatalf("ParseLine succeeded: %#v", ev)
			}
		})
	}
}

func TestParseLine(t *testing.T) {
	tests := []struct {
		name string
		line string
		want Event
	}{
		{"invented type", `{"type":"hologram","subtype":"v2","x":1}`,
			Unknown{Type: "hologram", Subtype: "v2", Raw: json.RawMessage(`{"type":"hologram","subtype":"v2","x":1}`)}},
		{"known type with the wrong body shape degrades to unknown", `{"type":"assistant","message":{"content":42}}`,
			Unknown{Type: "assistant", Raw: json.RawMessage(`{"type":"assistant","message":{"content":42}}`)}},
		{"system init", `{"type":"system","subtype":"init","session_id":"s1","model":"m","permissionMode":"plan","cwd":"/w","tools":["Bash"],"slash_commands":["tdd"]}`,
			SystemInit{SessionID: "s1", Model: "m", PermissionMode: "plan", CWD: "/w", Tools: []string{"Bash"}, SlashCommands: []string{"tdd"}}},
		{"system status", `{"type":"system","subtype":"status","session_id":"s1"}`,
			System{Subtype: "status", SessionID: "s1", Raw: json.RawMessage(`{"type":"system","subtype":"status","session_id":"s1"}`)}},
		{"assistant blocks", `{"type":"assistant","uuid":"a1","session_id":"s1","parent_tool_use_id":"toolu_p","message":{"id":"msg_1","content":[` +
			`{"type":"text","text":"hi"},{"type":"thinking","thinking":"hm","signature":"zz"},` +
			`{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls"}},{"type":"server_tool_use","id":"x"}]}}`,
			Assistant{UUID: "a1", SessionID: "s1", ParentToolUseID: "toolu_p", MessageID: "msg_1", Blocks: []Block{
				TextBlock{Text: "hi"},
				ThinkingBlock{Text: "hm"},
				ToolUseBlock{ID: "toolu_1", Name: "Bash", Input: json.RawMessage(`{"command":"ls"}`)},
				OtherBlock{Type: "server_tool_use", Raw: json.RawMessage(`{"type":"server_tool_use","id":"x"}`)},
			}}},
		{"user string content", `{"type":"user","uuid":"u1","isReplay":true,"parent_tool_use_id":null,"message":{"content":"hello"}}`,
			User{UUID: "u1", IsReplay: true, Blocks: []Block{TextBlock{Text: "hello"}}}},
		{"user tool result and image", `{"type":"user","uuid":"u2","message":{"content":[` +
			`{"type":"tool_result","tool_use_id":"toolu_1","is_error":true,"content":"boom"},` +
			`{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA"}}]}}`,
			User{UUID: "u2", Blocks: []Block{
				ToolResultBlock{ToolUseID: "toolu_1", IsError: true, Content: json.RawMessage(`"boom"`)},
				ImageBlock{MediaType: "image/png"},
			}}},
		{"result", `{"type":"result","subtype":"error_during_execution","session_id":"s1","is_error":true,"num_turns":2,"total_cost_usd":0.5,"duration_ms":1200}`,
			Result{Subtype: "error_during_execution", SessionID: "s1", IsError: true, NumTurns: 2, TotalCostUSD: 0.5, DurationMS: 1200}},
		{"text delta", `{"type":"stream_event","session_id":"s1","parent_tool_use_id":null,"event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"ab"}}}`,
			StreamEvent{SessionID: "s1", Delta: Delta{Kind: DeltaText, Index: 1, Text: "ab"}}},
		{"thinking delta", `{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hm"}}}`,
			StreamEvent{Delta: Delta{Kind: DeltaThinking, Text: "hm"}}},
		{"input json delta", `{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"a\""}}}`,
			StreamEvent{Delta: Delta{Kind: DeltaInputJSON, Text: `{"a"`}}},
		{"signature delta is not text", `{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"zz"}}}`,
			StreamEvent{Delta: Delta{Kind: DeltaOther}}},
		{"non-delta stream event", `{"type":"stream_event","event":{"type":"message_start"}}`,
			StreamEvent{Delta: Delta{Kind: DeltaOther}}},
		{"initialize request", `{"type":"control_request","request_id":"r1","request":{"subtype":"initialize"}}`,
			ControlRequest{RequestID: "r1", Body: OtherControl{Subtype: "initialize", Raw: json.RawMessage(`{"subtype":"initialize"}`)}}},
		{"can_use_tool", `{"type":"control_request","request_id":"r2","request":{"subtype":"can_use_tool","tool_name":"Write","tool_use_id":"toolu_1",` +
			`"input":{"file_path":"/a"},"description":"a","decision_reason":"because","blocked_path":"/a","permission_suggestions":[{"type":"setMode"}]}}`,
			ControlRequest{RequestID: "r2", Body: CanUseTool{
				ToolName: "Write", ToolUseID: "toolu_1", Description: "a", DecisionReason: "because", BlockedPath: "/a",
				Input: json.RawMessage(`{"file_path":"/a"}`), Suggestions: []json.RawMessage{json.RawMessage(`{"type":"setMode"}`)},
			}}},
		{"can_use_tool with a structured reason still reaches the host", `{"type":"control_request","request_id":"r3","request":{"subtype":"can_use_tool","tool_name":"Bash","tool_use_id":"toolu_2","input":{},"decision_reason":{"type":"rule"}}}`,
			ControlRequest{RequestID: "r3", Body: CanUseTool{ToolName: "Bash", ToolUseID: "toolu_2", DecisionReason: `{"type":"rule"}`, Input: json.RawMessage(`{}`)}}},
		{"success response", `{"type":"control_response","response":{"subtype":"success","request_id":"r1","response":{"mode":"plan"}}}`,
			ControlResponse{RequestID: "r1", OK: true, Response: json.RawMessage(`{"mode":"plan"}`)}},
		{"error response", `{"type":"control_response","response":{"subtype":"error","request_id":"r1","error":"nope"}}`,
			ControlResponse{RequestID: "r1", Error: "nope"}},
		{"cancel", `{"type":"control_cancel_request","request_id":"r2"}`, ControlCancel{RequestID: "r2"}},
		{"lifecycle", `{"type":"command_lifecycle","command_uuid":"u1","state":"cancelled"}`,
			CommandLifecycle{CommandUUID: "u1", State: CommandCancelled}},
		{"lifecycle keeps a state it does not know", `{"type":"command_lifecycle","command_uuid":"u1","state":"paused"}`,
			CommandLifecycle{CommandUUID: "u1", State: "paused"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseLine([]byte(tt.line))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got  %#v\nwant %#v", got, tt.want)
			}
		})
	}
}

func TestParseLineDoesNotAliasItsInput(t *testing.T) {
	line := []byte(`{"type":"hologram"}`)
	ev, err := ParseLine(line)
	if err != nil {
		t.Fatal(err)
	}
	copy(line, strings.Repeat("x", len(line)))
	if got := string(ev.(Unknown).Raw); got != `{"type":"hologram"}` {
		t.Errorf("Raw changed with the caller's buffer: %s", got)
	}
}

func TestFixturesParseCleanly(t *testing.T) {
	modelled := map[string]bool{
		"system": true, "assistant": true, "user": true, "result": true, "stream_event": true,
		"control_request": true, "control_response": true, "command_lifecycle": true,
	}
	for _, name := range []string{"first_session.jsonl", "resumed_session.jsonl"} {
		for _, e := range loadFixture(t, name) {
			if e.Dir == DirHost {
				continue
			}
			ev, err := ParseLine(e.Payload)
			if err != nil {
				t.Fatalf("%s seq %d: %v", name, e.Seq, err)
			}
			var head struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(e.Payload, &head); err != nil {
				t.Fatal(err)
			}
			if u, isUnknown := ev.(Unknown); isUnknown == modelled[head.Type] {
				t.Errorf("%s seq %d: type %q parsed as %T (unknown=%v)", name, e.Seq, head.Type, ev, u.Type != "")
			}
		}
	}
}

func TestStreamEventsFixture(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "stream_events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	byKind := map[DeltaKind]*strings.Builder{DeltaText: {}, DeltaInputJSON: {}}
	lines := 0
	for sc.Scan() {
		ev, err := ParseLine(sc.Bytes())
		if err != nil {
			t.Fatalf("line %d: %v", lines+1, err)
		}
		se, ok := ev.(StreamEvent)
		if !ok {
			t.Fatalf("line %d: %T, want StreamEvent", lines+1, ev)
		}
		if b := byKind[se.Delta.Kind]; b != nil {
			b.WriteString(se.Delta.Text)
		}
		lines++
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if lines != 40 {
		t.Errorf("parsed %d lines, want 40", lines)
	}

	const prefix, suffix = "I created `hello.txt` in `", "`. It contains the single word \"hi\"."
	text := byKind[DeltaText].String()
	if !strings.HasPrefix(text, prefix) || !strings.HasSuffix(text, suffix) {
		t.Errorf("joined text deltas = %q", text)
	}
	if reply := firstAssistantText(t, loadFixture(t, "first_session.jsonl")); !strings.HasPrefix(reply, prefix) || !strings.HasSuffix(reply, suffix) {
		t.Errorf("first_session reply = %q", reply)
	}

	var write struct {
		FilePath string `json:"file_path"`
		Content  string `json:"content"`
	}
	if err := json.NewDecoder(strings.NewReader(byKind[DeltaInputJSON].String())).Decode(&write); err != nil {
		t.Fatalf("partial_json deltas do not join into JSON: %v", err)
	}
	if !strings.HasSuffix(write.FilePath, "/hello.txt") || write.Content != "hi" {
		t.Errorf("joined input = %+v", write)
	}
}

func firstAssistantText(t *testing.T, entries []Entry) string {
	t.Helper()
	for _, e := range entries {
		if e.Dir != DirOut {
			continue
		}
		ev, err := ParseLine(e.Payload)
		if err != nil {
			t.Fatal(err)
		}
		if a, ok := ev.(Assistant); ok {
			for _, b := range a.Blocks {
				if tb, ok := b.(TextBlock); ok {
					return tb.Text
				}
			}
		}
	}
	t.Fatal("no assistant text in log")
	return ""
}
