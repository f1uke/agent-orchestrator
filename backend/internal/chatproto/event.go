// Package chatproto speaks Claude Code's stream-json protocol
// (`claude -p --input-format stream-json --output-format stream-json`): it
// parses what claude writes to stdout, encodes what a host writes to stdin,
// decodes an append-only log of both, and folds that log into a chat
// transcript. It is a leaf package with no I/O of its own.
package chatproto

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// Event is one parsed line of claude's stdout. The concrete types are the
// exported structs below; ParseLine never returns anything else.
type Event interface{ isEvent() }

// SystemInit is the system/init event that opens every turn.
type SystemInit struct {
	SessionID      string
	Model          string
	PermissionMode string
	CWD            string
	Tools          []string
	SlashCommands  []string
}

// System is every system event other than init (status, thinking_tokens, ...).
type System struct {
	Subtype        string
	SessionID      string
	PermissionMode string // set on a status event after the mode changes
	Raw            json.RawMessage
}

// Assistant is one assistant message event. With partial messages on, claude
// emits one event per content block, all sharing MessageID.
type Assistant struct {
	UUID            string
	SessionID       string
	ParentToolUseID string
	MessageID       string
	Blocks          []Block
}

// User is a user message event: a replay of an input, or tool results.
// String content becomes a single TextBlock.
type User struct {
	UUID            string
	SessionID       string
	ParentToolUseID string
	IsReplay        bool
	Blocks          []Block
}

// Result ends a turn.
type Result struct {
	Subtype      string
	SessionID    string
	IsError      bool
	NumTurns     int
	TotalCostUSD float64
	DurationMS   int64
}

// StreamEvent is a partial-message stream event. Delta.Kind is DeltaOther for
// stream events that carry no delta (message_start, content_block_stop, ...).
type StreamEvent struct {
	SessionID       string
	ParentToolUseID string
	Delta           Delta
}

// ControlRequest is a request claude sends to the host, or a host request
// echoed back.
type ControlRequest struct {
	RequestID string
	Body      ControlBody
}

// ControlResponse answers a ControlRequest. Response is the inner payload,
// whose shape depends on the request (a permission decision, a mode, ...).
type ControlResponse struct {
	RequestID string
	OK        bool
	Error     string
	Response  json.RawMessage
}

// ControlCancel withdraws an earlier ControlRequest.
type ControlCancel struct{ RequestID string }

// CommandLifecycle reports the queue state of one user message.
type CommandLifecycle struct {
	CommandUUID string
	State       CommandState
}

// Unknown is any event type this package does not model, or a modelled type
// whose body did not have the expected shape. Raw is the whole line.
type Unknown struct {
	Type    string
	Subtype string
	Raw     json.RawMessage
}

func (SystemInit) isEvent()       {}
func (System) isEvent()           {}
func (Assistant) isEvent()        {}
func (User) isEvent()             {}
func (Result) isEvent()           {}
func (StreamEvent) isEvent()      {}
func (ControlRequest) isEvent()   {}
func (ControlResponse) isEvent()  {}
func (ControlCancel) isEvent()    {}
func (CommandLifecycle) isEvent() {}
func (Unknown) isEvent()          {}

// CommandState is the lifecycle state of a user message. Values beyond the
// constants are kept as claude sent them.
type CommandState string

// CommandSent is the state of an input the host wrote before claude reports
// any lifecycle for it; the others are states seen from claude.
const (
	CommandSent      CommandState = "sent"
	CommandQueued    CommandState = "queued"
	CommandStarted   CommandState = "started"
	CommandCompleted CommandState = "completed"
	CommandCancelled CommandState = "cancelled"
)

// ControlBody is the request inside a ControlRequest.
type ControlBody interface{ isControlBody() }

// CanUseTool asks the host whether a tool call may run. AskUserQuestion and
// ExitPlanMode arrive this way too.
type CanUseTool struct {
	ToolName       string
	ToolUseID      string
	Description    string
	DecisionReason string
	BlockedPath    string
	Input          json.RawMessage
	Suggestions    []json.RawMessage
}

// OtherControl is any control request other than can_use_tool (initialize,
// interrupt, set_permission_mode, ...). Raw is the request object.
type OtherControl struct {
	Subtype string
	Raw     json.RawMessage
}

func (CanUseTool) isControlBody()   {}
func (OtherControl) isControlBody() {}

// Block is one content block of a message.
type Block interface{ isBlock() }

// TextBlock is plain text.
type TextBlock struct{ Text string }

// ThinkingBlock is model reasoning. Claude often sends it redacted, with empty Text.
type ThinkingBlock struct{ Text string }

// ToolUseBlock is a tool call; Input stays raw because its shape is tool-specific.
type ToolUseBlock struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// ToolResultBlock is a tool's output. Content is a JSON string or an array of blocks.
type ToolResultBlock struct {
	ToolUseID string
	IsError   bool
	Content   json.RawMessage
}

// ImageBlock is an attached image; the data itself is not kept.
type ImageBlock struct{ MediaType string }

// OtherBlock is a block type this package does not model.
type OtherBlock struct {
	Type string
	Raw  json.RawMessage
}

