package terminal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionhost"
)

const chatTimeout = 15 * time.Second

// fakeClaude answers a user message as claude does with --replay-user-messages
// and --include-partial-messages: a stream event, then the echoed message.
const fakeClaude = `while IFS= read -r line; do
  case "$line" in
    *'"type":"user"'*)
      printf '%s\n' '{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}}'
      printf '%s\n' "$line" ;;
  esac
done`

type fakeChatSessions map[domain.SessionID]domain.Transport

func (f fakeChatSessions) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	tr, ok := f[id]
	if !ok {
		return domain.SessionRecord{}, false, nil
	}
	return domain.SessionRecord{ID: id, Transport: tr}, true, nil
}

func chatDataDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "chat")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func serveChat(t *testing.T, sessions ChatSessions, dataDir string) *fakeConn {
	t.Helper()
	var opts []Option
	if sessions != nil {
		opts = append(opts, WithChat(sessions, dataDir))
	}
	mgr := NewManager(&fakeSource{}, nil, testLogger(), append(opts, WithHeartbeat(0))...)
	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan struct{})
	go func() {
		defer close(served)
		mgr.Serve(ctx, conn)
	}()
	t.Cleanup(func() {
		cancel()
		<-served
		mgr.Close()
	})
	return conn
}

type chatHost struct {
	client sessionhost.Client
	cancel context.CancelFunc
	done   chan struct{}
}

func startChatHost(t *testing.T, dataDir, id string) *chatHost {
	t.Helper()
	logPath, _ := sessionhost.Paths(dataDir, id)
	return startChatHostLoggingTo(t, dataDir, id, logPath)
}

func startChatHostLoggingTo(t *testing.T, dataDir, id, logPath string) *chatHost {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake claude is a POSIX shell script")
	}
	_, socket, err := sessionhost.PreparePaths(dataDir, id)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := &chatHost{client: sessionhost.NewClient(socket), cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(h.done)
		cfg := sessionhost.Config{Command: []string{"/bin/sh", "-c", fakeClaude, "fake-claude"}, LogPath: logPath, SocketPath: socket}
		if _, err := sessionhost.Run(ctx, cfg, io.Discard); err != nil {
			t.Errorf("session host: %v", err)
		}
	}()
	t.Cleanup(h.stop)
	h.waitForSeq(t, 2)
	return h
}

func (h *chatHost) stop() {
	h.cancel()
	<-h.done
}

func (h *chatHost) waitForSeq(t *testing.T, seq uint64) {
	t.Helper()
	eventually(t, chatTimeout, func() bool {
		st, err := h.client.State(context.Background())
		return err == nil && st.LastSeq == seq
	})
}

func (h *chatHost) send(t *testing.T, text string) {
	t.Helper()
	if _, err := h.client.Send(context.Background(), chatproto.UserInput{Text: text}, nil); err != nil {
		t.Fatal(err)
	}
}

func writeChatLog(t *testing.T, dataDir, id string, n int, tail string) {
	t.Helper()
	logPath, _, err := sessionhost.PreparePaths(dataDir, id)
	if err != nil {
		t.Fatal(err)
	}
	var data []byte
	for seq := 1; seq <= n; seq++ {
		b, err := json.Marshal(chatproto.Entry{Seq: uint64(seq), TS: time.Unix(int64(seq), 0).UTC(), Dir: chatproto.DirOut, Payload: json.RawMessage(`{"type":"system"}`)})
		if err != nil {
			t.Fatal(err)
		}
		data = append(append(data, b...), '\n')
	}
	if err := os.WriteFile(logPath, append(data, tail...), 0o600); err != nil {
		t.Fatal(err)
	}
}

func nextFrame(t *testing.T, conn *fakeConn) serverMsg {
	t.Helper()
	select {
	case m := <-conn.out:
		return m
	case <-time.After(chatTimeout):
		t.Fatal("no frame arrived")
		return serverMsg{}
	}
}

