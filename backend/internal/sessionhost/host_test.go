package sessionhost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
)

var wantProtocolFlags = []string{
	"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
	"--replay-user-messages", "--include-partial-messages", "--permission-prompt-tool=stdio",
}

const firstSessionID = "12e38fc9-a4a5-4819-b046-4107629b2c8b"

func replayInputs(t *testing.T, sock string, from uint64, fixture []chatproto.Entry, skipUsers int) {
	t.Helper()
	outs := outCounter(t, subscribeTo(t, sock, from))
	need := int64(0)
	var origin *chatproto.Origin
	for _, e := range fixture {
		switch e.Dir {
		case chatproto.DirOut:
			need++
		case chatproto.DirHost:
			if note, err := chatproto.ParseHostNote(e.Payload); err == nil && note.Kind == chatproto.NoteUserOrigin {
				origin = note.Origin
			}
		case chatproto.DirIn:
			eventually(t, fmt.Sprintf("%d out entries before seq %d", need, e.Seq), func() bool { return outs.Load() >= need })
			switch kind := messageKind(e.Payload); kind {
			case "user/":
				if skipUsers > 0 {
					skipUsers--
					continue
				}
				req := map[string]any{"op": "send", "text": userText(t, e.Payload)}
				if origin != nil {
					req["origin"] = origin
					origin = nil
				}
				callOK(t, sock, req)
			case "control_request/initialize":
			case "control_request/set_permission_mode":
				var w struct {
					Request struct{ Mode string } `json:"request"`
				}
				_ = json.Unmarshal(e.Payload, &w)
				callOK(t, sock, map[string]any{"op": "set_mode", "mode": w.Request.Mode})
			case "control_request/interrupt":
				callOK(t, sock, map[string]any{"op": "interrupt"})
			case "control_response/success":
				answerLike(t, sock, e.Payload)
			default:
				t.Fatalf("seq %d: no socket op for %s", e.Seq, kind)
			}
		}
	}
}

func answerLike(t *testing.T, sock string, payload json.RawMessage) {
	t.Helper()
	var w struct {
		Response struct {
			Response struct {
				Behavior           string            `json:"behavior"`
				Message            string            `json:"message"`
				Interrupt          bool              `json:"interrupt"`
				UpdatedInput       json.RawMessage   `json:"updatedInput"`
				UpdatedPermissions []json.RawMessage `json:"updatedPermissions"`
			} `json:"response"`
		} `json:"response"`
	}
	if err := json.Unmarshal(payload, &w); err != nil {
		t.Fatal(err)
	}
	fx := w.Response.Response
	st := state(t, sock)
	if len(st.Pending) != 1 {
		t.Fatalf("pending = %+v, want one request to answer", st.Pending)
	}
	p := st.Pending[0]
	req := map[string]any{"op": "answer", "request_id": p.RequestID, "behavior": fx.Behavior}
	switch fx.Behavior {
	case "deny":
		req["message"], req["interrupt"] = fx.Message, fx.Interrupt
	case "allow":
		if len(fx.UpdatedPermissions) > 0 {
			req["updated_permissions"] = p.Suggestions
		}
		if !jsonEqual(t, fx.UpdatedInput, p.Input) {
			req["updated_input"] = fx.UpdatedInput
		}
	}
	callOK(t, sock, req)
}

