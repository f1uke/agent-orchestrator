package chatproto

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func foldEntries(t *testing.T, entries []Entry) Transcript {
	t.Helper()
	tr, err := Fold(entries)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func upTo(entries []Entry, seq uint64) []Entry {
	n := 0
	for n < len(entries) && entries[n].Seq <= seq {
		n++
	}
	return entries[:n]
}

func itemsOf[T Item](tr Transcript) []T {
	var out []T
	for _, it := range tr.Items {
		if v, ok := it.(T); ok {
			out = append(out, v)
		}
	}
	return out
}

func toolCalls(tr Transcript, name string) []ToolCall {
	return slices.DeleteFunc(itemsOf[ToolCall](tr), func(c ToolCall) bool { return c.Name != name })
}

func noticeKinds(tr Transcript) []NoticeKind {
	var kinds []NoticeKind
	for _, n := range itemsOf[Notice](tr) {
		kinds = append(kinds, n.Kind)
	}
	return kinds
}

func resultString(t *testing.T, r *ToolResult) string {
	t.Helper()
	if r == nil {
		t.Fatal("tool call has no result")
	}
	var s string
	if err := json.Unmarshal(r.Content, &s); err != nil {
		t.Fatalf("result content is not a string: %s", r.Content)
	}
	return s
}

func synth(seq uint64, dir Direction, payload string) Entry {
	return Entry{Seq: seq, Dir: dir, Payload: json.RawMessage(payload)}
}

func TestFoldFirstSession(t *testing.T) {
	tr := foldEntries(t, loadFixture(t, "first_session.jsonl"))

	if want := []string{"12e38fc9-a4a5-4819-b046-4107629b2c8b"}; !reflect.DeepEqual(tr.ClaudeSessionIDs, want) {
		t.Errorf("ClaudeSessionIDs = %v, want %v", tr.ClaudeSessionIDs, want)
	}

	users := itemsOf[UserItem](tr)
	wantUsers := []struct{ uuid, textPrefix string }{
		{"ce40626b-c042-46c2-8d92-2588f30c93ef", "Create a file named hello.txt"},
		{"80bc0dba-da1e-479f-9f4b-fa3e99a11697", "Use the AskUserQuestion tool"},
		{"31cfce2e-3e77-425d-b572-5aabe70b411e", "[from @billing-portal-12] "},
		{"ddecc06d-38b6-43e4-9756-8d15537e927e", "Count from 1 to 3"},
		{"6b6a4d66-83ae-48a8-be55-3de01739cff8", "Now say the word queued."},
	}
	if len(users) != len(wantUsers) {
		t.Fatalf("%d user items, want %d", len(users), len(wantUsers))
	}
	for i, w := range wantUsers {
		u := users[i]
		if u.UUID != w.uuid || !strings.HasPrefix(u.Text, w.textPrefix) || u.State != CommandCompleted {
			t.Errorf("user item %d = %+v, want uuid %s, text prefix %q, state completed", i, u, w.uuid, w.textPrefix)
		}
		wantOrigin := (*Origin)(nil)
		if i == 2 {
			wantOrigin = &Origin{Via: "ao_send", From: "billing-portal-12"}
		}
		if !reflect.DeepEqual(u.Origin, wantOrigin) {
			t.Errorf("user item %d origin = %+v, want %+v", i, u.Origin, wantOrigin)
		}
	}

	writes := toolCalls(tr, "Write")
	if len(writes) != 1 {
		t.Fatalf("%d Write calls, want 1", len(writes))
	}
	p := writes[0].Permission
	if p == nil || p.Status != PermissionAllowed || len(p.UpdatedPermissions) != 1 {
		t.Fatalf("Write permission = %+v, want allowed with one updated permission", p)
	}
	assertJSONEqual(t, p.UpdatedPermissions[0], []byte(`{"type":"setMode","mode":"acceptEdits","destination":"session"}`))
	if writes[0].Result == nil || writes[0].Result.IsError {
		t.Errorf("Write result = %+v, want a successful result", writes[0].Result)
	}

	asks := toolCalls(tr, "AskUserQuestion")
	if len(asks) != 1 || asks[0].Permission == nil || asks[0].Permission.Status != PermissionAllowed {
		t.Fatalf("AskUserQuestion calls = %+v, want one allowed", asks)
	}
	answer := resultString(t, asks[0].Result)
	for _, want := range []string{`"Red"`, `"Chips, Fruit"`} {
		if !strings.Contains(answer, want) {
			t.Errorf("AskUserQuestion result %q lacks %s", answer, want)
		}
	}

	if len(tr.Pending) != 0 || len(tr.Queued) != 0 || tr.Busy {
		t.Errorf("end state Pending=%v Queued=%v Busy=%v, want all empty", tr.Pending, tr.Queued, tr.Busy)
	}
	if tr.CostUSD != 0.04411749999999999 {
		t.Errorf("CostUSD = %v, want the last result's total_cost_usd", tr.CostUSD)
	}
	if tr.PermissionMode != "acceptEdits" {
		t.Errorf("PermissionMode = %q, want acceptEdits (set by the approved Write suggestion)", tr.PermissionMode)
	}
	if tr.LastSeq != 67 {
		t.Errorf("LastSeq = %d, want 67", tr.LastSeq)
	}
	if got, want := noticeKinds(tr), []NoticeKind{NoticeSpawn, NoticeExit}; !reflect.DeepEqual(got, want) {
		t.Errorf("notices = %v, want %v", got, want)
	}
	if ends := itemsOf[TurnEnd](tr); len(ends) != 5 || ends[0].IsError {
		t.Errorf("TurnEnds = %+v, want 5 successful ones", ends)
	}
}

func TestFoldFirstSessionPrefixes(t *testing.T) {
	entries := loadFixture(t, "first_session.jsonl")
	const (
		write  = "ce40626b-c042-46c2-8d92-2588f30c93ef"
		first  = "ddecc06d-38b6-43e4-9756-8d15537e927e"
		second = "6b6a4d66-83ae-48a8-be55-3de01739cff8"
	)
	tests := []struct {
		name        string
		seq         uint64
		wantQueued  []string
		wantBusy    bool
		wantPending []string
	}{
		{"input sent, not yet acknowledged", 4, []string{write}, false, nil},
		{"input started", 6, nil, true, nil},
		{"waiting on the Write permission", 12, nil, true, []string{"Write"}},
		{"permission answered", 13, nil, true, nil},
		{"waiting on the question", 28, nil, true, []string{"AskUserQuestion"}},
		{"burst sent, first still running the earlier message", 49, []string{first, second}, true, nil},
		{"first running, second queued", 53, []string{second}, true, nil},
		{"first done, second not started", 59, []string{second}, false, nil},
		{"second running", 60, nil, true, nil},
		{"all done", 66, nil, false, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := foldEntries(t, upTo(entries, tt.seq))
			if !reflect.DeepEqual(tr.Queued, tt.wantQueued) {
				t.Errorf("Queued = %v, want %v", tr.Queued, tt.wantQueued)
			}
			if tr.Busy != tt.wantBusy {
				t.Errorf("Busy = %v, want %v", tr.Busy, tt.wantBusy)
			}
			var pending []string
			for _, p := range tr.Pending {
				pending = append(pending, p.ToolName)
			}
			if !reflect.DeepEqual(pending, tt.wantPending) {
				t.Errorf("Pending tools = %v, want %v", pending, tt.wantPending)
			}
		})
	}

	t.Run("pending permission carries its request", func(t *testing.T) {
		tr := foldEntries(t, upTo(entries, 12))
		if len(tr.Pending) != 1 {
			t.Fatalf("Pending = %+v", tr.Pending)
		}
		p := tr.Pending[0]
		if p.Kind != PendingPermission || p.RequestID != "a14f5ad2-62e1-4695-88c8-112417c012ac" || p.ToolUseID != "toolu_01Rcbbfzp9t7Z51pRQtCMHpY" {
			t.Errorf("Pending = %+v", p)
		}
		if len(p.Suggestions) != 1 {
			t.Fatalf("Suggestions = %s", p.Suggestions)
		}
		assertJSONEqual(t, p.Suggestions[0], []byte(`{"type":"setMode","mode":"acceptEdits","destination":"session"}`))
		assertJSONEqual(t, p.Input, []byte(`{"file_path":"/work/project/hello.txt","content":"hi"}`))
		calls := toolCalls(tr, "Write")
		if len(calls) != 1 || calls[0].Permission == nil || calls[0].Permission.Status != PermissionPending || calls[0].Permission.RequestID != p.RequestID {
			t.Errorf("Write call = %+v, want its permission pending", calls)
		}
	})

	t.Run("question is a question", func(t *testing.T) {
		tr := foldEntries(t, upTo(entries, 28))
		if len(tr.Pending) != 1 || tr.Pending[0].Kind != PendingQuestion {
			t.Errorf("Pending = %+v, want one question", tr.Pending)
		}
	})
}