func readChat(t *testing.T, conn *fakeConn, id, stop string) []serverMsg {
	t.Helper()
	var got []serverMsg
	for {
		m := nextFrame(t, conn)
		if m.Ch != chChat || m.ID != id {
			continue
		}
		got = append(got, m)
		if m.Type == stop {
			return got
		}
		switch m.Type {
		case msgError, msgOffline, msgLive:
			t.Fatalf("chat %s sent %s (%q) while waiting for %s; frames so far: %s", id, m.Type, m.Error, stop, describe(got))
		}
	}
}

func readEntries(t *testing.T, conn *fakeConn, id string, last uint64) []serverMsg {
	t.Helper()
	var got []serverMsg
	for {
		m := nextFrame(t, conn)
		if m.Ch != chChat || m.ID != id {
			continue
		}
		got = append(got, m)
		if m.Type != msgEntry && m.Type != msgDelta {
			t.Fatalf("chat %s sent %s (%q) while waiting for seq %d; frames so far: %s", id, m.Type, m.Error, last, describe(got))
		}
		if m.Entry != nil && m.Entry.Seq >= last {
			return got
		}
	}
}

func pingBarrier(t *testing.T, conn *fakeConn) []serverMsg {
	t.Helper()
	conn.in <- clientMsg{Ch: chSystem, Type: msgPing}
	var got []serverMsg
	for {
		m := nextFrame(t, conn)
		if m.Ch == chSystem && m.Type == msgPong {
			return got
		}
		got = append(got, m)
	}
}

func describe(frames []serverMsg) []string {
	out := make([]string, 0, len(frames))
	for _, m := range frames {
		if m.Entry != nil {
			out = append(out, fmt.Sprintf("%s:%d", m.Type, m.Entry.Seq))
			continue
		}
		out = append(out, m.Type)
	}
	return out
}

func wantFrames(t *testing.T, label string, got []serverMsg, want ...string) {
	t.Helper()
	if d := describe(got); !slices.Equal(d, want) {
		t.Fatalf("%s: frames = %v, want %v", label, d, want)
	}
}

func TestChatOpenReplaysTheLogThenStreamsTheHost(t *testing.T) {
	dir := chatDataDir(t)
	h := startChatHost(t, dir, "s1")
	conn := serveChat(t, fakeChatSessions{"s1": domain.TransportChat}, dir)

	conn.in <- clientMsg{Ch: chChat, Type: msgOpen, ID: "s1"}
	wantFrames(t, "replay", readChat(t, conn, "s1", msgLive), "entry:1", "entry:2", "live")

	h.send(t, "hello")
	got := readEntries(t, conn, "s1", 4)
	wantFrames(t, "live", got, "entry:3", "delta", "entry:4")
	if got[0].Entry.Dir != chatproto.DirIn || got[2].Entry.Dir != chatproto.DirOut {
		t.Errorf("live entries have dirs %s and %s, want the message in and its echo out", got[0].Entry.Dir, got[2].Entry.Dir)
	}
	if kind := messageType(t, got[1].Delta); kind != "stream_event" {
		t.Errorf("the delta is a %q line, want the raw stream_event", kind)
	}
	wantFrames(t, "after the turn", pingBarrier(t, conn))
}

func TestChatOpenFromSeqSendsOnlyNewerEntries(t *testing.T) {
	dir := chatDataDir(t)
	h := startChatHost(t, dir, "s1")
	h.send(t, "first")
	h.waitForSeq(t, 4)
	conn := serveChat(t, fakeChatSessions{"s1": domain.TransportChat}, dir)

	conn.in <- clientMsg{Ch: chChat, Type: msgOpen, ID: "s1", FromSeq: 2}
	wantFrames(t, "replay from 2", readChat(t, conn, "s1", msgLive), "entry:3", "entry:4", "live")

	h.send(t, "second")
	wantFrames(t, "live", readEntries(t, conn, "s1", 6), "entry:5", "delta", "entry:6")
	wantFrames(t, "after the turn", pingBarrier(t, conn))
}

