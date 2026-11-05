package chatproto

import (
	"reflect"
	"testing"
)

const exampleSessionURL = "https://claude.ai/code/session_example"

func TestFoldRemoteControlFollowsTheBridgeFromEnableToDisable(t *testing.T) {
	entries := loadFixture(t, "remote_control.jsonl")
	steps := []struct {
		name string
		seq  uint64
		want RemoteControl
	}{
		{"nothing asked yet", 0, RemoteControl{State: RemoteOff}},
		{"enable sent", 1, RemoteControl{State: RemoteConnecting}},
		{"claude says ready", 2, RemoteControl{State: RemoteConnecting}},
		{"claude accepts and gives the url", 3, RemoteControl{State: RemoteConnecting, URL: exampleSessionURL}},
		{"bridge connected", 4, RemoteControl{State: RemoteConnected, URL: exampleSessionURL}},
		{"bridge reconnecting", 5, RemoteControl{State: RemoteReconnecting, URL: exampleSessionURL}},
		{"bridge connected again", 6, RemoteControl{State: RemoteConnected, URL: exampleSessionURL}},
		{"a message typed on the remote side runs", 10, RemoteControl{State: RemoteConnected, URL: exampleSessionURL}},
		{"a local prompt and a second remote prompt ran", 16, RemoteControl{State: RemoteConnected, URL: exampleSessionURL}},
		{"disable sent, claude not answered", 17, RemoteControl{State: RemoteConnected, URL: exampleSessionURL}},
		{"disable accepted", 18, RemoteControl{State: RemoteOff}},
	}
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			if got := foldEntries(t, upTo(entries, s.seq)).RemoteControl; !reflect.DeepEqual(got, s.want) {
				t.Errorf("RemoteControl after seq %d = %+v, want %+v", s.seq, got, s.want)
			}
		})
	}
}

func TestFoldRemoteControlKeepsTheFailureReason(t *testing.T) {
	entries := loadFixture(t, "remote_control_failed.jsonl")
	const reason = "Remote Control is not available for this account."
	for _, seq := range []uint64{2, 3} {
		want := RemoteControl{State: RemoteFailed, Detail: reason}
		if got := foldEntries(t, upTo(entries, seq)).RemoteControl; !reflect.DeepEqual(got, want) {
			t.Errorf("RemoteControl after seq %d = %+v, want %+v", seq, got, want)
		}
	}
}

func TestFoldRemoteControlTakesTheReasonFromTheErrorWhenNoBridgeStateCameFirst(t *testing.T) {
	tr := foldEntries(t, []Entry{
		synth(1, DirIn, `{"type":"control_request","request_id":"rc","request":{"subtype":"remote_control","enabled":true}}`),
		synth(2, DirOut, `{"type":"control_response","response":{"subtype":"error","request_id":"rc","error":"cannot ask the user to confirm"}}`),
	})
	if want := (RemoteControl{State: RemoteFailed, Detail: "cannot ask the user to confirm"}); tr.RemoteControl != want {
		t.Errorf("RemoteControl = %+v, want %+v", tr.RemoteControl, want)
	}
}

func TestFoldRemoteControlStaysOnWhenADisableIsRefused(t *testing.T) {
	entries := append(upTo(loadFixture(t, "remote_control.jsonl"), 10),
		synth(11, DirIn, `{"type":"control_request","request_id":"rc-2","request":{"subtype":"remote_control","enabled":false}}`),
		synth(12, DirOut, `{"type":"control_response","response":{"subtype":"error","request_id":"rc-2","error":"boom"}}`),
	)
	if got := foldEntries(t, entries).RemoteControl.State; got != RemoteConnected {
		t.Errorf("State = %q after a refused disable, want connected", got)
	}
}

func TestFoldRemoteControlIgnoresAnAnswerToNoRequestOfTheHost(t *testing.T) {
	tr := foldEntries(t, []Entry{
		synth(1, DirOut, `{"type":"control_response","response":{"subtype":"success","request_id":"x","response":{"session_url":"`+exampleSessionURL+`"}}}`),
	})
	if want := (RemoteControl{State: RemoteOff}); tr.RemoteControl != want {
		t.Errorf("RemoteControl = %+v, want %+v", tr.RemoteControl, want)
	}
}