func (TextBlock) isBlock()       {}
func (ThinkingBlock) isBlock()   {}
func (ToolUseBlock) isBlock()    {}
func (ToolResultBlock) isBlock() {}
func (ImageBlock) isBlock()      {}
func (OtherBlock) isBlock()      {}

// DeltaKind says which stream the text of a Delta belongs to.
type DeltaKind uint8

// Delta kinds. The zero value is DeltaOther.
const (
	DeltaOther DeltaKind = iota
	DeltaText
	DeltaThinking
	DeltaInputJSON
)

// Delta is the increment carried by a stream event, for content block Index.
type Delta struct {
	Kind  DeltaKind
	Index int
	Text  string
}

// ParseLine parses one line of claude's stdout. It fails only on invalid JSON
// or a missing "type". An unmodelled type, or a modelled type whose body does
// not decode, yields Unknown so a new claude release cannot break a reader.
func ParseLine(line []byte) (Event, error) {
	var head struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
	}
	if err := json.Unmarshal(line, &head); err != nil {
		return nil, fmt.Errorf("chatproto: parse line: %w", err)
	}
	if head.Type == "" {
		return nil, errors.New("chatproto: parse line: missing type")
	}
	ev, ok := decodeKnown(head.Type, head.Subtype, line)
	if !ok {
		return Unknown{Type: head.Type, Subtype: head.Subtype, Raw: bytes.Clone(line)}, nil
	}
	return ev, nil
}

func decodeKnown(typ, subtype string, line []byte) (Event, bool) {
	switch typ {
	case "system":
		return decodeSystem(subtype, line)
	case "assistant":
		return decodeAssistant(line)
	case "user":
		return decodeUser(line)
	case "result":
		return decodeResult(line)
	case "stream_event":
		return decodeStreamEvent(line)
	case "control_request":
		return decodeControlRequest(line)
	case "control_response":
		return decodeControlResponse(line)
	case "control_cancel_request":
		var w struct {
			RequestID string `json:"request_id"`
		}
		if json.Unmarshal(line, &w) != nil {
			return nil, false
		}
		return ControlCancel{RequestID: w.RequestID}, true
	case "command_lifecycle":
		var w struct {
			CommandUUID string       `json:"command_uuid"`
			State       CommandState `json:"state"`
		}
		if json.Unmarshal(line, &w) != nil {
			return nil, false
		}
		return CommandLifecycle{CommandUUID: w.CommandUUID, State: w.State}, true
	}
	return nil, false
}

func decodeSystem(subtype string, line []byte) (Event, bool) {
	var w struct {
		SessionID      string   `json:"session_id"`
		Model          string   `json:"model"`
		PermissionMode string   `json:"permissionMode"`
		CWD            string   `json:"cwd"`
		Tools          []string `json:"tools"`
		SlashCommands  []string `json:"slash_commands"`
	}
	if json.Unmarshal(line, &w) != nil {
		return nil, false
	}
	if subtype == "init" {
		return SystemInit{
			SessionID: w.SessionID, Model: w.Model, PermissionMode: w.PermissionMode,
			CWD: w.CWD, Tools: w.Tools, SlashCommands: w.SlashCommands,
		}, true
	}
	return System{Subtype: subtype, SessionID: w.SessionID, PermissionMode: w.PermissionMode, Raw: bytes.Clone(line)}, true
}