func TestChatEntriesLoggedAfterTheReplayArriveOnceFromTheHost(t *testing.T) {
	dir := chatDataDir(t)
	hostLog := filepath.Join(dir, "host-events.jsonl")
	h := startChatHostLoggingTo(t, dir, "s1", hostLog)
	snapshot, err := os.ReadFile(hostLog)
	if err != nil {
		t.Fatal(err)
	}
	muxLog, _ := sessionhost.Paths(dir, "s1")
	if err := os.WriteFile(muxLog, snapshot, 0o600); err != nil {
		t.Fatal(err)
	}
	h.send(t, "after the snapshot")
	h.waitForSeq(t, 4)
	conn := serveChat(t, fakeChatSessions{"s1": domain.TransportChat}, dir)

	conn.in <- clientMsg{Ch: chChat, Type: msgOpen, ID: "s1"}
	wantFrames(t, "replay from disk", readChat(t, conn, "s1", msgLive), "entry:1", "entry:2", "live")
	wantFrames(t, "catch-up from the host", readEntries(t, conn, "s1", 4), "entry:3", "entry:4")
	wantFrames(t, "after the catch-up", pingBarrier(t, conn))
}

func TestChatWithoutAHostReplaysTheLogThenGoesOffline(t *testing.T) {
	dir := chatDataDir(t)
	writeChatLog(t, dir, "s1", 5, "")
	conn := serveChat(t, fakeChatSessions{"s1": domain.TransportChat}, dir)

	conn.in <- clientMsg{Ch: chChat, Type: msgOpen, ID: "s1"}
	wantFrames(t, "from 0", readChat(t, conn, "s1", msgOffline), "entry:1", "entry:2", "entry:3", "entry:4", "entry:5", "offline")

	conn.in <- clientMsg{Ch: chChat, Type: msgOpen, ID: "s1", FromSeq: 3}
	wantFrames(t, "reopened from 3", readChat(t, conn, "s1", msgOffline), "entry:4", "entry:5", "offline")
}

func TestChatWithNoLogYetGoesOffline(t *testing.T) {
	conn := serveChat(t, fakeChatSessions{"s1": domain.TransportChat}, chatDataDir(t))
	conn.in <- clientMsg{Ch: chChat, Type: msgOpen, ID: "s1"}
	wantFrames(t, "no log", readChat(t, conn, "s1", msgOffline), "offline")
}

func TestChatReplayLeavesOutALineTheHostIsStillWriting(t *testing.T) {
	dir := chatDataDir(t)
	writeChatLog(t, dir, "s1", 2, `{"seq":3,"ts":"2026-`)
	conn := serveChat(t, fakeChatSessions{"s1": domain.TransportChat}, dir)

	conn.in <- clientMsg{Ch: chChat, Type: msgOpen, ID: "s1"}
	wantFrames(t, "torn tail", readChat(t, conn, "s1", msgOffline), "entry:1", "entry:2", "offline")
}

func TestChatReplayLongerThanTheWriteBufferArrivesWhole(t *testing.T) {
	dir := chatDataDir(t)
	n := 3 * defaultWriteBuffer
	writeChatLog(t, dir, "s1", n, "")
	conn := serveChat(t, fakeChatSessions{"s1": domain.TransportChat}, dir)

	conn.in <- clientMsg{Ch: chChat, Type: msgOpen, ID: "s1"}
	got := readChat(t, conn, "s1", msgOffline)
	if len(got) != n+1 {
		t.Fatalf("got %d frames, want %d entries and offline", len(got), n)
	}
	for i, m := range got[:n] {
		if m.Entry == nil || m.Entry.Seq != uint64(i+1) {
			t.Fatalf("frame %d is %s, want entry %d", i, describe([]serverMsg{m}), i+1)
		}
	}
}