func TestFoldResumedSession(t *testing.T) {
	entries := loadFixture(t, "resumed_session.jsonl")
	tr := foldEntries(t, entries)

	if got, want := noticeKinds(tr), []NoticeKind{NoticeResumed, NoticeInterrupted, NoticeExit}; !reflect.DeepEqual(got, want) {
		t.Errorf("notices = %v, want %v", got, want)
	}
	if n := itemsOf[Notice](tr)[0]; n.Text != "12e38fc9-a4a5-4819-b046-4107629b2c8b" {
		t.Errorf("resumed notice = %+v, want the resumed session id", n)
	}

	plans := toolCalls(tr, "ExitPlanMode")
	if len(plans) != 2 {
		t.Fatalf("%d ExitPlanMode calls, want 2", len(plans))
	}
	first, second := plans[0].Permission, plans[1].Permission
	if first == nil || first.Status != PermissionDenied || first.Message != "Rejected: name the file goodbye.txt instead and present the revised plan." {
		t.Errorf("first plan permission = %+v, want denied with the reviewer's message", first)
	}
	if second == nil || second.Status != PermissionAllowed {
		t.Errorf("second plan permission = %+v, want allowed", second)
	}
	if plans[0].Result == nil || !plans[0].Result.IsError {
		t.Errorf("first plan result = %+v, want an error result", plans[0].Result)
	}

	bash := toolCalls(tr, "Bash")
	if len(bash) != 1 || bash[0].Permission == nil || bash[0].Permission.Status != PermissionDenied || bash[0].Permission.Message != "The user denied rm from the chat UI." {
		t.Errorf("Bash calls = %+v, want one denied with the chat UI message", bash)
	}

	interrupted := -1
	for i, it := range tr.Items {
		if n, ok := it.(Notice); ok && n.Kind == NoticeInterrupted {
			interrupted = i
		}
	}
	if interrupted < 0 || interrupted+1 >= len(tr.Items) {
		t.Fatalf("no interrupted notice followed by an item; items = %d", len(tr.Items))
	}
	if end, ok := tr.Items[interrupted+1].(TurnEnd); !ok || !end.IsError || end.Subtype != "error_during_execution" {
		t.Errorf("item after the interrupt = %+v, want an errored TurnEnd", tr.Items[interrupted+1])
	}

	users := itemsOf[UserItem](tr)
	if essay := users[len(users)-2]; !strings.HasPrefix(essay.Text, "Write a 600-word essay") || essay.State != CommandCancelled {
		t.Errorf("interrupted input = %+v, want state cancelled", essay)
	}

	if len(tr.ClaudeSessionIDs) != 1 {
		t.Errorf("ClaudeSessionIDs = %v, want the one resumed session", tr.ClaudeSessionIDs)
	}
	if len(tr.Pending) != 0 || len(tr.Queued) != 0 || tr.Busy {
		t.Errorf("end state Pending=%v Queued=%v Busy=%v, want all empty", tr.Pending, tr.Queued, tr.Busy)
	}
	if tr.CostUSD != 0.09444599999999999 {
		t.Errorf("CostUSD = %v, want the last result's total_cost_usd", tr.CostUSD)
	}

	for _, c := range []struct {
		seq  uint64
		want string
	}{{16, "default"}, {17, "plan"}, {20, "plan"}, {43, "plan"}, {44, "default"}, {54, "default"}, {59, "default"}, {91, "default"}} {
		if got := foldEntries(t, upTo(entries, c.seq)).PermissionMode; got != c.want {
			t.Errorf("PermissionMode after seq %d = %q, want %q", c.seq, got, c.want)
		}
	}
}