func decodeAssistant(line []byte) (Event, bool) {
	var w struct {
		UUID            string `json:"uuid"`
		SessionID       string `json:"session_id"`
		ParentToolUseID string `json:"parent_tool_use_id"`
		Message         struct {
			ID      string          `json:"id"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &w) != nil {
		return nil, false
	}
	blocks, err := decodeBlocks(w.Message.Content)
	if err != nil {
		return nil, false
	}
	return Assistant{
		UUID: w.UUID, SessionID: w.SessionID, ParentToolUseID: w.ParentToolUseID,
		MessageID: w.Message.ID, Blocks: blocks,
	}, true
}

func decodeUser(line []byte) (Event, bool) {
	var w struct {
		UUID            string `json:"uuid"`
		SessionID       string `json:"session_id"`
		ParentToolUseID string `json:"parent_tool_use_id"`
		IsReplay        bool   `json:"isReplay"`
		Message         struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &w) != nil {
		return nil, false
	}
	blocks, err := decodeBlocks(w.Message.Content)
	if err != nil {
		return nil, false
	}
	return User{
		UUID: w.UUID, SessionID: w.SessionID, ParentToolUseID: w.ParentToolUseID,
		IsReplay: w.IsReplay, Blocks: blocks,
	}, true
}

func decodeBlocks(content json.RawMessage) ([]Block, error) {
	content = bytes.TrimSpace(content)
	if len(content) == 0 || string(content) == "null" {
		return nil, nil
	}
	if content[0] == '"' {
		var s string
		if err := json.Unmarshal(content, &s); err != nil {
			return nil, err
		}
		return []Block{TextBlock{Text: s}}, nil
	}
	var raws []json.RawMessage
	if err := json.Unmarshal(content, &raws); err != nil {
		return nil, err
	}
	blocks := make([]Block, 0, len(raws))
	for _, raw := range raws {
		b, err := decodeBlock(raw)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, b)
	}
	return blocks, nil
}

func decodeBlock(raw json.RawMessage) (Block, error) {
	var w struct {
		Type      string          `json:"type"`
		Text      string          `json:"text"`
		Thinking  string          `json:"thinking"`
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Input     json.RawMessage `json:"input"`
		ToolUseID string          `json:"tool_use_id"`
		IsError   bool            `json:"is_error"`
		Content   json.RawMessage `json:"content"`
		Source    struct {
			MediaType string `json:"media_type"`
		} `json:"source"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, err
	}
	switch w.Type {
	case "text":
		return TextBlock{Text: w.Text}, nil
	case "thinking":
		return ThinkingBlock{Text: w.Thinking}, nil
	case "tool_use":
		return ToolUseBlock{ID: w.ID, Name: w.Name, Input: w.Input}, nil
	case "tool_result":
		return ToolResultBlock{ToolUseID: w.ToolUseID, IsError: w.IsError, Content: w.Content}, nil
	case "image":
		return ImageBlock{MediaType: w.Source.MediaType}, nil
	}
	return OtherBlock{Type: w.Type, Raw: bytes.Clone(raw)}, nil
}

func decodeResult(line []byte) (Event, bool) {
	var w struct {
		Subtype      string  `json:"subtype"`
		SessionID    string  `json:"session_id"`
		IsError      bool    `json:"is_error"`
		NumTurns     int     `json:"num_turns"`
		TotalCostUSD float64 `json:"total_cost_usd"`
		DurationMS   int64   `json:"duration_ms"`
	}
	if json.Unmarshal(line, &w) != nil {
		return nil, false
	}
	return Result(w), true
}

func decodeStreamEvent(line []byte) (Event, bool) {
	var w struct {
		SessionID       string `json:"session_id"`
		ParentToolUseID string `json:"parent_tool_use_id"`
		Event           struct {
			Type  string `json:"type"`
			Index int    `json:"index"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		} `json:"event"`
	}
	if json.Unmarshal(line, &w) != nil {
		return nil, false
	}
	d := Delta{Kind: DeltaOther, Index: w.Event.Index}
	if w.Event.Type == "content_block_delta" {
		switch w.Event.Delta.Type {
		case "text_delta":
			d.Kind, d.Text = DeltaText, w.Event.Delta.Text
		case "thinking_delta":
			d.Kind, d.Text = DeltaThinking, w.Event.Delta.Thinking
		case "input_json_delta":
			d.Kind, d.Text = DeltaInputJSON, w.Event.Delta.PartialJSON
		}
	}
	return StreamEvent{SessionID: w.SessionID, ParentToolUseID: w.ParentToolUseID, Delta: d}, true
}

type lenientString string

func (s *lenientString) UnmarshalJSON(b []byte) error {
	var v string
	if json.Unmarshal(b, &v) == nil {
		*s = lenientString(v)
		return nil
	}
	if string(b) != "null" {
		*s = lenientString(b)
	}
	return nil
}

func decodeControlRequest(line []byte) (Event, bool) {
	var w struct {
		RequestID string          `json:"request_id"`
		Request   json.RawMessage `json:"request"`
	}
	if json.Unmarshal(line, &w) != nil {
		return nil, false
	}
	var head struct {
		Subtype string `json:"subtype"`
	}
	if json.Unmarshal(w.Request, &head) != nil {
		return nil, false
	}
	if head.Subtype != "can_use_tool" {
		return ControlRequest{RequestID: w.RequestID, Body: OtherControl{Subtype: head.Subtype, Raw: w.Request}}, true
	}
	var c struct {
		ToolName       string            `json:"tool_name"`
		ToolUseID      string            `json:"tool_use_id"`
		Description    lenientString     `json:"description"`
		DecisionReason lenientString     `json:"decision_reason"`
		BlockedPath    lenientString     `json:"blocked_path"`
		Input          json.RawMessage   `json:"input"`
		Suggestions    []json.RawMessage `json:"permission_suggestions"`
	}
	if json.Unmarshal(w.Request, &c) != nil {
		return nil, false
	}
	return ControlRequest{RequestID: w.RequestID, Body: CanUseTool{
		ToolName: c.ToolName, ToolUseID: c.ToolUseID,
		Description: string(c.Description), DecisionReason: string(c.DecisionReason), BlockedPath: string(c.BlockedPath),
		Input: c.Input, Suggestions: c.Suggestions,
	}}, true
}

func decodeControlResponse(line []byte) (Event, bool) {
	var w struct {
		Response struct {
			Subtype   string          `json:"subtype"`
			RequestID string          `json:"request_id"`
			Error     lenientString   `json:"error"`
			Response  json.RawMessage `json:"response"`
		} `json:"response"`
	}
	if json.Unmarshal(line, &w) != nil {
		return nil, false
	}
	r := w.Response
	return ControlResponse{RequestID: r.RequestID, OK: r.Subtype == "success", Error: string(r.Error), Response: r.Response}, true
}
