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
		{"disable sent, claude not answered", 11, RemoteControl{State: RemoteConnected, URL: exampleSessionURL}},
		{"disable accepted", 12, RemoteControl{State: RemoteOff}},
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