func TestFoldRemoteControlIsOffAfterTheProcessEnds(t *testing.T) {
	connected := upTo(loadFixture(t, "remote_control.jsonl"), 6)
	for name, note := range map[string]string{
		"exit":  `{"kind":"exit","code":0}`,
		"spawn": `{"kind":"spawn","argv":["claude"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			tr := foldEntries(t, append(connected, synth(7, DirHost, note)))
			if want := (RemoteControl{State: RemoteOff}); tr.RemoteControl != want {
				t.Errorf("RemoteControl = %+v, want off with no url", tr.RemoteControl)
			}
		})
	}
}

func TestFoldRemoteControlSurvivesTheInitThatOpensEveryTurn(t *testing.T) {
	entries := append(upTo(loadFixture(t, "remote_control.jsonl"), 6),
		synth(7, DirOut, `{"type":"system","subtype":"init","session_id":"s-1","model":"m","permissionMode":"default"}`))
	if got := foldEntries(t, entries).RemoteControl.State; got != RemoteConnected {
		t.Errorf("State = %q after a later turn's init, want connected", got)
	}
}

func TestFoldRunsARemoteTypedMessageThroughItsLifecycle(t *testing.T) {
	entries := loadFixture(t, "remote_control.jsonl")
	if busy := foldEntries(t, upTo(entries, 9)).Busy; !busy {
		t.Error("Busy = false while the remote-typed command runs, want true")
	}
	if busy := foldEntries(t, upTo(entries, 10)).Busy; busy {
		t.Error("Busy = true after the remote-typed command completed, want false")
	}
}

func TestParseLineReadsABridgeState(t *testing.T) {
	ev, err := ParseLine([]byte(`{"type":"system","subtype":"bridge_state","state":"failed","detail":"why","failure_kind":"terminal"}`))
	if err != nil {
		t.Fatal(err)
	}
	if want := (BridgeState{State: "failed", Detail: "why", FailureKind: "terminal"}); ev != want {
		t.Errorf("ParseLine = %#v, want %#v", ev, want)
	}
}

func TestFoldRemotePromptsGetOneUserRowEachInOrder(t *testing.T) {
	entries := loadFixture(t, "remote_control.jsonl")
	remote := &Origin{Via: OriginRemoteControl}
	want := []UserItem{
		{UUID: "remote-1", Text: "placeholder remote prompt", Origin: remote, State: CommandCompleted},
		{UUID: "local-1", Text: "placeholder local prompt", State: CommandSent},
		{UUID: "remote-2", Text: "second placeholder remote prompt", Origin: remote, State: CommandCompleted},
	}
	for seq, rows := range map[uint64]int{8: 0, 9: 1, 10: 1, 11: 2, 12: 2, 15: 3, 18: 3} {
		if got := itemsOf[UserItem](foldEntries(t, upTo(entries, seq))); len(got) != rows {
			t.Errorf("after seq %d: %d user rows, want %d: %+v", seq, len(got), rows, got)
		}
	}
	if got := itemsOf[UserItem](foldEntries(t, entries)); !reflect.DeepEqual(got, want) {
		t.Errorf("user rows = %+v, want %+v", got, want)
	}
}

func TestFoldReplayAddsNoRowUnlessAHumanTypedItElsewhere(t *testing.T) {
	const origin = `"origin":{"kind":"human"}`
	tests := []struct {
		name    string
		payload string
		rows    int
	}{
		{"a human message", `{"type":"user","uuid":"r1",` + origin + `,"isReplay":true,"message":{"role":"user","content":"hi"}}`, 1},
		{"a human message with an image", `{"type":"user","uuid":"r1",` + origin + `,"isReplay":true,"message":{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA"}}]}}`, 1},
		{"no origin", `{"type":"user","uuid":"r1","isReplay":true,"message":{"role":"user","content":"hi"}}`, 0},
		{"a task notification", `{"type":"user","uuid":"r1","origin":{"kind":"task-notification"},"isReplay":true,"message":{"role":"user","content":"hi"}}`, 0},
		{"no uuid", `{"type":"user",` + origin + `,"isReplay":true,"message":{"role":"user","content":"hi"}}`, 0},
		{"inside a subagent", `{"type":"user","uuid":"r1",` + origin + `,"parent_tool_use_id":"toolu_1","isReplay":true,"message":{"role":"user","content":"hi"}}`, 0},
		{"tool results only", `{"type":"user","uuid":"r1",` + origin + `,"isReplay":true,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"}]}}`, 0},
		{"not a replay", `{"type":"user","uuid":"r1",` + origin + `,"message":{"role":"user","content":"hi"}}`, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := foldEntries(t, []Entry{synth(1, DirOut, tt.payload)})
			if got := len(itemsOf[UserItem](tr)); got != tt.rows {
				t.Errorf("user rows = %d, want %d", got, tt.rows)
			}
		})
	}
}

func TestFoldReplayOfAHostInputKeepsItsOneRowAndItsOrigin(t *testing.T) {
	const replay = `{"type":"user","uuid":"u1","origin":{"kind":"human"},"isReplay":true,"message":{"role":"user","content":"hi"}}`
	tr := foldEntries(t, []Entry{
		synth(1, DirHost, `{"kind":"user_origin","uuid":"u1","origin":{"via":"ao_send","from":"w-2"}}`),
		synth(2, DirIn, `{"type":"user","uuid":"u1","message":{"role":"user","content":"hi"}}`),
		synth(3, DirOut, replay),
		synth(4, DirOut, replay),
	})
	want := []UserItem{{UUID: "u1", Text: "hi", Origin: &Origin{Via: "ao_send", From: "w-2"}, State: CommandSent}}
	if got := itemsOf[UserItem](tr); !reflect.DeepEqual(got, want) {
		t.Errorf("user rows = %+v, want %+v", got, want)
	}
}

func TestFoldRepeatedReplayOfARemotePromptKeepsOneRow(t *testing.T) {
	const replay = `{"type":"user","uuid":"r1","origin":{"kind":"human"},"isReplay":true,"message":{"role":"user","content":"hi"}}`
	tr := foldEntries(t, []Entry{synth(1, DirOut, replay), synth(2, DirOut, replay)})
	if got := len(itemsOf[UserItem](tr)); got != 1 {
		t.Errorf("user rows = %d, want 1", got)
	}
}