func TestChatHostExitAfterLiveSendsOffline(t *testing.T) {
	dir := chatDataDir(t)
	h := startChatHost(t, dir, "s1")
	conn := serveChat(t, fakeChatSessions{"s1": domain.TransportChat}, dir)

	conn.in <- clientMsg{Ch: chChat, Type: msgOpen, ID: "s1"}
	readChat(t, conn, "s1", msgLive)
	h.stop()
	got := readChat(t, conn, "s1", msgOffline)
	wantFrames(t, "host exit", got, "entry:3", "offline")
	if note, err := chatproto.ParseHostNote(got[0].Entry.Payload); err != nil || note.Kind != chatproto.NoteExit {
		t.Errorf("the last entry before offline is %s (%v), want the host's exit note", got[0].Entry.Payload, err)
	}
}

func TestChatOpenOfAnythingButAChatSessionIsAnError(t *testing.T) {
	sessions := fakeChatSessions{"t1": domain.TransportTerminal, "old": ""}
	for _, tc := range []struct {
		name     string
		sessions ChatSessions
		id       string
	}{
		{"terminal session", sessions, "t1"},
		{"session from before transports", sessions, "old"},
		{"unknown session", sessions, "nope"},
		{"chat not served", nil, "t1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := serveChat(t, tc.sessions, chatDataDir(t))
			conn.in <- clientMsg{Ch: chChat, Type: msgOpen, ID: tc.id}
			got := readChat(t, conn, tc.id, msgError)
			if len(got) != 1 || got[0].Error == "" {
				t.Fatalf("frames = %v, want one error with a reason", describe(got))
			}
		})
	}
	t.Run("missing id", func(t *testing.T) {
		conn := serveChat(t, sessions, chatDataDir(t))
		conn.in <- clientMsg{Ch: chChat, Type: msgOpen}
		if got := readChat(t, conn, "", msgError); got[0].Error == "" {
			t.Fatal("the error has no reason")
		}
	})
}

func TestChatCloseStopsTheStream(t *testing.T) {
	dir := chatDataDir(t)
	h := startChatHost(t, dir, "s1")
	conn := serveChat(t, fakeChatSessions{"s1": domain.TransportChat}, dir)

	conn.in <- clientMsg{Ch: chChat, Type: msgOpen, ID: "s1"}
	readChat(t, conn, "s1", msgLive)
	conn.in <- clientMsg{Ch: chChat, Type: msgClose, ID: "s1"}
	wantFrames(t, "close", pingBarrier(t, conn))

	h.send(t, "after close")
	h.waitForSeq(t, 4)
	conn.in <- clientMsg{Ch: chChat, Type: msgOpen, ID: "s1", FromSeq: 2}
	wantFrames(t, "reopened", readChat(t, conn, "s1", msgLive), "entry:3", "entry:4", "live")
	h.send(t, "reopened")
	wantFrames(t, "reopened live", readEntries(t, conn, "s1", 6), "entry:5", "delta", "entry:6")
	wantFrames(t, "after the turn", pingBarrier(t, conn))
}

func messageType(t *testing.T, line json.RawMessage) string {
	t.Helper()
	var w struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &w); err != nil {
		t.Fatalf("delta %q: %v", line, err)
	}
	return w.Type
}

const chatImageB64 = "aGVsbG8gaW1hZ2U="

func writeImageLog(t *testing.T, dataDir, id string) []byte {
	t.Helper()
	logPath, _, err := sessionhost.PreparePaths(dataDir, id)
	if err != nil {
		t.Fatal(err)
	}
	read := `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"t1","type":"tool_result","content":[{"type":"image","source":{"type":"base64","data":"` + chatImageB64 + `","media_type":"image/png"}}]}]},"tool_use_result":{"type":"image","file":{"base64":"` + chatImageB64 + `","type":"image/png"}}}`
	log := []byte(`{"seq":1,"ts":"2026-10-30T00:00:00Z","dir":"out","payload":{"type":"system","subtype":"init"}}` + "\n" +
		`{"seq":2,"ts":"2026-10-30T00:00:01Z","dir":"out","payload":` + read + `}` + "\n")
	if err := os.WriteFile(logPath, log, 0o600); err != nil {
		t.Fatal(err)
	}
	return log
}