func jsonEqual(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

func userText(t *testing.T, payload json.RawMessage) string {
	t.Helper()
	var w struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(payload, &w); err != nil {
		t.Fatalf("user input %s: %v", payload, err)
	}
	return w.Message.Content
}

func shape(t *testing.T, entries []chatproto.Entry) []string {
	t.Helper()
	tr, err := chatproto.Fold(entries)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, it := range tr.Items {
		switch it := it.(type) {
		case chatproto.UserItem:
			origin := "-"
			if it.Origin != nil {
				origin = it.Origin.Via + ":" + it.Origin.From
			}
			out = append(out, fmt.Sprintf("user %q images=%d origin=%s state=%s", it.Text, it.Images, origin, it.State))
		case chatproto.AssistantText:
			out = append(out, fmt.Sprintf("text %q", it.Text))
		case chatproto.Thinking:
			out = append(out, "thinking")
		case chatproto.ToolCall:
			perm, result := "-", "-"
			if p := it.Permission; p != nil {
				perm = fmt.Sprintf("%s %q perms=%d", p.Status, p.Message, len(p.UpdatedPermissions))
			}
			if it.Result != nil {
				result = "ok"
				if it.Result.IsError {
					result = "error"
				}
			}
			out = append(out, fmt.Sprintf("tool %s perm=%s result=%s", it.Name, perm, result))
		case chatproto.TurnEnd:
			out = append(out, fmt.Sprintf("turn %s cost=%v", it.Subtype, it.CostUSD))
		case chatproto.Notice:
			out = append(out, fmt.Sprintf("notice %s %s", it.Kind, it.Text))
		}
	}
	return append(out, fmt.Sprintf("sessions=%v busy=%v queued=%d pending=%d mode=%s cost=%v",
		tr.ClaudeSessionIDs, tr.Busy, len(tr.Queued), len(tr.Pending), tr.PermissionMode, tr.CostUSD))
}

func assertSameShape(t *testing.T, got, want []chatproto.Entry) {
	t.Helper()
	g, w := shape(t, got), shape(t, want)
	if !slices.Equal(g, w) {
		t.Errorf("folded log differs from the fixture\n got:\n  %s\nwant:\n  %s", strings.Join(g, "\n  "), strings.Join(w, "\n  "))
	}
}

func assertConsecutive(t *testing.T, entries []chatproto.Entry) {
	t.Helper()
	for i, e := range entries {
		if e.Seq != uint64(i+1) {
			t.Fatalf("entry %d has seq %d, want %d", i, e.Seq, i+1)
		}
	}
}

func writePrompt(t *testing.T, dir string, fixture []chatproto.Entry) string {
	t.Helper()
	for _, e := range fixture {
		if e.Dir == chatproto.DirIn && messageKind(e.Payload) == "user/" {
			p := filepath.Join(dir, "prompt.txt")
			if err := os.WriteFile(p, []byte(userText(t, e.Payload)), 0o600); err != nil {
				t.Fatal(err)
			}
			return p
		}
	}
	t.Fatal("fixture has no user input")
	return ""
}

func runFirstSession(t *testing.T, dir string) (Config, []chatproto.Entry, string) {
	t.Helper()
	fixture := loadEntries(t, fixturePath(t, "first_session.jsonl"))
	cfg, argvFile := fakeConfig(t, dir, fixturePath(t, "first_session.jsonl"))
	cfg.PromptFile = writePrompt(t, dir, fixture)
	r := startHost(t, cfg)
	replayInputs(t, cfg.SocketPath, 0, fixture, 1)
	if res := r.wait(t); res.err != nil || res.code != 0 {
		t.Fatalf("Run = %d, %v; want 0\npane:\n%s", res.code, res.err, r.pane)
	}
	socketGone(t, cfg.SocketPath)
	return cfg, fixture, argvFile
}

func TestFreshSessionEndToEnd(t *testing.T) {
	cfg, fixture, argvFile := runFirstSession(t, shortDir(t))

	if got, want := readArgv(t, argvFile), append([]string{"--model", "sonnet"}, wantProtocolFlags...); !slices.Equal(got, want) {
		t.Errorf("argv = %v, want %v", got, want)
	}
	got := loadEntries(t, cfg.LogPath)
	assertConsecutive(t, got)
	assertSameShape(t, got, fixture)

	initAnswered := slices.IndexFunc(got, func(e chatproto.Entry) bool {
		return e.Dir == chatproto.DirOut && messageKind(e.Payload) == "control_response/success"
	})
	firstInput := slices.IndexFunc(got, func(e chatproto.Entry) bool {
		return e.Dir == chatproto.DirIn && messageKind(e.Payload) == "user/"
	})
	if initAnswered < 0 || firstInput < initAnswered {
		t.Errorf("prompt sent at entry %d, before initialize was answered at %d", firstInput, initAnswered)
	}
}

func TestRestartResumes(t *testing.T) {
	dir := shortDir(t)
	cfg, first, _ := runFirstSession(t, dir)
	before := loadEntries(t, cfg.LogPath)
	last := before[len(before)-1].Seq

	resumed := loadEntries(t, fixturePath(t, "resumed_session.jsonl"))
	cfg2, argvFile := fakeConfig(t, dir, fixturePath(t, "resumed_session.jsonl"))
	cfg2.PromptFile = cfg.PromptFile
	r := startHost(t, cfg2)
	replayInputs(t, cfg2.SocketPath, last, resumed, 0)
	if res := r.wait(t); res.err != nil || res.code != 0 {
		t.Fatalf("Run = %d, %v; want 0\npane:\n%s", res.code, res.err, r.pane)
	}

	argv := readArgv(t, argvFile)
	if n := len(argv); n < 2 || argv[n-2] != "--resume" || argv[n-1] != firstSessionID {
		t.Errorf("argv = %v, want it to end with --resume %s", argv, firstSessionID)
	}
	got := loadEntries(t, cfg.LogPath)
	assertConsecutive(t, got)
	spawn, err := chatproto.ParseHostNote(got[last].Payload)
	if err != nil || spawn.Kind != chatproto.NoteSpawn || spawn.Resume != firstSessionID {
		t.Errorf("entry %d = %s, want the spawn note resuming %s", last+1, got[last].Payload, firstSessionID)
	}
	want := slices.Clone(first)
	for i, e := range resumed {
		e.Seq = last + uint64(i) + 1
		want = append(want, e)
	}
	assertSameShape(t, got, want)
}

func waitingFixture(t *testing.T, dir string, extra ...fixtureLine) string {
	t.Helper()
	first := loadEntries(t, fixturePath(t, "first_session.jsonl"))
	lines := []fixtureLine{
		{chatproto.DirIn, string(first[1].Payload)},
		{chatproto.DirOut, string(first[2].Payload)},
	}
	lines = append(lines, extra...)
	lines = append(lines, fixtureLine{chatproto.DirIn, `{"type":"user","uuid":"never-sent","message":{"role":"user","content":"x"}}`})
	p := filepath.Join(dir, "waiting.jsonl")
	writeFixture(t, p, lines)
	return p
}

func TestSubscribe(t *testing.T) {
	dir := shortDir(t)
	first := loadEntries(t, fixturePath(t, "first_session.jsonl"))
	deltas, err := os.ReadFile(fixturePath(t, "stream_events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	turn := []fixtureLine{{chatproto.DirIn, string(first[3].Payload)}}
	nDeltas := 0
	for _, l := range bytes.Split(bytes.TrimSpace(deltas), []byte("\n")) {
		turn = append(turn, fixtureLine{chatproto.DirOut, string(l)})
		nDeltas++
	}
	turn = append(turn,
		fixtureLine{chatproto.DirOut, string(first[17].Payload)},
		fixtureLine{chatproto.DirOut, string(first[18].Payload)},
		fixtureLine{chatproto.DirIn, string(first[19].Payload)},
		fixtureLine{chatproto.DirOut, string(first[33].Payload)},
	)
	cfg, _ := fakeConfig(t, dir, waitingFixture(t, dir, turn...))
	r := startHost(t, cfg)
	eventually(t, "initialize answered", func() bool { return state(t, cfg.SocketPath).LastSeq == 3 })

	a := subscribeTo(t, cfg.SocketPath, 0)
	callOK(t, cfg.SocketPath, map[string]any{"op": "send", "text": "first"})
	var seen []chatproto.Entry
	gotDeltas := 0
	for len(seen) == 0 || messageKind(seen[len(seen)-1].Payload) != "result/" {
		f, ok := a.next(t)
		if !ok {
			t.Fatalf("stream closed after %d entries", len(seen))
		}
		switch {
		case f.Entry != nil:
			seen = append(seen, *f.Entry)
		case messageKind(f.Delta) == "stream_event/":
			gotDeltas++
		default:
			t.Fatalf("frame is neither an entry nor a stream event: %+v", f)
		}
	}
	assertConsecutive(t, seen)
	if gotDeltas != nDeltas {
		t.Errorf("got %d delta frames, want %d", gotDeltas, nDeltas)
	}
	for _, e := range loadEntries(t, cfg.LogPath) {
		if messageKind(e.Payload) == "stream_event/" {
			t.Errorf("stream event logged at seq %d", e.Seq)
		}
	}

	from := seen[len(seen)-2].Seq
	b := subscribeTo(t, cfg.SocketPath, from)
	callOK(t, cfg.SocketPath, map[string]any{"op": "send", "text": "second"})
	var bSeqs []uint64
	for len(bSeqs) < 3 {
		f, ok := b.next(t)
		if !ok {
			t.Fatalf("second stream closed after seqs %v", bSeqs)
		}
		if f.Entry != nil {
			bSeqs = append(bSeqs, f.Entry.Seq)
		}
	}
	if want := []uint64{from + 1, from + 2, from + 3}; !slices.Equal(bSeqs, want) {
		t.Errorf("second subscriber got seqs %v, want %v", bSeqs, want)
	}

	r.cancel()
	r.wait(t)
	for {
		f, ok := a.next(t)
		if !ok {
			break
		}
		if f.Entry != nil {
			seen = append(seen, *f.Entry)
		}
	}
	logged := loadEntries(t, cfg.LogPath)
	if len(seen) != len(logged) {
		t.Fatalf("first subscriber saw %d entries, log has %d", len(seen), len(logged))
	}
	for i := range logged {
		if seen[i].Seq != logged[i].Seq || !bytes.Equal(seen[i].Payload, logged[i].Payload) {
			t.Errorf("entry %d: streamed %d %s, logged %d %s", i, seen[i].Seq, seen[i].Payload, logged[i].Seq, logged[i].Payload)
		}
	}
}

func TestAnswer(t *testing.T) {
	dir := shortDir(t)
	fixture := loadEntries(t, fixturePath(t, "first_session.jsonl"))
	cfg, _ := fakeConfig(t, dir, fixturePath(t, "first_session.jsonl"))
	cfg.PromptFile = writePrompt(t, dir, fixture)
	startHost(t, cfg)
	var pending pendingJSON
	eventually(t, "the Write permission request", func() bool {
		st := state(t, cfg.SocketPath)
		if len(st.Pending) == 1 {
			pending = st.Pending[0]
		}
		return len(st.Pending) == 1
	})
	allow := map[string]any{"op": "answer", "request_id": pending.RequestID, "behavior": "allow"}

	if msg := callErr(t, cfg.SocketPath, map[string]any{"op": "answer", "request_id": "nope", "behavior": "deny"}); msg != "request nope is not pending" {
		t.Errorf("unknown request: error %q", msg)
	}
	callOK(t, cfg.SocketPath, allow)
	if msg := callErr(t, cfg.SocketPath, allow); msg != "request "+pending.RequestID+" is not pending" {
		t.Errorf("second answer: error %q", msg)
	}

	var answers []json.RawMessage
	for _, e := range loadEntries(t, cfg.LogPath) {
		if e.Dir == chatproto.DirIn && messageKind(e.Payload) == "control_response/success" {
			answers = append(answers, e.Payload)
		}
	}
	if len(answers) != 1 {
		t.Fatalf("logged %d answers, want 1", len(answers))
	}
	var sent struct {
		Response struct {
			Response struct {
				UpdatedInput json.RawMessage `json:"updatedInput"`
			} `json:"response"`
		} `json:"response"`
	}
	if err := json.Unmarshal(answers[0], &sent); err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(t, sent.Response.Response.UpdatedInput, pending.Input) {
		t.Errorf("allow sent updatedInput %s, want the request's own input %s", sent.Response.Response.UpdatedInput, pending.Input)
	}
}

func TestBadRequests(t *testing.T) {
	dir := shortDir(t)
	cfg, _ := fakeConfig(t, dir, waitingFixture(t, dir))
	startHost(t, cfg)
	for _, tc := range []struct {
		req  any
		want string
	}{
		{map[string]any{"op": "dance"}, `unknown op "dance"`},
		{map[string]any{"op": "send"}, "send needs text or images"},
		{map[string]any{"op": "send", "images": []any{map[string]any{"media_type": "image/png"}}}, "send image needs media_type and base64"},
		{map[string]any{"op": "answer", "request_id": "r", "behavior": "maybe"}, `answer behavior must be allow or deny, not "maybe"`},
		{map[string]any{"op": "set_mode", "mode": "yolo"}, `chatproto: unknown permission mode "yolo"`},
	} {
		if got := callErr(t, cfg.SocketPath, tc.req); got != tc.want {
			t.Errorf("%v: error %q, want %q", tc.req, got, tc.want)
		}
	}
}

func TestSingleHost(t *testing.T) {
	t.Run("a live socket refuses a second host", func(t *testing.T) {
		dir := shortDir(t)
		cfg, _ := fakeConfig(t, dir, waitingFixture(t, dir))
		startHost(t, cfg)
		second := cfg
		second.LogPath = filepath.Join(dir, "other.jsonl")
		_, err := Run(context.Background(), second, &syncBuffer{})
		if err == nil || !strings.Contains(err.Error(), "session host already running") {
			t.Fatalf("second Run err = %v, want already running", err)
		}
		state(t, cfg.SocketPath)
	})

	t.Run("a stale socket file is replaced", func(t *testing.T) {
		dir := shortDir(t)
		cfg, _ := fakeConfig(t, dir, waitingFixture(t, dir))
		ln, err := net.Listen("unix", cfg.SocketPath)
		if err != nil {
			t.Fatal(err)
		}
		ln.(*net.UnixListener).SetUnlinkOnClose(false)
		_ = ln.Close()
		if _, err := os.Stat(cfg.SocketPath); err != nil {
			t.Fatalf("stale socket file missing: %v", err)
		}
		r := startHost(t, cfg)
		state(t, cfg.SocketPath)
		r.cancel()
		r.wait(t)
		socketGone(t, cfg.SocketPath)
	})
}

func lastNote(t *testing.T, path string) (chatproto.HostNote, json.RawMessage) {
	t.Helper()
	entries := loadEntries(t, path)
	e := entries[len(entries)-1]
	note, err := chatproto.ParseHostNote(e.Payload)
	if err != nil {
		t.Fatalf("last entry %s is not a host note: %v", e.Payload, err)
	}
	return note, e.Payload
}

func TestShutdown(t *testing.T) {
	t.Run("claude exits when its stdin closes", func(t *testing.T) {
		dir := shortDir(t)
		cfg, _ := fakeConfig(t, dir, waitingFixture(t, dir))
		r := startHost(t, cfg)
		r.cancel()
		if res := r.wait(t); res.err != nil || res.code != 0 {
			t.Fatalf("Run = %d, %v; want 0", res.code, res.err)
		}
		if note, raw := lastNote(t, cfg.LogPath); note.Kind != chatproto.NoteExit || note.Code == nil || *note.Code != 0 {
			t.Errorf("last entry %s, want exit code 0", raw)
		}
		if !strings.Contains(r.pane.String(), "── claude exited 0") {
			t.Errorf("pane does not show the exit:\n%s", r.pane)
		}
		socketGone(t, cfg.SocketPath)
	})

	t.Run("claude that ignores stdin and SIGTERM is killed", func(t *testing.T) {
		dir := shortDir(t)
		cfg, _ := fakeConfig(t, dir, waitingFixture(t, dir))
		cfg.stdinGrace, cfg.killGrace = 100*time.Millisecond, 200*time.Millisecond
		t.Setenv(envFakeStubborn, "1")
		childFile := filepath.Join(dir, "child.pid")
		if runtime.GOOS != "windows" {
			t.Setenv(envFakeChildPID, childFile)
		}
		r := startHost(t, cfg)
		var child int
		if runtime.GOOS != "windows" {
			eventually(t, "the fake's child pid", func() bool {
				b, err := os.ReadFile(childFile)
				child, _ = strconv.Atoi(string(b))
				return err == nil && child > 0
			})
			t.Cleanup(func() { killIfAlive(child) })
		}
		started := time.Now()
		r.cancel()
		res := r.wait(t)
		if res.err != nil {
			t.Fatal(res.err)
		}
		if took := time.Since(started); took > 5*time.Second {
			t.Errorf("shutdown took %v", took)
		}
		note, raw := lastNote(t, cfg.LogPath)
		if note.Kind != chatproto.NoteExit {
			t.Fatalf("last entry %s, want an exit note", raw)
		}
		socketGone(t, cfg.SocketPath)
		if runtime.GOOS == "windows" {
			return
		}
		if note.Code != nil || !bytes.Contains(raw, []byte(`"signal":"SIGKILL"`)) || res.code != 128+9 {
			t.Errorf("exit note %s, Run = %d; want SIGKILL and 137", raw, res.code)
		}
		eventually(t, "claude's child to die with its process group", func() bool { return !alive(child) })
	})
}