func TestReplayChangesNothing(t *testing.T) {
	for _, name := range []string{"first_session.jsonl", "resumed_session.jsonl"} {
		t.Run(name, func(t *testing.T) {
			entries := loadFixture(t, name)
			want := foldEntries(t, entries)

			f := NewFolder()
			for _, e := range entries {
				if err := f.Apply(e); err != nil {
					t.Fatal(err)
				}
			}
			for _, replay := range [][]Entry{entries, entries[:len(entries)/2], entries[:1], {entries[len(entries)-1]}} {
				for _, e := range replay {
					if err := f.Apply(e); err != nil {
						t.Fatal(err)
					}
				}
				if got := f.Transcript(); !reflect.DeepEqual(got, want) {
					t.Fatalf("transcript changed after replaying %d entries", len(replay))
				}
			}
		})
	}
}

func TestTranscriptIsASnapshot(t *testing.T) {
	entries := loadFixture(t, "first_session.jsonl")
	f := NewFolder()
	for _, e := range upTo(entries, 12) {
		if err := f.Apply(e); err != nil {
			t.Fatal(err)
		}
	}
	snap := f.Transcript()
	want := foldEntries(t, upTo(entries, 12))

	for _, e := range entries[12:] {
		if err := f.Apply(e); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(snap, want) {
		t.Error("an earlier Transcript changed when later entries were applied")
	}
	if got := f.Transcript(); reflect.DeepEqual(got, snap) {
		t.Error("later entries did not change the live transcript")
	}

	full := foldEntries(t, entries)
	mutated := f.Transcript()
	for _, it := range mutated.Items {
		switch it := it.(type) {
		case ToolCall:
			it.Permission.Status = "tampered"
			it.Result.IsError = !it.Result.IsError
		case UserItem:
			if it.Origin != nil {
				it.Origin.From = "tampered"
			}
		}
	}
	mutated.ClaudeSessionIDs[0] = "tampered"
	mutated.Items[0] = Notice{}
	if got := f.Transcript(); !reflect.DeepEqual(got, full) {
		t.Error("mutating a snapshot reached the Folder")
	}
}

func TestApplyRejectsBadEntriesWithoutChangingState(t *testing.T) {
	f := NewFolder()
	if err := f.Apply(synth(1, DirOut, `{"type":"system","subtype":"init","session_id":"s1","permissionMode":"plan"}`)); err != nil {
		t.Fatal(err)
	}
	before := f.Transcript()
	for name, e := range map[string]Entry{
		"not json":           synth(2, DirOut, `{`),
		"missing type":       synth(2, DirIn, `{"uuid":"u"}`),
		"host note not json": synth(2, DirHost, `[]`),
		"host note, no kind": synth(2, DirHost, `{}`),
		"unknown direction":  synth(2, "sideways", `{"type":"x"}`),
	} {
		if err := f.Apply(e); err == nil {
			t.Errorf("%s: Apply succeeded", name)
		}
	}
	if after := f.Transcript(); !reflect.DeepEqual(after, before) {
		t.Errorf("a failed Apply changed the transcript: %+v", after)
	}
	if err := f.Apply(synth(2, DirOut, `{"type":"hologram"}`)); err != nil {
		t.Errorf("unknown type after failures: %v", err)
	}
	if got := f.Transcript().LastSeq; got != 2 {
		t.Errorf("LastSeq = %d, want 2: a rejected entry must not consume its seq", got)
	}
}

const (
	toolUseLine = `{"type":"assistant","uuid":"a1","message":{"id":"m1","content":[{"type":"tool_use","id":"toolu_1","name":"%s","input":{"x":1}}]}}`
	askLine     = `{"type":"control_request","request_id":"req1","request":{"subtype":"can_use_tool","tool_name":"%s","tool_use_id":"toolu_1","input":{"x":1}}}`
	allowLine   = `{"type":"control_response","response":{"subtype":"success","request_id":"req1","response":{"behavior":"allow","updatedInput":{"x":1}}}}`
	denyLine    = `{"type":"control_response","response":{"subtype":"success","request_id":"req1","response":{"behavior":"deny","message":"no thanks"}}}`
)

func withTool(name string, rest ...Entry) []Entry {
	entries := []Entry{
		synth(1, DirOut, strings.Replace(toolUseLine, "%s", name, 1)),
		synth(2, DirOut, strings.Replace(askLine, "%s", name, 1)),
	}
	for i, e := range rest {
		e.Seq = uint64(3 + i)
		entries = append(entries, e)
	}
	return entries
}

func TestPermissionSettlement(t *testing.T) {
	tests := []struct {
		name        string
		rest        []Entry
		wantStatus  PermissionStatus
		wantMessage string
		wantPending int
	}{
		{"unanswered", nil, PermissionPending, "", 1},
		{"host allows", []Entry{synth(0, DirIn, allowLine)}, PermissionAllowed, "", 0},
		{"host denies", []Entry{synth(0, DirIn, denyLine)}, PermissionDenied, "no thanks", 0},
		{"echo after the host's answer changes nothing", []Entry{synth(0, DirIn, denyLine), synth(0, DirOut, denyLine)}, PermissionDenied, "no thanks", 0},
		{"first answer wins over a conflicting later one", []Entry{synth(0, DirIn, allowLine), synth(0, DirOut, denyLine)}, PermissionAllowed, "", 0},
		{"answer seen only on stdout", []Entry{synth(0, DirOut, denyLine)}, PermissionDenied, "no thanks", 0},
		{"cancelled by claude", []Entry{synth(0, DirOut, `{"type":"control_cancel_request","request_id":"req1"}`)}, PermissionCancelled, "", 0},
		{"cancel after an answer changes nothing", []Entry{synth(0, DirIn, allowLine), synth(0, DirOut, `{"type":"control_cancel_request","request_id":"req1"}`)}, PermissionAllowed, "", 0},
		{"answer after a cancel changes nothing", []Entry{synth(0, DirOut, `{"type":"control_cancel_request","request_id":"req1"}`), synth(0, DirIn, allowLine)}, PermissionCancelled, "", 0},
		{"error response counts as denied", []Entry{synth(0, DirIn, `{"type":"control_response","response":{"subtype":"error","request_id":"req1","error":"handler crashed"}}`)}, PermissionDenied, "handler crashed", 0},
		{"response to another request is ignored", []Entry{synth(0, DirOut, `{"type":"control_response","response":{"subtype":"success","request_id":"other","response":{"mode":"plan"}}}`)}, PermissionPending, "", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := foldEntries(t, withTool("Bash", tt.rest...))
			calls := toolCalls(tr, "Bash")
			if len(calls) != 1 || calls[0].Permission == nil {
				t.Fatalf("calls = %+v", calls)
			}
			p := calls[0].Permission
			if p.Status != tt.wantStatus || p.Message != tt.wantMessage || p.RequestID != "req1" {
				t.Errorf("permission = %+v, want status %s message %q", p, tt.wantStatus, tt.wantMessage)
			}
			if len(tr.Pending) != tt.wantPending {
				t.Errorf("Pending = %+v, want %d", tr.Pending, tt.wantPending)
			}
		})
	}
}

