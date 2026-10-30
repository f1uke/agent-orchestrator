package sessionhost

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
)

func mcpMessage(id, message string) string {
	return fmt.Sprintf(`{"type":"control_request","request_id":%q,"request":{"subtype":"mcp_message","server_name":"ao","message":%s}}`, id, message)
}

const mcpReplyLine = `{"type":"control_response","response":{"subtype":"success","request_id":"x","response":{}}}`

// mcpReplies returns the mcp_response of every reply the host logged, by request id.
func mcpReplies(t *testing.T, entries []chatproto.Entry) map[string]json.RawMessage {
	t.Helper()
	out := map[string]json.RawMessage{}
	for _, e := range entries {
		if e.Dir != chatproto.DirIn {
			continue
		}
		var w struct {
			Response struct {
				RequestID string `json:"request_id"`
				Response  struct {
					MCP json.RawMessage `json:"mcp_response"`
				} `json:"response"`
			} `json:"response"`
		}
		if json.Unmarshal(e.Payload, &w) == nil && w.Response.Response.MCP != nil {
			out[w.Response.RequestID] = w.Response.Response.MCP
		}
	}
	return out
}

func TestHostServesItsMCPTools(t *testing.T) {
	dir := shortDir(t)
	widget := `<svg viewBox="0 0 10 10"><rect width="10" height="10"/></svg>` + strings.Repeat(" ", 4096)
	call := func(id, name string, args any) string {
		a, _ := json.Marshal(args)
		return mcpMessage(id, fmt.Sprintf(`{"jsonrpc":"2.0","id":%q,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, id, name, a))
	}
	fixture := waitingFixture(t, dir,
		fixtureLine{chatproto.DirOut, mcpMessage("m1", `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)},
		fixtureLine{chatproto.DirIn, mcpReplyLine},
		fixtureLine{chatproto.DirOut, mcpMessage("m2", `{"jsonrpc":"2.0","method":"notifications/initialized"}`)},
		fixtureLine{chatproto.DirIn, mcpReplyLine},
		fixtureLine{chatproto.DirOut, mcpMessage("m3", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)},
		fixtureLine{chatproto.DirIn, mcpReplyLine},
		fixtureLine{chatproto.DirOut, call("m4", "show_widget", map[string]any{"title": "flow", "widget_code": widget})},
		fixtureLine{chatproto.DirIn, mcpReplyLine},
		fixtureLine{chatproto.DirOut, call("m5", "show_widget", map[string]any{"title": "huge", "widget_code": strings.Repeat("x", maxWidgetCode+1)})},
		fixtureLine{chatproto.DirIn, mcpReplyLine},
		fixtureLine{chatproto.DirOut, call("m6", "read_me", map[string]any{})},
		fixtureLine{chatproto.DirIn, mcpReplyLine},
	)
	cfg, _ := fakeConfig(t, dir, fixture)
	startHost(t, cfg)
	var replies map[string]json.RawMessage
	eventually(t, "six mcp replies", func() bool {
		replies = mcpReplies(t, loadEntries(t, cfg.LogPath))
		return len(replies) == 6
	})

	var init struct {
		Result struct {
			ProtocolVersion string         `json:"protocolVersion"`
			Capabilities    map[string]any `json:"capabilities"`
		} `json:"result"`
	}
	if err := json.Unmarshal(replies["m1"], &init); err != nil || init.Result.ProtocolVersion != "2025-06-18" || init.Result.Capabilities["tools"] == nil {
		t.Errorf("initialize reply = %s, want the client's protocol version and a tools capability", replies["m1"])
	}

	var list struct {
		ID     int `json:"id"`
		Result struct {
			Tools []struct {
				Name        string          `json:"name"`
				InputSchema json.RawMessage `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(replies["m3"], &list); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range list.Result.Tools {
		names = append(names, tool.Name)
		if !json.Valid(tool.InputSchema) {
			t.Errorf("tool %s has an invalid input schema", tool.Name)
		}
	}
	if got, want := strings.Join(names, ","), "read_me,show_widget,mark_chapter,wrap_up,suggest_session,suggest_skill"; got != want || list.ID != 1 {
		t.Errorf("tools/list = id %d %s, want id 1 %s", list.ID, got, want)
	}

	if text, isErr := toolText(t, replies["m4"]); isErr || !strings.Contains(text, "Shown to the user") {
		t.Errorf("show_widget reply = %q (error %v), want the acknowledgement", text, isErr)
	}
	if text, isErr := toolText(t, replies["m5"]); !isErr || !strings.Contains(text, "limit") {
		t.Errorf("oversized show_widget reply = %q (error %v), want an error naming the limit", text, isErr)
	}
	if text, _ := toolText(t, replies["m6"]); !strings.Contains(text, "sendPrompt") {
		t.Errorf("read_me reply does not explain sendPrompt: %q", text)
	}

	for _, e := range loadEntries(t, cfg.LogPath) {
		if e.Dir == chatproto.DirOut && strings.Contains(string(e.Payload), `"mcp_message"`) && strings.Contains(string(e.Payload), "widget_code") {
			t.Errorf("seq %d logs the widget code a second time: %.200s", e.Seq, e.Payload)
		}
	}
}

func toolText(t *testing.T, reply json.RawMessage) (string, bool) {
	t.Helper()
	var r struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(reply, &r); err != nil || len(r.Result.Content) != 1 {
		t.Fatalf("tool reply %s is not one text block", reply)
	}
	return r.Result.Content[0].Text, r.Result.IsError
}

func TestHostAsksForItsMCPServerAndAllowsOnlyItsTools(t *testing.T) {
	dir := shortDir(t)
	cfg, argvFile := fakeConfig(t, dir, waitingFixture(t, dir))
	startHost(t, cfg)
	eventually(t, "initialize logged", func() bool { return len(loadEntries(t, cfg.LogPath)) >= 3 })

	var init struct {
		Request struct {
			SDKMCPServers []string `json:"sdkMcpServers"`
		} `json:"request"`
	}
	for _, e := range loadEntries(t, cfg.LogPath) {
		if e.Dir == chatproto.DirIn && messageKind(e.Payload) == "control_request/initialize" {
			_ = json.Unmarshal(e.Payload, &init)
		}
	}
	if strings.Join(init.Request.SDKMCPServers, ",") != "ao" {
		t.Errorf("initialize sdkMcpServers = %v, want [ao]", init.Request.SDKMCPServers)
	}

	argv := readArgv(t, argvFile)
	var allowed []string
	for i, a := range argv {
		if a == "--allowedTools" && i+1 < len(argv) {
			allowed = append(allowed, strings.Split(argv[i+1], ",")...)
		}
	}
	if len(allowed) != len(mcpTools) {
		t.Errorf("--allowedTools = %v, want the %d ao tools", allowed, len(mcpTools))
	}
	for _, name := range allowed {
		if !strings.HasPrefix(name, "mcp__ao__") {
			t.Errorf("--allowedTools names %q, which is not one of the host's own tools", name)
		}
	}
}

// A claude that predates SDK MCP servers may refuse the initialize field. The
// session still starts: the prompt goes out and the turn runs, with no ao tools.
func TestSessionRunsWhenClaudeRefusesSDKMCPServers(t *testing.T) {
	dir := shortDir(t)
	first := loadEntries(t, fixturePath(t, "first_session.jsonl"))
	var lines []fixtureLine
	for _, e := range first[1:20] {
		p := string(e.Payload)
		if e.Seq == 3 {
			var w struct {
				Response struct {
					RequestID string `json:"request_id"`
				} `json:"response"`
			}
			_ = json.Unmarshal(e.Payload, &w)
			p = fmt.Sprintf(`{"type":"control_response","response":{"subtype":"error","request_id":%q,"error":"initialize: unknown field sdkMcpServers"}}`, w.Response.RequestID)
		}
		lines = append(lines, fixtureLine{e.Dir, p})
	}
	fixture := dir + "/old.jsonl"
	writeFixture(t, fixture, lines)
	cfg, _ := fakeConfig(t, dir, fixture)
	cfg.PromptFile = writePrompt(t, dir, first)
	startHost(t, cfg)
	var pending PendingRequest
	eventually(t, "the turn's permission request", func() bool {
		st := state(t, cfg.SocketPath)
		if len(st.Pending) == 1 {
			pending = st.Pending[0]
		}
		return len(st.Pending) == 1
	})
	callOK(t, cfg.SocketPath, map[string]any{"op": "answer", "request_id": pending.RequestID, "behavior": "allow"})
	eventually(t, "the turn ends", func() bool {
		tr, err := chatproto.Fold(loadEntries(t, cfg.LogPath))
		return err == nil && turnEnds(tr) == 1
	})
}
