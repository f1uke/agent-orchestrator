package sessionhost

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
)

// The host serves one MCP server to claude over the control protocol
// (initialize.sdkMcpServers plus mcp_message requests). Its tools only ask the
// chat view to draw something; the view renders from the tool_use input in the
// log, so a call's answer is a short acknowledgement.
const mcpServerName = "ao"

// maxWidgetCode bounds show_widget's widget_code. The code is logged once, in
// the assistant message that carries the tool_use.
const maxWidgetCode = 128 << 10

//go:embed widget_guide.md
var widgetGuide string

type mcpTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Meta        map[string]any  `json:"_meta"`
	reply       func(args map[string]any) (string, error)
}

var mcpTools = []mcpTool{
	{
		Name: "read_me",
		Description: "Returns the rules for show_widget: the CSS variables the chat provides, sizing, sendPrompt, and which hosts a widget may load from. " +
			"Call it once before your first show_widget call in a session. Do not mention the call to the user.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		reply:       func(map[string]any) (string, error) { return widgetGuide, nil },
	},
	{
		Name: "show_widget",
		Description: "Draws SVG or an HTML fragment inline in the chat, in a sandboxed frame: a diagram, chart, calculator, mockup or other interactive visual. " +
			"widget_code starting with <svg is drawn as SVG; anything else is an HTML fragment (no doctype, html, head or body). " +
			"A global sendPrompt(text) puts text in the user's message box for them to send. Call read_me first. " +
			"Do not repeat the visual's content in your reply.",
		InputSchema: json.RawMessage(fmt.Sprintf(`{"type":"object","properties":{`+
			`"title":{"type":"string","description":"Short snake_case name that tells this visual apart from others in the chat, e.g. q4_revenue_by_region."},`+
			`"loading_messages":{"type":"array","items":{"type":"string"},"minItems":1,"maxItems":4,"description":"1-4 short lines shown while the visual loads, in the user's language."},`+
			`"widget_code":{"type":"string","maxLength":%d,"description":"The SVG or HTML fragment."}},`+
			`"required":["title","widget_code"],"additionalProperties":false}`, maxWidgetCode)),
		reply: func(args map[string]any) (string, error) {
			code, _ := args["widget_code"].(string)
			switch {
			case strings.TrimSpace(code) == "":
				return "", fmt.Errorf("widget_code is empty")
			case len(code) > maxWidgetCode:
				return "", fmt.Errorf("widget_code is %d bytes; the limit is %d. Make it smaller", len(code), maxWidgetCode)
			}
			return "Shown to the user. Do not repeat its content in text.", nil
		},
	},
	{
		Name: "mark_chapter",
		Description: "Starts a new chapter of this session. The user sees a divider and a table of contents to jump between chapters. " +
			"Call it when the work moves to a different phase, such as from exploring to implementing, or from a fix to verifying it. " +
			"A typical session has 3-8 chapters; do not mark one for the first message.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{` +
			`"title":{"type":"string","maxLength":40,"description":"Short noun phrase, e.g. Auth bug fix."},` +
			`"summary":{"type":"string","maxLength":200,"description":"Optional one line shown on hover."}},` +
			`"required":["title"],"additionalProperties":false}`),
		reply: func(args map[string]any) (string, error) {
			return fmt.Sprintf("Chapter marked: %q. Continue your work.", args["title"]), nil
		},
	},
	{
		Name: "wrap_up",
		Description: "Fills the box shown at the end of your turn: a short recap and, when the next move is the user's decision, one question with 1-2 reply buttons. " +
			"A button puts its label in the user's message box; it does not act by itself. " +
			"Call it once at the end of a turn that changed files, ran commands or did multi-step work, before your closing message. Skip it for short answers.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{` +
			`"recap":{"type":"string","maxLength":300,"description":"Two or three short sentences to the user: what changed and what is not done or not verified."},` +
			`"ask":{"type":"string","maxLength":100,"description":"One question, only when the user must decide something."},` +
			`"replies":{"type":"array","maxItems":2,"items":{"type":"string","maxLength":24},"description":"Button labels that answer the question, your recommendation first."}},` +
			`"required":["recap"],"additionalProperties":false}`),
		reply: func(map[string]any) (string, error) {
			return "Box shown. Write your closing message now.", nil
		},
	},
	{
		Name: "suggest_session",
		Description: "Shows a card that offers to move a separate piece of work into a new session. The user decides; the card only opens the new-session form filled in. " +
			"Use it when the user asks for something unrelated to the current task.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{` +
			`"title":{"type":"string","maxLength":60,"description":"Names the work."},` +
			`"prompt":{"type":"string","maxLength":4000,"description":"The brief the new session would start with."}},` +
			`"required":["title","prompt"],"additionalProperties":false}`),
		reply: func(map[string]any) (string, error) {
			return "Card shown. Carry on with the current task unless the user says otherwise.", nil
		},
	},
	{
		Name: "suggest_skill",
		Description: "Shows a card that suggests one of the user's installed skills for the task. The card only puts /<name> in the user's message box. " +
			"Use it when an installed skill fits the task and the user has not used it. Do not suggest a skill twice in a session.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{` +
			`"name":{"type":"string","maxLength":100,"description":"The skill's name, without the slash."},` +
			`"reason":{"type":"string","maxLength":200,"description":"One line on why it fits."}},` +
			`"required":["name","reason"],"additionalProperties":false}`),
		reply: func(map[string]any) (string, error) {
			return "Card shown. Carry on.", nil
		},
	},
}