func TestPendingKindFollowsTheTool(t *testing.T) {
	for tool, want := range map[string]PendingKind{
		"Bash": PendingPermission, "Write": PendingPermission, "AskUserQuestion": PendingQuestion, "ExitPlanMode": PendingPlan,
	} {
		tr := foldEntries(t, withTool(tool))
		if len(tr.Pending) != 1 || tr.Pending[0].Kind != want || tr.Pending[0].ToolName != tool {
			t.Errorf("%s: Pending = %+v, want kind %s", tool, tr.Pending, want)
		}
	}
}

func TestPendingStaysOldestFirst(t *testing.T) {
	ask := func(seq uint64, id string) Entry {
		return synth(seq, DirOut, `{"type":"control_request","request_id":"`+id+`","request":{"subtype":"can_use_tool","tool_name":"Bash","tool_use_id":"t-`+id+`","input":{}}}`)
	}
	answer := func(seq uint64, id string) Entry {
		return synth(seq, DirIn, `{"type":"control_response","response":{"subtype":"success","request_id":"`+id+`","response":{"behavior":"deny","message":"x"}}}`)
	}
	tr := foldEntries(t, []Entry{ask(1, "a"), ask(2, "b"), ask(3, "c"), answer(4, "b")})
	var ids []string
	for _, p := range tr.Pending {
		ids = append(ids, p.RequestID)
	}
	if want := []string{"a", "c"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("Pending = %v, want %v", ids, want)
	}
}

