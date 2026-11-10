package chatproto

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Transcript is the chat state a log folds into.
type Transcript struct {
	ClaudeSessionIDs []string      // in first-seen order, from system/init
	Items            []Item        // in the order they happened
	Pending          []Pending     // open can_use_tool requests, oldest first
	Queued           []string      // user input uuids sent but not yet started
	Busy             bool          // some command started and has not finished
	PermissionMode   string        // latest system/init value
	CostUSD          float64       // latest result.total_cost_usd, cumulative for the claude process
	Commands         []Command     // from the latest initialize response
	Models           []Model       // from the latest initialize response
	Model            string        // in use: the spawn's --model, then system/init or an accepted set_model
	ChosenModel      string        // the latest accepted set_model, which a respawn passes as --model
	Effort           string        // in use: the spawn's --effort, then a get_settings answer or an accepted apply_flag_settings; empty when unknown
	ChosenEffort     string        // the latest accepted apply_flag_settings effortLevel, which a respawn passes as --effort
	RemoteControl    RemoteControl // the Remote Control bridge; off after a process ends
	Chrome           bool
	Bash             *BashItem // the "!" command run the session is busy with, until it settles; nil when none
	Suggestion       string    // claude's guess at the next user message; empty once input, a turn start, an interrupt or a process change follows it
	LastSeq          uint64
}

// Command is a slash command or skill claude offers.
type Command struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	ArgumentHint string `json:"argumentHint"`
	Builtin      *bool  `json:"builtin"` // nil when claude does not say
}

// Model is a model the session can switch to. Value is what set_model takes:
// an alias such as "sonnet", or a full model id.
type Model struct {
	Value                 string   `json:"value"`
	ResolvedModel         string   `json:"resolvedModel"`
	DisplayName           string   `json:"displayName"`
	Description           string   `json:"description"`
	SupportsEffort        bool     `json:"supportsEffort"`
	SupportedEffortLevels []string `json:"supportedEffortLevels"`
}

// Item is one row of the transcript. The concrete types are the structs below,
// held by value.
type Item interface{ isItem() }

// UserItem is a message the host sent. State is CommandSent until claude
// reports the message's lifecycle.
type UserItem struct {
	UUID   string
	Text   string
	Images int
	Origin *Origin
	State  CommandState
}

// AssistantText is one text block of an assistant message.
type AssistantText struct{ MessageID, Text string }

// Thinking is one thinking block. Text is empty when claude redacts it.
type Thinking struct{ Text string }

// ToolCall is a tool invocation with its result and permission outcome, when
// they have arrived. Input, result content and suggestions stay raw JSON.
type ToolCall struct {
	ID              string
	Name            string
	ParentToolUseID string
	Input           json.RawMessage
	Result          *ToolResult
	Permission      *PermissionOutcome
}

// ToolResult is what a tool returned.
type ToolResult struct {
	IsError bool
	Content json.RawMessage
}

// PermissionStatus is where a can_use_tool request stands.
type PermissionStatus string

// Permission statuses.
const (
	PermissionPending   PermissionStatus = "pending"
	PermissionAllowed   PermissionStatus = "allowed"
	PermissionDenied    PermissionStatus = "denied"
	PermissionCancelled PermissionStatus = "cancelled"
)

// PermissionOutcome is the settled or pending answer to a ToolCall's permission request.
type PermissionOutcome struct {
	RequestID          string
	Status             PermissionStatus
	Message            string // the deny message
	UpdatedPermissions []json.RawMessage
}

// TurnEnd marks the end of a turn.
type TurnEnd struct {
	Subtype  string
	IsError  bool
	NumTurns int
	CostUSD  float64
}

// NoticeKind classifies a Notice.
type NoticeKind string

// Notice kinds.
const (
	NoticeInterrupted NoticeKind = "interrupted"
	NoticeSpawn       NoticeKind = "spawn"
	NoticeResumed     NoticeKind = "resumed"
	NoticeExit        NoticeKind = "exit"
	NoticeCompacted   NoticeKind = "compacted"
)

// Notice is a marker in the transcript that is not a message: an interrupt, a
// compaction, or a process lifecycle event. For resumed, Text is the claude session id.
type Notice struct {
	Kind NoticeKind
	Text string
}

