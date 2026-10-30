package chatproto

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Outbound is a message a host writes to claude's stdin.
type Outbound interface{ isOutbound() }

// UserInput is a user message. Without Images it is sent as a string; with
// images it is sent as a content block array.
type UserInput struct {
	UUID   string
	Text   string
	Images []Image
}

// Image is an inline base64 image attached to a UserInput.
type Image struct {
	MediaType string
	Base64    string
}

// Initialize is the control request that opens a session. SDKMCPServers names
// MCP servers the host serves itself over mcp_message control requests.
type Initialize struct {
	RequestID     string
	SDKMCPServers []string
}

// Interrupt stops the running turn.
type Interrupt struct{ RequestID string }

// SetPermissionMode changes the permission mode of the running session.
type SetPermissionMode struct {
	RequestID string
	Mode      PermissionMode
}

// SetModel switches the running session to Model, one of the values the
// initialize response lists or a full model id. Claude answers with the
// resolved model id, or an error for a model it does not know.
type SetModel struct {
	RequestID string
	Model     string
}

// MCPReply answers an mcp_message control request with one JSON-RPC message.
type MCPReply struct {
	RequestID string
	Message   json.RawMessage
}

// PermissionAnswer answers a can_use_tool request.
type PermissionAnswer struct {
	RequestID string
	Decision  Decision
}

func (UserInput) isOutbound()         {}
func (Initialize) isOutbound()        {}
func (Interrupt) isOutbound()         {}
func (SetPermissionMode) isOutbound() {}
func (SetModel) isOutbound()          {}
func (PermissionAnswer) isOutbound()  {}
func (MCPReply) isOutbound()          {}

// Decision is the verdict in a PermissionAnswer.
type Decision interface{ isDecision() }

// Allow lets the tool run. UpdatedInput is required by the CLI: pass the
// request's own input to leave it unchanged, or an edited input to change it.
type Allow struct {
	UpdatedInput       json.RawMessage
	UpdatedPermissions []json.RawMessage
}

// Deny refuses the tool and tells the model why. Interrupt also ends the turn.
type Deny struct {
	Message   string
	Interrupt bool
}

func (Allow) isDecision() {}
func (Deny) isDecision()  {}

// PermissionMode is a Claude Code permission mode.
type PermissionMode string

// Permission modes accepted by claude.
const (
	ModeDefault           PermissionMode = "default"
	ModeAcceptEdits       PermissionMode = "acceptEdits"
	ModePlan              PermissionMode = "plan"
	ModeBypassPermissions PermissionMode = "bypassPermissions"
	ModeAuto              PermissionMode = "auto"
	ModeDontAsk           PermissionMode = "dontAsk"
)

// ParsePermissionMode validates a mode name.
func ParsePermissionMode(s string) (PermissionMode, error) {
	switch m := PermissionMode(s); m {
	case ModeDefault, ModeAcceptEdits, ModePlan, ModeBypassPermissions, ModeAuto, ModeDontAsk:
		return m, nil
	}
	return "", fmt.Errorf("chatproto: unknown permission mode %q", s)
}

// Encode renders o as one JSON line with no trailing newline.
func Encode(o Outbound) ([]byte, error) {
	switch m := o.(type) {
	case UserInput:
		return encodeUserInput(m)
	case Initialize:
		req := map[string]any{"subtype": "initialize"}
		if len(m.SDKMCPServers) > 0 {
			req["sdkMcpServers"] = m.SDKMCPServers
		}
		return encodeControlRequest(m.RequestID, req)
	case Interrupt:
		return encodeControlRequest(m.RequestID, map[string]any{"subtype": "interrupt"})
	case SetPermissionMode:
		mode, err := ParsePermissionMode(string(m.Mode))
		if err != nil {
			return nil, err
		}
		return encodeControlRequest(m.RequestID, map[string]any{"subtype": "set_permission_mode", "mode": mode})
	case SetModel:
		if m.Model == "" {
			return nil, errors.New("chatproto: set_model needs a model")
		}
		return encodeControlRequest(m.RequestID, map[string]any{"subtype": "set_model", "model": m.Model})
	case PermissionAnswer:
		return encodePermissionAnswer(m)
	case MCPReply:
		return encodeMCPReply(m)
	}
	return nil, fmt.Errorf("chatproto: cannot encode %T", o)
}