func TestToolResultAttachesByID(t *testing.T) {
	tr := foldEntries(t, []Entry{
		synth(1, DirOut, `{"type":"assistant","message":{"id":"m","content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{}}]}}`),
		synth(2, DirOut, `{"type":"assistant","parent_tool_use_id":"toolu_1","message":{"id":"m2","content":[{"type":"tool_use","id":"toolu_2","name":"Grep","input":{}}]}}`),
		synth(3, DirOut, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_2","content":[{"type":"text","text":"found"}]},{"type":"tool_result","tool_use_id":"toolu_missing","content":"x"}]}}`),
		synth(4, DirOut, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","is_error":true,"content":"bad"}]}}`),
		synth(5, DirOut, `{"type":"assistant","message":{"id":"m","content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{}}]}}`),
	})
	calls := itemsOf[ToolCall](tr)
	if len(calls) != 2 {
		t.Fatalf("%d tool calls, want 2 (a repeated tool_use id is the same call)", len(calls))
	}
	if r := calls[0].Result; r == nil || !r.IsError || string(r.Content) != `"bad"` {
		t.Errorf("Read result = %+v", r)
	}
	if c := calls[1]; c.ParentToolUseID != "toolu_1" || c.Result == nil || c.Result.IsError || string(c.Result.Content) != `[{"type":"text","text":"found"}]` {
		t.Errorf("Grep call = %+v", c)
	}
}

func TestAssistantBlocksBecomeItems(t *testing.T) {
	tr := foldEntries(t, []Entry{
		synth(1, DirOut, `{"type":"assistant","message":{"id":"m1","content":[{"type":"thinking","thinking":"","signature":"s"}]}}`),
		synth(2, DirOut, `{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"hello"}]}}`),
		synth(3, DirOut, `{"type":"result","subtype":"success","num_turns":1,"total_cost_usd":0.25}`),
	})
	want := []Item{Thinking{}, AssistantText{MessageID: "m1", Text: "hello"}, TurnEnd{Subtype: "success", NumTurns: 1, CostUSD: 0.25}}
	if !reflect.DeepEqual(tr.Items, want) {
		t.Errorf("Items = %+v, want %+v", tr.Items, want)
	}
	if tr.CostUSD != 0.25 {
		t.Errorf("CostUSD = %v", tr.CostUSD)
	}
}

func TestInterruptMarkers(t *testing.T) {
	for text, want := range map[string]bool{
		"[Request interrupted by user]":              true,
		"[Request interrupted by user for tool use]": true,
		"please do not interrupt":                    false,
	} {
		payload, err := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}})
		if err != nil {
			t.Fatal(err)
		}
		tr := foldEntries(t, []Entry{synth(1, DirOut, string(payload))})
		if got := len(tr.Items) == 1 && reflect.DeepEqual(tr.Items[0], Notice{Kind: NoticeInterrupted}); got != want {
			t.Errorf("%q: interrupted notice = %v, want %v (items %+v)", text, got, want, tr.Items)
		}
	}
}