func (UserItem) isItem()      {}
func (AssistantText) isItem() {}
func (Thinking) isItem()      {}
func (ToolCall) isItem()      {}
func (TurnEnd) isItem()       {}
func (Notice) isItem()        {}

// PendingKind says how the UI should ask about a Pending request.
type PendingKind string

// Pending kinds.
const (
	PendingPermission PendingKind = "permission"
	PendingQuestion   PendingKind = "question"
	PendingPlan       PendingKind = "plan"
)

// Pending is an open can_use_tool request awaiting the host's answer.
type Pending struct {
	RequestID   string
	ToolName    string
	ToolUseID   string
	Kind        PendingKind
	Input       json.RawMessage
	Suggestions []json.RawMessage
}

// Fold folds a whole log into a transcript.
func Fold(entries []Entry) (Transcript, error) {
	f := NewFolder()
	for _, e := range entries {
		if err := f.Apply(e); err != nil {
			return Transcript{}, err
		}
	}
	return f.Transcript(), nil
}

type permission struct {
	toolUseID string
	out       PermissionOutcome
}

// Folder is a reducer from log entries to a Transcript. It is not safe for
// concurrent use.
type Folder struct {
	last     uint64
	sessions []string
	items    []Item
	tools    map[string]*ToolCall
	users    map[string]*UserItem
	origins  map[string]Origin
	commands map[string]CommandState
	perms    map[string]*permission
	pending  []Pending
	mode     string
	cost     float64
	// controls maps the id of a host's initialize, set_model, get_settings,
	// apply_flag_settings or remote_control request to what it asked, because
	// claude's response does not repeat it.
	controls     map[string]control
	remote       RemoteControl
	slash        []Command
	models       []Model
	model        string
	chosen       string
	effort       string
	chosenEffort string
	chrome       bool
	suggest      string
	bashes       map[string]*BashItem // every "!" command run, by id
	bash         *BashItem            // the latest run
}

type control struct {
	subtype string
	enabled bool // remote_control only
	level   string
}

// NewFolder returns an empty Folder.
func NewFolder() *Folder {
	return &Folder{
		tools:    map[string]*ToolCall{},
		users:    map[string]*UserItem{},
		origins:  map[string]Origin{},
		commands: map[string]CommandState{},
		perms:    map[string]*permission{},
		controls: map[string]control{},
		bashes:   map[string]*BashItem{},
		remote:   RemoteControl{State: RemoteOff},
	}
}

// Apply folds one entry in. An entry whose Seq is not above the last applied
// one is ignored, so replaying a prefix of the log changes nothing. An entry
// that fails to parse is an error and leaves the Folder untouched.
func (f *Folder) Apply(e Entry) error {
	if e.Seq <= f.last {
		return nil
	}
	switch e.Dir {
	case DirOut, DirIn:
		ev, err := ParseLine(e.Payload)
		if err != nil {
			return fmt.Errorf("chatproto: entry %d: %w", e.Seq, err)
		}
		if e.Dir == DirOut {
			f.applyOut(ev)
		} else {
			f.applyIn(ev)
		}
	case DirHost:
		note, err := ParseHostNote(e.Payload)
		if err != nil {
			return fmt.Errorf("chatproto: entry %d: %w", e.Seq, err)
		}
		f.applyHost(note)
	default:
		return fmt.Errorf("chatproto: entry %d: unknown dir %q", e.Seq, e.Dir)
	}
	f.last = e.Seq
	return nil
}

func (f *Folder) applyOut(ev Event) {
	switch ev := ev.(type) {
	case SystemInit:
		if !slices.Contains(f.sessions, ev.SessionID) {
			f.sessions = append(f.sessions, ev.SessionID)
		}
		if ev.PermissionMode != "" {
			f.mode = ev.PermissionMode
		}
		if ev.Model != "" {
			f.model = ev.Model
		}
	case System:
		if ev.PermissionMode != "" {
			f.mode = ev.PermissionMode
		}
		if ev.Subtype == "compact_boundary" {
			f.items = append(f.items, Notice{Kind: NoticeCompacted})
		}
	case Assistant:
		f.applyAssistant(ev)
	case PromptSuggestion:
		f.suggest = ev.Text
	case BridgeState:
		if state, ok := bridgeState(ev.State); ok {
			f.remote.State, f.remote.Detail = state, ev.Detail
		}
	case User:
		if ev.IsReplay {
			f.addRemoteInput(ev)
		} else {
			f.applyToolResults(ev)
		}
	case Result:
		if f.settleBash() {
			return
		}
		f.cost = ev.TotalCostUSD
		f.items = append(f.items, TurnEnd{Subtype: ev.Subtype, IsError: ev.IsError, NumTurns: ev.NumTurns, CostUSD: ev.TotalCostUSD})
	case ControlRequest:
		if c, ok := ev.Body.(CanUseTool); ok {
			f.openPermission(ev.RequestID, c)
		}
	case ControlResponse:
		f.answerPermission(ev)
		f.answerControl(ev)
	case ControlCancel:
		f.settle(ev.RequestID, PermissionCancelled, "", nil)
	case CommandLifecycle:
		if f.bashes[ev.CommandUUID] == nil {
			f.setCommand(ev.CommandUUID, ev.State)
		}
	}
}

