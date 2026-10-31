package chatproto

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestEncode(t *testing.T) {
	input := json.RawMessage(`{"file_path":"/a","content":"hi"}`)
	perm := json.RawMessage(`{"type":"setMode","mode":"acceptEdits","destination":"session"}`)
	tests := []struct {
		name string
		in   Outbound
		want string
	}{
		{"text input", UserInput{UUID: "u1", Text: "hello"},
			`{"type":"user","uuid":"u1","message":{"role":"user","content":"hello"},"parent_tool_use_id":null,"session_id":""}`},
		{"text with image", UserInput{UUID: "u1", Text: "look", Images: []Image{{MediaType: "image/png", Base64: "AAAA"}}},
			`{"type":"user","uuid":"u1","message":{"role":"user","content":[{"type":"text","text":"look"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}]},"parent_tool_use_id":null,"session_id":""}`},
		{"image only", UserInput{UUID: "u1", Images: []Image{{MediaType: "image/jpeg", Base64: "BBBB"}}},
			`{"type":"user","uuid":"u1","message":{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/jpeg","data":"BBBB"}}]},"parent_tool_use_id":null,"session_id":""}`},
		{"initialize", Initialize{RequestID: "r1"}, `{"type":"control_request","request_id":"r1","request":{"subtype":"initialize"}}`},
		{"initialize with sdk mcp servers", Initialize{RequestID: "r1", SDKMCPServers: []string{"ao"}},
			`{"type":"control_request","request_id":"r1","request":{"sdkMcpServers":["ao"],"subtype":"initialize"}}`},
		{"mcp reply", MCPReply{RequestID: "r7", Message: json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{}}`)},
			`{"type":"control_response","response":{"request_id":"r7","response":{"mcp_response":{"jsonrpc":"2.0","id":1,"result":{}}},"subtype":"success"}}`},
		{"interrupt", Interrupt{RequestID: "r2"}, `{"type":"control_request","request_id":"r2","request":{"subtype":"interrupt"}}`},
		{"set permission mode", SetPermissionMode{RequestID: "r3", Mode: ModePlan},
			`{"type":"control_request","request_id":"r3","request":{"subtype":"set_permission_mode","mode":"plan"}}`},
		{"set model", SetModel{RequestID: "r6", Model: "haiku"},
			`{"type":"control_request","request_id":"r6","request":{"subtype":"set_model","model":"haiku"}}`},
		{"get context usage", GetContextUsage{RequestID: "ctx-1"},
			`{"type":"control_request","request_id":"ctx-1","request":{"subtype":"get_context_usage"}}`},
		{"allow", PermissionAnswer{RequestID: "r4", Decision: Allow{UpdatedInput: input}},
			`{"type":"control_response","response":{"subtype":"success","request_id":"r4","response":{"behavior":"allow","updatedInput":{"file_path":"/a","content":"hi"}}}}`},
		{"allow with permissions", PermissionAnswer{RequestID: "r4", Decision: Allow{UpdatedInput: input, UpdatedPermissions: []json.RawMessage{perm}}},
			`{"type":"control_response","response":{"subtype":"success","request_id":"r4","response":{"behavior":"allow","updatedInput":{"file_path":"/a","content":"hi"},"updatedPermissions":[{"type":"setMode","mode":"acceptEdits","destination":"session"}]}}}`},
		{"deny", PermissionAnswer{RequestID: "r5", Decision: Deny{Message: "no"}},
			`{"type":"control_response","response":{"subtype":"success","request_id":"r5","response":{"behavior":"deny","message":"no"}}}`},
		{"deny and interrupt", PermissionAnswer{RequestID: "r5", Decision: Deny{Message: "stop", Interrupt: true}},
			`{"type":"control_response","response":{"subtype":"success","request_id":"r5","response":{"behavior":"deny","message":"stop","interrupt":true}}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Encode(tt.in)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(got), "\n") {
				t.Errorf("encoded line contains a newline: %q", got)
			}
			assertJSONEqual(t, got, []byte(tt.want))
		})
	}
}

func TestEncodeRejects(t *testing.T) {
	tests := []struct {
		name string
		in   Outbound
	}{
		{"nil", nil},
		{"input without uuid", UserInput{Text: "x"}},
		{"empty input", UserInput{UUID: "u"}},
		{"image without data", UserInput{UUID: "u", Images: []Image{{MediaType: "image/png"}}}},
		{"initialize without id", Initialize{}},
		{"interrupt without id", Interrupt{}},
		{"unknown mode", SetPermissionMode{RequestID: "r", Mode: "yolo"}},
		{"empty mode", SetPermissionMode{RequestID: "r"}},
		{"set model without id", SetModel{Model: "haiku"}},
		{"set model without model", SetModel{RequestID: "r"}},
		{"get context usage without id", GetContextUsage{}},
		{"answer without id", PermissionAnswer{Decision: Deny{}}},
		{"answer without decision", PermissionAnswer{RequestID: "r"}},
		{"allow without input", PermissionAnswer{RequestID: "r", Decision: Allow{}}},
		{"mcp reply without id", MCPReply{Message: json.RawMessage(`{}`)}},
		{"mcp reply without message", MCPReply{RequestID: "r"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := Encode(tt.in); err == nil {
				t.Fatalf("Encode succeeded: %s", got)
			}
		})
	}
}