func TestChatOpenWithBlobRefsSendsAnOldLogsImagesAsRefs(t *testing.T) {
	dir := chatDataDir(t)
	log := writeImageLog(t, dir, "s1")
	conn := serveChat(t, fakeChatSessions{"s1": domain.TransportChat}, dir)

	conn.in <- clientMsg{Ch: chChat, Type: msgOpen, ID: "s1", BlobRefs: true}
	got := readChat(t, conn, "s1", msgOffline)
	wantFrames(t, "replay", got, "entry:1", "entry:2", "offline")
	p := string(got[1].Entry.Payload)
	if strings.Contains(p, chatImageB64) || strings.Count(p, `"blob":"`) != 2 || !strings.Contains(p, `"type":"ao_blob"`) {
		t.Errorf("payload %s, want both copies replaced by refs", p)
	}
	logPath, _ := sessionhost.Paths(dir, "s1")
	if now, _ := os.ReadFile(logPath); !bytes.Equal(now, log) {
		t.Errorf("the open rewrote the log")
	}
	blobs, err := os.ReadDir(filepath.Join(filepath.Dir(logPath), "blobs"))
	if err != nil || len(blobs) != 1 || !strings.Contains(p, blobs[0].Name()) {
		t.Errorf("blobs = %v, %v; want the one the refs name", blobs, err)
	}
}

func TestChatOpenWithoutBlobRefsSendsEachImageOnce(t *testing.T) {
	dir := chatDataDir(t)
	writeImageLog(t, dir, "s1")
	conn := serveChat(t, fakeChatSessions{"s1": domain.TransportChat}, dir)

	conn.in <- clientMsg{Ch: chChat, Type: msgOpen, ID: "s1"}
	got := readChat(t, conn, "s1", msgOffline)
	p := string(got[1].Entry.Payload)
	if n := strings.Count(p, chatImageB64); n != 1 || !strings.Contains(p, `"source":{"data":"`+chatImageB64+`","media_type":"image/png","type":"base64"}`) {
		t.Errorf("payload %s carries the image %d times, want once, as the base64 source", p, n)
	}
}

func TestChatLiveImagesFollowTheOpensBlobRefs(t *testing.T) {
	dir := chatDataDir(t)
	h := startChatHost(t, dir, "s1")
	refs := serveChat(t, fakeChatSessions{"s1": domain.TransportChat}, dir)
	inline := serveChat(t, fakeChatSessions{"s1": domain.TransportChat}, dir)
	refs.in <- clientMsg{Ch: chChat, Type: msgOpen, ID: "s1", BlobRefs: true}
	inline.in <- clientMsg{Ch: chChat, Type: msgOpen, ID: "s1"}
	readChat(t, refs, "s1", msgLive)
	readChat(t, inline, "s1", msgLive)

	if _, err := h.client.Send(context.Background(), chatproto.UserInput{Text: "look", Images: []chatproto.Image{{MediaType: "image/png", Base64: chatImageB64}}}, nil); err != nil {
		t.Fatal(err)
	}
	r := readEntries(t, refs, "s1", 3)[0].Entry.Payload
	i := readEntries(t, inline, "s1", 3)[0].Entry.Payload
	if bytes.Contains(r, []byte(chatImageB64)) || !bytes.Contains(r, []byte(`"type":"ao_blob"`)) {
		t.Errorf("blobRefs client got %s, want a ref", r)
	}
	if !bytes.Contains(i, []byte(`"data":"`+chatImageB64+`"`)) {
		t.Errorf("inline client got %s, want the base64 source", i)
	}
}