func (f *Folder) applyIn(ev Event) {
	switch ev := ev.(type) {
	case User:
		if !f.isBashResult(ev) {
			f.addUserInput(ev, nil)
		}
	case ControlRequest:
		c, ok := ev.Body.(OtherControl)
		if !ok {
			return
		}
		switch c.Subtype {
		case "initialize", "set_model", "get_settings":
			f.controls[ev.RequestID] = control{subtype: c.Subtype}
		case "apply_flag_settings":
			if level := effortLevel(c.Raw); level != "" {
				f.controls[ev.RequestID] = control{subtype: c.Subtype, level: level}
			}
		case "remote_control":
			enabled := remoteEnabled(c.Raw)
			f.controls[ev.RequestID] = control{subtype: c.Subtype, enabled: enabled}
			if enabled {
				f.remote = RemoteControl{State: RemoteConnecting}
			}
		}
	case ControlResponse:
		f.answerPermission(ev)
	}
}

func (f *Folder) answerControl(r ControlResponse) {
	c, ok := f.controls[r.RequestID]
	if !ok {
		return
	}
	delete(f.controls, r.RequestID)
	if c.subtype == "remote_control" {
		f.answerRemote(r, c.enabled)
		return
	}
	if !r.OK {
		return
	}
	switch c.subtype {
	case "initialize":
		var body struct {
			Commands []Command `json:"commands"`
			Models   []Model   `json:"models"`
		}
		if json.Unmarshal(r.Response, &body) == nil {
			f.slash, f.models = body.Commands, body.Models
		}
	case "set_model":
		var body struct {
			Model string `json:"model"`
		}
		if json.Unmarshal(r.Response, &body) == nil && body.Model != "" {
			f.model, f.chosen = body.Model, body.Model
		}
	case "apply_flag_settings":
		f.effort, f.chosenEffort = c.level, c.level
	case "get_settings":
		var body struct {
			Applied struct {
				Effort string `json:"effort"`
			} `json:"applied"`
		}
		if json.Unmarshal(r.Response, &body) == nil && body.Applied.Effort != "" {
			f.effort = body.Applied.Effort
		}
	}
}

func (f *Folder) applyAssistant(a Assistant) {
	for _, b := range a.Blocks {
		switch b := b.(type) {
		case TextBlock:
			f.items = append(f.items, AssistantText{MessageID: a.MessageID, Text: b.Text})
		case ThinkingBlock:
			f.items = append(f.items, Thinking(b))
		case ToolUseBlock:
			if _, seen := f.tools[b.ID]; seen {
				continue
			}
			tc := &ToolCall{ID: b.ID, Name: b.Name, ParentToolUseID: a.ParentToolUseID, Input: b.Input}
			f.tools[b.ID] = tc
			f.items = append(f.items, tc)
		}
	}
}

// interruptMarker prefixes the user message claude emits after an interrupt
// ("[Request interrupted by user]", or "... for tool use]").
const interruptMarker = "[Request interrupted by user"

func (f *Folder) applyToolResults(u User) {
	for _, b := range u.Blocks {
		switch b := b.(type) {
		case ToolResultBlock:
			if tc := f.tools[b.ToolUseID]; tc != nil {
				tc.Result = &ToolResult{IsError: b.IsError, Content: b.Content}
			}
		case TextBlock:
			if strings.HasPrefix(b.Text, interruptMarker) {
				f.suggest = ""
				f.items = append(f.items, Notice{Kind: NoticeInterrupted})
			}
		}
	}
}