func TestUserOriginAttachesInEitherOrder(t *testing.T) {
	const input = `{"type":"user","uuid":"u1","message":{"role":"user","content":"[from @w-1] hi"},"parent_tool_use_id":null,"session_id":""}`
	const note = `{"kind":"user_origin","uuid":"u1","origin":{"via":"ao_send","from":"w-1"}}`
	want := &Origin{Via: "ao_send", From: "w-1"}
	for name, entries := range map[string][]Entry{
		"note first": {synth(1, DirHost, note), synth(2, DirIn, input)},
		"note after": {synth(1, DirIn, input), synth(2, DirHost, note)},
	} {
		tr := foldEntries(t, entries)
		users := itemsOf[UserItem](tr)
		if len(users) != 1 || !reflect.DeepEqual(users[0].Origin, want) {
			t.Errorf("%s: users = %+v, want origin %+v", name, users, want)
		}
	}
	tr := foldEntries(t, []Entry{synth(1, DirIn, input), synth(2, DirHost, `{"kind":"user_origin","uuid":"other","origin":{"via":"ao_send","from":"w-2"}}`)})
	if u := itemsOf[UserItem](tr)[0]; u.Origin != nil {
		t.Errorf("origin of another input attached: %+v", u.Origin)
	}
}

func TestUserInputDeduplicatesByUUID(t *testing.T) {
	const input = `{"type":"user","uuid":"u1","message":{"role":"user","content":[{"type":"text","text":"look"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA"}}]}}`
	tr := foldEntries(t, []Entry{
		synth(1, DirIn, input),
		synth(2, DirOut, `{"type":"user","uuid":"u1","isReplay":true,"message":{"content":"look"}}`),
		synth(3, DirIn, input),
	})
	want := []Item{UserItem{UUID: "u1", Text: "look", Images: 1, State: CommandSent}}
	if !reflect.DeepEqual(tr.Items, want) || !reflect.DeepEqual(tr.Queued, []string{"u1"}) {
		t.Errorf("Items = %+v, Queued = %v; want %+v queued once", tr.Items, tr.Queued, want)
	}
}

func TestLifecycleBeforeInputStillApplies(t *testing.T) {
	tr := foldEntries(t, []Entry{
		synth(1, DirOut, `{"type":"command_lifecycle","command_uuid":"u1","state":"started"}`),
		synth(2, DirIn, `{"type":"user","uuid":"u1","message":{"content":"hi"}}`),
	})
	if u := itemsOf[UserItem](tr)[0]; u.State != CommandStarted || !tr.Busy || len(tr.Queued) != 0 {
		t.Errorf("user = %+v Busy=%v Queued=%v, want started and busy", u, tr.Busy, tr.Queued)
	}
}