// mcpAllowedTools are the host's tools in the form claude names them. The
// host lists exactly these in --allowedTools: they only draw in the chat view,
// so they need no permission prompt.
func mcpAllowedTools() string {
	names := make([]string, len(mcpTools))
	for i, t := range mcpTools {
		names[i] = "mcp__" + mcpServerName + "__" + t.Name
	}
	return strings.Join(names, ",")
}

type rpcMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// mcpReply answers one JSON-RPC message claude sent to the host's MCP server.
// A notification gets the empty result the official SDK sends for one.
func mcpReply(server string, message json.RawMessage) json.RawMessage {
	var m rpcMessage
	if err := json.Unmarshal(message, &m); err != nil {
		return rpcReply(json.RawMessage("null"), nil, &rpcError{Code: -32700, Message: "parse error"})
	}
	if len(m.ID) == 0 {
		return rpcReply(json.RawMessage("0"), struct{}{}, nil)
	}
	if server != mcpServerName {
		return rpcReply(m.ID, nil, &rpcError{Code: -32601, Message: "unknown server " + server})
	}
	switch m.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(m.Params, &p)
		return rpcReply(m.ID, map[string]any{
			"protocolVersion": p.ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": mcpServerName, "version": "1"},
		}, nil)
	case "ping":
		return rpcReply(m.ID, struct{}{}, nil)
	case "tools/list":
		return rpcReply(m.ID, map[string]any{"tools": listedTools()}, nil)
	case "tools/call":
		return rpcReply(m.ID, callTool(m.Params), nil)
	}
	return rpcReply(m.ID, nil, &rpcError{Code: -32601, Message: "method not found: " + m.Method})
}

func listedTools() []mcpTool {
	out := make([]mcpTool, len(mcpTools))
	for i, t := range mcpTools {
		t.Meta = map[string]any{"anthropic/alwaysLoad": true}
		out[i] = t
	}
	return out
}

func callTool(params json.RawMessage) map[string]any {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return toolResult("bad tools/call params: "+err.Error(), true)
	}
	for _, t := range mcpTools {
		if t.Name == p.Name {
			text, err := t.reply(p.Arguments)
			if err != nil {
				return toolResult(err.Error(), true)
			}
			return toolResult(text, false)
		}
	}
	return toolResult("unknown tool "+p.Name, true)
}

func toolResult(text string, isError bool) map[string]any {
	r := map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}
	if isError {
		r["isError"] = true
	}
	return r
}

func rpcReply(id json.RawMessage, result any, e *rpcError) json.RawMessage {
	msg := map[string]any{"jsonrpc": "2.0", "id": id}
	if e != nil {
		msg["error"] = e
	} else {
		msg["result"] = result
	}
	b, _ := json.Marshal(msg)
	return b
}

type mcpCall struct {
	requestID string
	server    string
	message   json.RawMessage
	logLine   []byte
}

// mcpRequest picks out an mcp_message control request. Its log line drops the
// tools/call arguments: the same input is already logged in the assistant
// message's tool_use, and a widget's code can be large.
func mcpRequest(ev chatproto.Event) (mcpCall, bool) {
	req, ok := ev.(chatproto.ControlRequest)
	if !ok {
		return mcpCall{}, false
	}
	other, ok := req.Body.(chatproto.OtherControl)
	if !ok || other.Subtype != "mcp_message" {
		return mcpCall{}, false
	}
	var body struct {
		ServerName string          `json:"server_name"`
		Message    json.RawMessage `json:"message"`
	}
	if json.Unmarshal(other.Raw, &body) != nil {
		return mcpCall{}, false
	}
	var m rpcMessage
	_ = json.Unmarshal(body.Message, &m)
	logged := map[string]any{"jsonrpc": "2.0", "method": m.Method}
	if len(m.ID) > 0 {
		logged["id"] = m.ID
	}
	if m.Method == "tools/call" {
		var p struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(m.Params, &p)
		logged["params"] = map[string]any{"name": p.Name}
	} else if len(m.Params) > 0 {
		logged["params"] = m.Params
	}
	line, _ := json.Marshal(map[string]any{
		"type":       "control_request",
		"request_id": req.RequestID,
		"request":    map[string]any{"subtype": "mcp_message", "server_name": body.ServerName, "message": logged},
	})
	return mcpCall{requestID: req.RequestID, server: body.ServerName, message: body.Message, logLine: line}, true
}