func encodeUserInput(m UserInput) ([]byte, error) {
	if m.UUID == "" {
		return nil, errors.New("chatproto: user input needs a uuid")
	}
	if m.Text == "" && len(m.Images) == 0 {
		return nil, errors.New("chatproto: user input has no text and no images")
	}
	var content any = m.Text
	if len(m.Images) > 0 {
		blocks := make([]any, 0, len(m.Images)+1)
		if m.Text != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": m.Text})
		}
		for _, img := range m.Images {
			if img.MediaType == "" || img.Base64 == "" {
				return nil, errors.New("chatproto: image needs a media type and data")
			}
			blocks = append(blocks, map[string]any{
				"type":   "image",
				"source": map[string]any{"type": "base64", "media_type": img.MediaType, "data": img.Base64},
			})
		}
		content = blocks
	}
	return json.Marshal(map[string]any{
		"type":               "user",
		"uuid":               m.UUID,
		"message":            map[string]any{"role": "user", "content": content},
		"parent_tool_use_id": nil,
		"session_id":         "",
	})
}

func encodeControlRequest(requestID string, request map[string]any) ([]byte, error) {
	if requestID == "" {
		return nil, errors.New("chatproto: control request needs a request id")
	}
	return json.Marshal(map[string]any{
		"type":       "control_request",
		"request_id": requestID,
		"request":    request,
	})
}

func encodePermissionAnswer(m PermissionAnswer) ([]byte, error) {
	if m.RequestID == "" {
		return nil, errors.New("chatproto: permission answer needs a request id")
	}
	var body map[string]any
	switch d := m.Decision.(type) {
	case Allow:
		if len(d.UpdatedInput) == 0 {
			return nil, errors.New("chatproto: allow needs updated input")
		}
		body = map[string]any{"behavior": "allow", "updatedInput": d.UpdatedInput}
		if len(d.UpdatedPermissions) > 0 {
			body["updatedPermissions"] = d.UpdatedPermissions
		}
	case Deny:
		body = map[string]any{"behavior": "deny", "message": d.Message}
		if d.Interrupt {
			body["interrupt"] = true
		}
	default:
		return nil, fmt.Errorf("chatproto: cannot encode decision %T", m.Decision)
	}
	return json.Marshal(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "success",
			"request_id": m.RequestID,
			"response":   body,
		},
	})
}

func encodeMCPReply(m MCPReply) ([]byte, error) {
	if m.RequestID == "" {
		return nil, errors.New("chatproto: mcp reply needs a request id")
	}
	if !json.Valid(m.Message) {
		return nil, errors.New("chatproto: mcp reply message is not JSON")
	}
	return json.Marshal(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "success",
			"request_id": m.RequestID,
			"response":   map[string]any{"mcp_response": m.Message},
		},
	})
}

// WithAnswers returns an AskUserQuestion input with its answers filled in,
// keyed by question text, for use as Allow.UpdatedInput. Every other field of
// the input is kept as it was.
func WithAnswers(questionInput json.RawMessage, answers map[string]string) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(questionInput, &fields); err != nil || fields == nil {
		return nil, errors.New("chatproto: question input is not a JSON object")
	}
	if answers == nil {
		answers = map[string]string{}
	}
	raw, err := json.Marshal(answers)
	if err != nil {
		return nil, fmt.Errorf("chatproto: encode answers: %w", err)
	}
	fields["answers"] = raw
	out, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("chatproto: encode question input: %w", err)
	}
	return out, nil
}