func TestExitSettlesWhatTheProcessLeftBehind(t *testing.T) {
	entries := loadFixture(t, "first_session.jsonl")
	exit := synth(1000, DirHost, `{"kind":"exit","code":null,"signal":"SIGKILL"}`)

	t.Run("while waiting on a permission", func(t *testing.T) {
		tr := foldEntries(t, append(slices.Clone(upTo(entries, 12)), exit))
		if tr.Busy || len(tr.Pending) != 0 || len(tr.Queued) != 0 {
			t.Errorf("Busy=%v Pending=%v Queued=%v, want all cleared", tr.Busy, tr.Pending, tr.Queued)
		}
		if p := toolCalls(tr, "Write")[0].Permission; p.Status != PermissionCancelled {
			t.Errorf("permission = %+v, want cancelled", p)
		}
		if u := itemsOf[UserItem](tr)[0]; u.State != CommandCancelled {
			t.Errorf("running input state = %q, want cancelled", u.State)
		}
		if n := itemsOf[Notice](tr); n[len(n)-1] != (Notice{Kind: NoticeExit, Text: "terminated by signal"}) {
			t.Errorf("last notice = %+v", n[len(n)-1])
		}
	})

	t.Run("with queued input", func(t *testing.T) {
		tr := foldEntries(t, append(slices.Clone(upTo(entries, 53)), exit))
		if tr.Busy || len(tr.Queued) != 0 {
			t.Errorf("Busy=%v Queued=%v, want cleared", tr.Busy, tr.Queued)
		}
		users := itemsOf[UserItem](tr)
		if got := users[len(users)-1]; got.State != CommandCancelled {
			t.Errorf("queued input state = %q, want cancelled", got.State)
		}
		if got := users[1]; got.State != CommandCompleted {
			t.Errorf("finished input state = %q, want it left completed", got.State)
		}
	})
}

func TestSpawnSettlesWhatACrashedHostLeftBehind(t *testing.T) {
	entries := loadFixture(t, "first_session.jsonl")
	spawn := synth(1000, DirHost, `{"kind":"spawn","argv":["claude"],"resume":"12e38fc9-a4a5-4819-b046-4107629b2c8b","pid":7}`)
	tr := foldEntries(t, append(slices.Clone(upTo(entries, 12)), spawn))
	if tr.Busy || len(tr.Pending) != 0 || len(tr.Queued) != 0 {
		t.Errorf("Busy=%v Pending=%v Queued=%v, want all cleared", tr.Busy, tr.Pending, tr.Queued)
	}
	if p := toolCalls(tr, "Write")[0].Permission; p.Status != PermissionCancelled {
		t.Errorf("permission = %+v, want cancelled", p)
	}
	if u := itemsOf[UserItem](tr)[0]; u.State != CommandCancelled {
		t.Errorf("running input state = %q, want cancelled", u.State)
	}
	if n := itemsOf[Notice](tr); n[len(n)-1] != (Notice{Kind: NoticeResumed, Text: "12e38fc9-a4a5-4819-b046-4107629b2c8b"}) {
		t.Errorf("last notice = %+v", n[len(n)-1])
	}
}

func TestCompactBoundaryIsANotice(t *testing.T) {
	tr := foldEntries(t, []Entry{
		synth(1, DirOut, `{"type":"system","subtype":"compact_boundary","session_id":"s","compact_metadata":{"trigger":"manual","pre_tokens":12000}}`),
		synth(2, DirOut, `{"type":"user","uuid":"c1","session_id":"s","message":{"role":"user","content":"This session is being continued from a previous conversation. Summary: ..."}}`),
	})
	if want := []Item{Notice{Kind: NoticeCompacted}}; !reflect.DeepEqual(tr.Items, want) {
		t.Errorf("Items = %+v, want %+v", tr.Items, want)
	}
}

func TestUnknownEventsChangeNothing(t *testing.T) {
	base := foldEntries(t, upTo(loadFixture(t, "first_session.jsonl"), 12))
	f := NewFolder()
	for _, e := range upTo(loadFixture(t, "first_session.jsonl"), 12) {
		if err := f.Apply(e); err != nil {
			t.Fatal(err)
		}
	}
	extra := uint64(100)
	for _, payload := range []string{
		`{"type":"hologram","x":1}`,
		`{"type":"system","subtype":"future_thing","session_id":"s"}`,
		`{"type":"system","subtype":"status","status":"compacting"}`,
		`{"type":"rate_limit_event"}`,
		`{"type":"stream_event","event":{"type":"message_start"}}`,
		`{"type":"control_request","request_id":"x","request":{"subtype":"mcp_status"}}`,
		`{"type":"user","uuid":"r","isReplay":true,"message":{"content":"replayed"}}`,
		`{"kind":"future_note"}`,
	} {
		dir := DirOut
		if strings.HasPrefix(payload, `{"kind"`) {
			dir = DirHost
		}
		extra++
		if err := f.Apply(synth(extra, dir, payload)); err != nil {
			t.Fatalf("%s: %v", payload, err)
		}
	}
	got := f.Transcript()
	got.LastSeq = base.LastSeq
	if !reflect.DeepEqual(got, base) {
		t.Errorf("unknown events changed the transcript:\n got %+v\nwant %+v", got, base)
	}
}