// originHuman is the origin.kind claude gives a message a person typed.
const originHuman = "human"

// addRemoteInput adds the row for a prompt typed on the Remote Control side.
// The host logs every input it sends before claude can replay it, so a replay
// whose uuid has no row is a human's message that did not come from AO.
func (f *Folder) addRemoteInput(u User) {
	if u.UUID == "" || u.ParentToolUseID != "" || u.OriginKind != originHuman || f.bashes[u.UUID] != nil {
		return
	}
	typed := slices.ContainsFunc(u.Blocks, func(b Block) bool {
		switch b.(type) {
		case TextBlock, ImageBlock:
			return true
		}
		return false
	})
	if typed {
		f.addUserInput(u, &Origin{Via: OriginRemoteControl})
	}
}

func (f *Folder) addUserInput(u User, origin *Origin) {
	if u.UUID != "" && f.users[u.UUID] != nil {
		return
	}
	f.suggest = ""
	var text strings.Builder
	images := 0
	for _, b := range u.Blocks {
		switch b := b.(type) {
		case TextBlock:
			text.WriteString(b.Text)
		case ImageBlock:
			images++
		}
	}
	state, known := f.commands[u.UUID]
	if !known {
		state = CommandSent
	}
	item := &UserItem{UUID: u.UUID, Text: text.String(), Images: images, State: state}
	if o, ok := f.origins[u.UUID]; ok {
		item.Origin = &o
	} else {
		item.Origin = origin
	}
	if u.UUID != "" {
		f.users[u.UUID] = item
	}
	f.items = append(f.items, item)
}

func (f *Folder) setCommand(uuid string, state CommandState) {
	if state == CommandStarted {
		f.suggest = ""
	}
	f.commands[uuid] = state
	if item := f.users[uuid]; item != nil {
		item.State = state
	}
}

func (f *Folder) openPermission(requestID string, c CanUseTool) {
	if _, seen := f.perms[requestID]; seen {
		return
	}
	p := &permission{toolUseID: c.ToolUseID, out: PermissionOutcome{RequestID: requestID, Status: PermissionPending}}
	f.perms[requestID] = p
	f.pending = append(f.pending, Pending{
		RequestID: requestID, ToolName: c.ToolName, ToolUseID: c.ToolUseID,
		Kind: pendingKind(c.ToolName), Input: c.Input, Suggestions: c.Suggestions,
	})
	if tc := f.tools[c.ToolUseID]; tc != nil {
		tc.Permission = &p.out
	}
}

func pendingKind(tool string) PendingKind {
	switch tool {
	case "AskUserQuestion":
		return PendingQuestion
	case "ExitPlanMode":
		return PendingPlan
	}
	return PendingPermission
}

func (f *Folder) answerPermission(r ControlResponse) {
	if !r.OK {
		f.settle(r.RequestID, PermissionDenied, r.Error, nil)
		return
	}
	var body struct {
		Behavior           string            `json:"behavior"`
		Message            string            `json:"message"`
		UpdatedPermissions []json.RawMessage `json:"updatedPermissions"`
	}
	if json.Unmarshal(r.Response, &body) != nil {
		return
	}
	switch body.Behavior {
	case "allow":
		f.settle(r.RequestID, PermissionAllowed, "", body.UpdatedPermissions)
	case "deny":
		f.settle(r.RequestID, PermissionDenied, body.Message, nil)
	}
}

func (f *Folder) settle(requestID string, status PermissionStatus, message string, updated []json.RawMessage) {
	p := f.perms[requestID]
	if p == nil || p.out.Status != PermissionPending {
		return
	}
	p.out.Status, p.out.Message, p.out.UpdatedPermissions = status, message, updated
	f.pending = slices.DeleteFunc(f.pending, func(x Pending) bool { return x.RequestID == requestID })
}

func (f *Folder) applyHost(n HostNote) {
	switch n.Kind {
	case NoteSpawn:
		f.endProcess()
		f.model = flagValue(n.Argv, "--model")
		f.effort = flagValue(n.Argv, "--effort")
		f.chrome = slices.Contains(flagsOf(n.Argv), ChromeFlag)
		if n.Resume != "" {
			f.items = append(f.items, Notice{Kind: NoticeResumed, Text: n.Resume})
		} else {
			f.items = append(f.items, Notice{Kind: NoticeSpawn})
		}
	case NoteBashStart, NoteBashOutput, NoteBashEnd:
		f.applyBash(n)
	case NoteExit:
		f.endProcess()
		f.items = append(f.items, Notice{Kind: NoticeExit, Text: exitText(n.Code)})
	case NoteUserOrigin:
		if n.Origin == nil || n.UUID == "" {
			return
		}
		f.origins[n.UUID] = *n.Origin
		if item := f.users[n.UUID]; item != nil {
			o := *n.Origin
			item.Origin = &o
		}
	}
}