func outboundFromWire(payload json.RawMessage) (Outbound, error) {
	var w struct {
		Type    string `json:"type"`
		UUID    string `json:"uuid"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		RequestID string `json:"request_id"`
		Request   struct {
			Subtype string `json:"subtype"`
			Mode    string `json:"mode"`
			Model   string `json:"model"`
		} `json:"request"`
		Response struct {
			RequestID string `json:"request_id"`
			Response  struct {
				Behavior           string            `json:"behavior"`
				Message            string            `json:"message"`
				UpdatedInput       json.RawMessage   `json:"updatedInput"`
				UpdatedPermissions []json.RawMessage `json:"updatedPermissions"`
			} `json:"response"`
		} `json:"response"`
	}
	if err := json.Unmarshal(payload, &w); err != nil {
		return nil, err
	}
	switch w.Type {
	case "user":
		return UserInput{UUID: w.UUID, Text: w.Message.Content}, nil
	case "control_request":
		switch w.Request.Subtype {
		case "initialize":
			return Initialize{RequestID: w.RequestID}, nil
		case "interrupt":
			return Interrupt{RequestID: w.RequestID}, nil
		case "set_permission_mode":
			mode, err := ParsePermissionMode(w.Request.Mode)
			return SetPermissionMode{RequestID: w.RequestID, Mode: mode}, err
		case "set_model":
			return SetModel{RequestID: w.RequestID, Model: w.Request.Model}, nil
		}
	case "control_response":
		r := w.Response.Response
		switch r.Behavior {
		case "allow":
			return PermissionAnswer{RequestID: w.Response.RequestID, Decision: Allow{UpdatedInput: r.UpdatedInput, UpdatedPermissions: r.UpdatedPermissions}}, nil
		case "deny":
			return PermissionAnswer{RequestID: w.Response.RequestID, Decision: Deny{Message: r.Message}}, nil
		}
	}
	return nil, fmt.Errorf("unrecognised host message: %s", payload)
}

func TestEncodeMatchesWhatTheCLIAccepted(t *testing.T) {
	seen := map[string]int{}
	for _, name := range []string{"first_session.jsonl", "resumed_session.jsonl", "set_model.jsonl"} {
		for _, e := range loadFixture(t, name) {
			if e.Dir != DirIn {
				continue
			}
			o, err := outboundFromWire(e.Payload)
			if err != nil {
				t.Fatalf("%s seq %d: %v", name, e.Seq, err)
			}
			seen[reflect.TypeOf(o).Name()]++
			got, err := Encode(o)
			if err != nil {
				t.Fatalf("%s seq %d: Encode(%T): %v", name, e.Seq, o, err)
			}
			assertJSONEqual(t, got, e.Payload)
		}
	}
	want := map[string]int{"UserInput": 10, "Initialize": 2, "SetPermissionMode": 1, "Interrupt": 1, "PermissionAnswer": 6, "SetModel": 2}
	if !reflect.DeepEqual(seen, want) {
		t.Errorf("fixtures exercised %v, want %v", seen, want)
	}
}

func TestWithAnswers(t *testing.T) {
	t.Run("reproduces the input the CLI accepted", func(t *testing.T) {
		var request, answered json.RawMessage
		for _, e := range loadFixture(t, "first_session.jsonl") {
			if e.Dir == DirHost {
				continue
			}
			ev, err := ParseLine(e.Payload)
			if err != nil {
				t.Fatal(err)
			}
			if r, ok := ev.(ControlRequest); ok && e.Dir == DirOut {
				if c, ok := r.Body.(CanUseTool); ok && c.ToolName == "AskUserQuestion" {
					request = c.Input
				}
			}
			if r, ok := ev.(ControlResponse); ok && e.Dir == DirIn && request != nil && answered == nil {
				var body struct {
					UpdatedInput json.RawMessage `json:"updatedInput"`
				}
				if err := json.Unmarshal(r.Response, &body); err != nil {
					t.Fatal(err)
				}
				answered = body.UpdatedInput
			}
		}
		if request == nil || answered == nil {
			t.Fatal("fixture has no AskUserQuestion exchange")
		}
		got, err := WithAnswers(request, map[string]string{
			"Which colour do you prefer?": "Red",
			"Which snacks do you like?":   "Chips, Fruit",
		})
		if err != nil {
			t.Fatal(err)
		}
		assertJSONEqual(t, got, answered)
	})

	t.Run("nil answers still sends an answers object", func(t *testing.T) {
		got, err := WithAnswers(json.RawMessage(`{"questions":[]}`), nil)
		if err != nil {
			t.Fatal(err)
		}
		assertJSONEqual(t, got, []byte(`{"questions":[],"answers":{}}`))
	})

	for _, bad := range []string{`null`, `[]`, `"x"`, ``, `{`} {
		t.Run("rejects "+bad, func(t *testing.T) {
			if got, err := WithAnswers(json.RawMessage(bad), map[string]string{"q": "a"}); err == nil {
				t.Fatalf("succeeded: %s", got)
			}
		})
	}
}

func TestParsePermissionMode(t *testing.T) {
	for _, ok := range []string{"default", "acceptEdits", "plan", "bypassPermissions", "auto", "dontAsk"} {
		if m, err := ParsePermissionMode(ok); err != nil || string(m) != ok {
			t.Errorf("ParsePermissionMode(%q) = %q, %v", ok, m, err)
		}
	}
	for _, bad := range []string{"", "yolo", "Plan", "accept_edits"} {
		if m, err := ParsePermissionMode(bad); err == nil {
			t.Errorf("ParsePermissionMode(%q) = %q, want an error", bad, m)
		}
	}
}