func TestFoldKeepsCommandsModelsAndTheModelInUse(t *testing.T) {
	builtin, skill := true, false
	wantCommands := []Command{
		{Name: "compact", Description: "Clear history but keep a summary in context", Builtin: &builtin},
		{Name: "review", Description: "Review a pull request", Builtin: &builtin},
		{Name: "tdd", Description: "Write the failing test first", Builtin: &skill},
	}
	wantOpus := Model{
		Value: "opus", ResolvedModel: "claude-opus-5-5", DisplayName: "Opus 5.5",
		Description: "For complex work and everyday tasks", SupportsEffort: true,
		SupportedEffortLevels: []string{"low", "medium", "high", "xhigh", "max"},
	}
	for _, name := range []string{"first_session.jsonl", "resumed_session.jsonl"} {
		t.Run(name, func(t *testing.T) {
			entries := loadFixture(t, name)
			if tr := foldEntries(t, upTo(entries, 2)); tr.Model != "sonnet" || tr.Commands != nil || tr.Models != nil {
				t.Errorf("before initialize answers: Model=%q Commands=%v Models=%v, want the spawn's --model and no lists", tr.Model, tr.Commands, tr.Models)
			}
			tr := foldEntries(t, entries)
			if !reflect.DeepEqual(tr.Commands, wantCommands) {
				t.Errorf("Commands = %+v, want %+v", tr.Commands, wantCommands)
			}
			if len(tr.Models) != 13 || !reflect.DeepEqual(tr.Models[1], wantOpus) {
				t.Errorf("Models = %d, [1] = %+v, want 13 with opus second", len(tr.Models), tr.Models)
			}
			if tr.Model != "claude-sonnet-5-5" {
				t.Errorf("Model = %q, want system/init's claude-sonnet-5-5", tr.Model)
			}
		})
	}
}

func TestFoldFollowsAnAcceptedSetModel(t *testing.T) {
	entries := loadFixture(t, "set_model.jsonl")
	if tr := foldEntries(t, upTo(entries, 2)); tr.Model != "claude-sonnet-5-5" {
		t.Errorf("after the accepted set_model Model = %q, want the resolved claude-sonnet-5-5", tr.Model)
	}
	if tr := foldEntries(t, entries); tr.Model != "claude-sonnet-5-5" {
		t.Errorf("after the refused set_model Model = %q, want it unchanged", tr.Model)
	}

	unasked := foldEntries(t, []Entry{
		synth(1, DirOut, `{"type":"control_response","response":{"subtype":"success","request_id":"x","response":{"model":"claude-haiku-5-5"}}}`),
	})
	if unasked.Model != "" {
		t.Errorf("a response to no host request set Model = %q", unasked.Model)
	}
}

func TestFoldKeepsTheLastAcceptedInitialize(t *testing.T) {
	initialize := func(seq uint64, id string) Entry {
		return synth(seq, DirIn, `{"type":"control_request","request_id":"`+id+`","request":{"subtype":"initialize"}}`)
	}
	tr := foldEntries(t, []Entry{
		initialize(1, "a"),
		synth(2, DirOut, `{"type":"control_response","response":{"subtype":"success","request_id":"a","response":{"commands":[{"name":"compact","description":"d","argumentHint":"<what>"}],"models":[{"value":"haiku","displayName":"Haiku"}],"account":{"email":"x@example.com"}}}}`),
		initialize(3, "b"),
		synth(4, DirOut, `{"type":"control_response","response":{"subtype":"error","request_id":"b","error":"boom"}}`),
	})
	want := []Command{{Name: "compact", Description: "d", ArgumentHint: "<what>"}}
	if !reflect.DeepEqual(tr.Commands, want) || len(tr.Models) != 1 || tr.Models[0].Value != "haiku" {
		t.Errorf("Commands=%+v Models=%+v, want the accepted initialize's lists kept over the failed one", tr.Commands, tr.Models)
	}
}

func TestSpawnTakesTheModelFromItsCommandLine(t *testing.T) {
	for _, tt := range []struct{ argv, want string }{
		{`["claude","--model","opus"]`, "opus"},
		{`["claude","--model=haiku"]`, "haiku"},
		{`["claude","--","--model","opus"]`, ""},
		{`["claude","--model"]`, ""},
	} {
		tr := foldEntries(t, []Entry{
			synth(1, DirOut, `{"type":"system","subtype":"init","session_id":"s","model":"claude-sonnet-5-5"}`),
			synth(2, DirHost, `{"kind":"spawn","resume":"","argv":`+tt.argv+`}`),
		})
		if tr.Model != tt.want {
			t.Errorf("spawn %s: Model = %q, want %q", tt.argv, tr.Model, tt.want)
		}
	}
}