// ChromeFlag is the claude flag that turns on Claude in Chrome. Print mode
// ignores the setting that enables it by default, so a host must pass it.
const ChromeFlag = "--chrome"

func flagsOf(argv []string) []string {
	if i := slices.Index(argv, "--"); i >= 0 {
		return argv[:i]
	}
	return argv
}

func flagValue(argv []string, name string) string {
	argv = flagsOf(argv)
	for i, a := range argv {
		if v, ok := strings.CutPrefix(a, name+"="); ok {
			return v
		}
		if a == name && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}

// effortLevel reads settings.effortLevel from an apply_flag_settings request.
// It is empty when the request sets no effort, or clears it with null.
func effortLevel(request json.RawMessage) string {
	var w struct {
		Settings struct {
			EffortLevel string `json:"effortLevel"`
		} `json:"settings"`
	}
	if json.Unmarshal(request, &w) != nil {
		return ""
	}
	return w.Settings.EffortLevel
}

func exitText(code *int) string {
	if code == nil {
		return "terminated by signal"
	}
	return fmt.Sprintf("exit code %d", *code)
}

func (f *Folder) endProcess() {
	f.endBash()
	f.suggest = ""
	f.remote = RemoteControl{State: RemoteOff}
	for id := range f.perms {
		f.settle(id, PermissionCancelled, "", nil)
	}
	for uuid, state := range f.commands {
		if live(state) {
			f.setCommand(uuid, CommandCancelled)
		}
	}
	for uuid, item := range f.users {
		if live(item.State) {
			f.setCommand(uuid, CommandCancelled)
		}
	}
}

func live(s CommandState) bool {
	return s == CommandSent || s == CommandQueued || s == CommandStarted
}

// Transcript returns a snapshot. Later Apply calls do not change it. Raw JSON
// payloads are shared with the Folder and must be treated as read-only.
func (f *Folder) Transcript() Transcript {
	t := Transcript{
		ClaudeSessionIDs: slices.Clone(f.sessions),
		Items:            make([]Item, 0, len(f.items)),
		Pending:          make([]Pending, 0, len(f.pending)),
		PermissionMode:   f.mode,
		CostUSD:          f.cost,
		Commands:         slices.Clone(f.slash),
		Models:           slices.Clone(f.models),
		Model:            f.model,
		ChosenModel:      f.chosen,
		Effort:           f.effort,
		ChosenEffort:     f.chosenEffort,
		RemoteControl:    f.remote,
		Chrome:           f.chrome,
		Bash:             f.activeBash(),
		Suggestion:       f.suggest,
		LastSeq:          f.last,
	}
	for _, it := range f.items {
		switch it := it.(type) {
		case *UserItem:
			c := *it
			if it.Origin != nil {
				o := *it.Origin
				c.Origin = &o
			}
			t.Items = append(t.Items, c)
			if c.State == CommandSent || c.State == CommandQueued {
				t.Queued = append(t.Queued, c.UUID)
			}
		case *ToolCall:
			t.Items = append(t.Items, cloneToolCall(it))
		case *BashItem:
			t.Items = append(t.Items, cloneBash(it))
		default:
			t.Items = append(t.Items, it)
		}
	}
	for _, p := range f.pending {
		p.Suggestions = slices.Clone(p.Suggestions)
		t.Pending = append(t.Pending, p)
	}
	for _, s := range f.commands {
		if s == CommandStarted {
			t.Busy = true
			break
		}
	}
	return t
}

func cloneToolCall(tc *ToolCall) ToolCall {
	c := *tc
	if tc.Result != nil {
		r := *tc.Result
		c.Result = &r
	}
	if tc.Permission != nil {
		p := *tc.Permission
		p.UpdatedPermissions = slices.Clone(p.UpdatedPermissions)
		c.Permission = &p
	}
	return c
}
