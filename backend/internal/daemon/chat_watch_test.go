package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionhost"
)

const watchTimeout = 10 * time.Second

func canUseTool(id string) string {
	return fmt.Sprintf(`{"type":"control_request","request_id":%q,"request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"ls"}}}`, id)
}

func answer(t *testing.T, id string) string {
	t.Helper()
	b, err := chatproto.Encode(chatproto.PermissionAnswer{RequestID: id, Decision: chatproto.Allow{UpdatedInput: json.RawMessage(`{"command":"ls"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func echo(id string) string {
	return fmt.Sprintf(`{"type":"control_response","response":{"subtype":"success","request_id":%q,"response":{"behavior":"allow","updatedInput":{}}}}`, id)
}

func cancelRequest(id string) string {
	return fmt.Sprintf(`{"type":"control_cancel_request","request_id":%q}`, id)
}

const (
	spawnNote     = `{"kind":"spawn","argv":["claude"],"resume":"","pid":7}`
	exitNote      = `{"kind":"exit","code":1,"signal":null}`
	originNote    = `{"kind":"user_origin","uuid":"u-1","origin":{"via":"ao_send","from":"p-2"}}`
	assistantText = `{"type":"assistant","message":{"id":"m-1","role":"assistant","content":[{"type":"text","text":"hi"}]}}`
	initRequest   = `{"type":"control_request","request_id":"init-1","request":{"subtype":"initialize"}}`
)

func entry(dir chatproto.Direction, payload string) chatproto.Entry {
	return chatproto.Entry{Seq: 1, Dir: dir, Payload: json.RawMessage(payload)}
}

func TestFoldPending(t *testing.T) {
	two := pendingSet{"r-1": {}, "r-2": {}}
	cases := []struct {
		name string
		in   pendingSet
		e    chatproto.Entry
		want pendingSet
	}{
		{"claude's can_use_tool adds its id", pendingSet{}, entry(chatproto.DirOut, canUseTool("r-1")), pendingSet{"r-1": {}}},
		{"a repeated request changes nothing", pendingSet{"r-1": {}}, entry(chatproto.DirOut, canUseTool("r-1")), pendingSet{"r-1": {}}},
		{"the host's own control request adds nothing", pendingSet{}, entry(chatproto.DirIn, initRequest), pendingSet{}},
		{"the host's answer removes its id", two, entry(chatproto.DirIn, answer(t, "r-1")), pendingSet{"r-2": {}}},
		{"claude's echo of an answer removes its id", two, entry(chatproto.DirOut, echo("r-2")), pendingSet{"r-1": {}}},
		{"a cancel removes its id", two, entry(chatproto.DirOut, cancelRequest("r-1")), pendingSet{"r-2": {}}},
		{"an answer to another request changes nothing", two, entry(chatproto.DirOut, echo("init-1")), two},
		{"a spawn note clears the set", two, entry(chatproto.DirHost, spawnNote), pendingSet{}},
		{"an exit note clears the set", two, entry(chatproto.DirHost, exitNote), pendingSet{}},
		{"an origin note changes nothing", two, entry(chatproto.DirHost, originNote), two},
		{"an assistant message changes nothing", two, entry(chatproto.DirOut, assistantText), two},
		{"an unparseable line changes nothing", two, entry(chatproto.DirOut, `not json`), two},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := maps.Clone(tc.in)
			got := foldPending(tc.in, tc.e)
			if !maps.Equal(got, tc.want) {
				t.Errorf("foldPending = %v, want %v", got, tc.want)
			}
			if !maps.Equal(tc.in, before) {
				t.Errorf("foldPending changed its input to %v, was %v", tc.in, before)
			}
		})
	}
}

var errHostDown = errors.New("host down")

type fakeChatHost struct {
	mu        sync.Mutex
	entries   []chatproto.Entry
	folder    *chatproto.Folder
	down      bool
	epoch     int
	wake      chan struct{}
	froms     []uint64
	streams   int
	delivered uint64
}

func newFakeChatHost(t *testing.T, lines ...fixtureEntry) *fakeChatHost {
	t.Helper()
	h := &fakeChatHost{folder: chatproto.NewFolder(), wake: make(chan struct{})}
	for _, l := range lines {
		h.add(t, l.dir, l.payload)
	}
	return h
}

type fixtureEntry struct {
	dir     chatproto.Direction
	payload string
}

func (h *fakeChatHost) notifyLocked() {
	close(h.wake)
	h.wake = make(chan struct{})
}

func (h *fakeChatHost) add(t *testing.T, dir chatproto.Direction, payload string) uint64 {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	e := chatproto.Entry{Seq: uint64(len(h.entries) + 1), TS: time.Now().UTC(), Dir: dir, Payload: json.RawMessage(payload)}
	if err := h.folder.Apply(e); err != nil {
		t.Fatal(err)
	}
	h.entries = append(h.entries, e)
	h.notifyLocked()
	return e.Seq
}

func (h *fakeChatHost) lastSeq() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return uint64(len(h.entries))
}

func (h *fakeChatHost) drop() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.epoch++
	h.notifyLocked()
}

func (h *fakeChatHost) setDown(down bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.down = down
}

func (h *fakeChatHost) snapshot() (froms []uint64, streams int, delivered uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.froms), h.streams, h.delivered
}

func (h *fakeChatHost) State(context.Context) (sessionhost.State, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.down {
		return sessionhost.State{}, errHostDown
	}
	st := sessionhost.State{LastSeq: uint64(len(h.entries))}
	for _, p := range h.folder.Transcript().Pending {
		st.Pending = append(st.Pending, sessionhost.PendingRequest{RequestID: p.RequestID, ToolName: p.ToolName})
	}
	return st, nil
}

func (h *fakeChatHost) Subscribe(ctx context.Context, from uint64, _ func(), fn func(sessionhost.Frame) error) error {
	h.mu.Lock()
	if h.down {
		h.mu.Unlock()
		return errHostDown
	}
	h.froms = append(h.froms, from)
	h.streams++
	epoch := h.epoch
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.streams--
		h.notifyLocked()
		h.mu.Unlock()
	}()
	cursor := from
	for {
		h.mu.Lock()
		if h.epoch != epoch {
			h.mu.Unlock()
			return nil
		}
		var next []chatproto.Entry
		if int(cursor) < len(h.entries) {
			next = slices.Clone(h.entries[cursor:])
		}
		wake := h.wake
		h.mu.Unlock()
		for _, e := range next {
			if err := fn(sessionhost.Frame{Entry: &e}); err != nil {
				return err
			}
			cursor = e.Seq
			h.mu.Lock()
			h.delivered = e.Seq
			h.notifyLocked()
			h.mu.Unlock()
		}
		if len(next) > 0 {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wake:
		}
	}
}

type signalLog struct {
	mu     sync.Mutex
	ids    []domain.SessionID
	states []domain.ActivityState
	events []domain.ActivityEvent
}

func (s *signalLog) ApplyActivitySignal(_ context.Context, id domain.SessionID, sig ports.ActivitySignal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ids = append(s.ids, id)
	s.states = append(s.states, sig.State)
	return nil
}

func (s *signalLog) Publish(_ context.Context, ev domain.ActivityEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
	return nil
}

func (s *signalLog) got() []domain.ActivityState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.states)
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(watchTimeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

type watcherRig struct {
	host  *fakeChatHost
	sigs  *signalLog
	waits chan time.Duration
	fire  chan time.Time
}

func startWatcher(t *testing.T, host *fakeChatHost, stored domain.ActivityState) *watcherRig {
	t.Helper()
	r := &watcherRig{host: host, sigs: &signalLog{}, waits: make(chan time.Duration, 64), fire: make(chan time.Time)}
	w := &chatWatcher{
		id: "p-1", host: host, rec: r.sigs, feed: r.sigs, log: slog.New(slog.DiscardHandler),
		after: func(d time.Duration) <-chan time.Time {
			r.waits <- d
			return r.fire
		},
		reportedWaiting: stored == domain.ActivityWaitingInput,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(watchTimeout):
			t.Error("the watcher did not stop when its context ended")
		}
	})
	return r
}

func (r *watcherRig) add(t *testing.T, dir chatproto.Direction, payload string) {
	t.Helper()
	seq := r.host.add(t, dir, payload)
	r.caughtUp(t, seq)
}

func (r *watcherRig) caughtUp(t *testing.T, seq uint64) {
	t.Helper()
	waitUntil(t, fmt.Sprintf("the watcher to handle seq %d", seq), func() bool {
		_, _, delivered := r.host.snapshot()
		return delivered >= seq
	})
}

func (r *watcherRig) subscribed(t *testing.T, n int) {
	t.Helper()
	waitUntil(t, fmt.Sprintf("subscription %d", n), func() bool {
		froms, streams, _ := r.host.snapshot()
		return len(froms) == n && streams == 1
	})
}

func (r *watcherRig) wantSignals(t *testing.T, want ...domain.ActivityState) {
	t.Helper()
	if got := r.sigs.got(); !slices.Equal(got, want) {
		t.Fatalf("signals = %v, want %v", got, want)
	}
}

func (r *watcherRig) nextWait(t *testing.T) time.Duration {
	t.Helper()
	select {
	case d := <-r.waits:
		return d
	case <-time.After(watchTimeout):
		t.Fatal("the watcher never waited to retry")
		return 0
	}
}

func spawned(t *testing.T) *fakeChatHost {
	return newFakeChatHost(t, fixtureEntry{chatproto.DirHost, spawnNote})
}

func TestChatWatcher_PendingRequestSignalsWaitingOnce(t *testing.T) {
	r := startWatcher(t, spawned(t), domain.ActivityIdle)
	r.subscribed(t, 1)

	r.add(t, chatproto.DirOut, canUseTool("r-1"))
	r.add(t, chatproto.DirOut, assistantText)
	r.add(t, chatproto.DirHost, originNote)
	r.wantSignals(t, domain.ActivityWaitingInput)

	r.sigs.mu.Lock()
	defer r.sigs.mu.Unlock()
	if len(r.sigs.events) != 1 || r.sigs.events[0].SessionID != "p-1" || r.sigs.events[0].Coarse != domain.CoarseWaiting {
		t.Fatalf("feed got %+v, want one waiting frame for p-1", r.sigs.events)
	}
}

func TestChatWatcher_AnswerSignalsActiveOnce(t *testing.T) {
	r := startWatcher(t, spawned(t), domain.ActivityIdle)
	r.subscribed(t, 1)
	r.add(t, chatproto.DirOut, canUseTool("r-1"))

	r.add(t, chatproto.DirIn, answer(t, "r-1"))
	r.add(t, chatproto.DirOut, echo("r-1"))
	r.add(t, chatproto.DirOut, assistantText)
	r.wantSignals(t, domain.ActivityWaitingInput, domain.ActivityActive)
}

func TestChatWatcher_OverlappingRequestsSignalOncePerTransition(t *testing.T) {
	r := startWatcher(t, spawned(t), domain.ActivityIdle)
	r.subscribed(t, 1)
	r.add(t, chatproto.DirOut, canUseTool("r-1"))
	r.add(t, chatproto.DirOut, canUseTool("r-2"))
	r.add(t, chatproto.DirIn, answer(t, "r-1"))
	r.wantSignals(t, domain.ActivityWaitingInput)

	r.add(t, chatproto.DirOut, cancelRequest("r-2"))
	r.wantSignals(t, domain.ActivityWaitingInput, domain.ActivityActive)
}

func TestChatWatcher_SpawnClearsARequestACrashLeft(t *testing.T) {
	r := startWatcher(t, spawned(t), domain.ActivityIdle)
	r.subscribed(t, 1)
	r.add(t, chatproto.DirOut, canUseTool("r-1"))
	r.add(t, chatproto.DirHost, spawnNote)
	r.wantSignals(t, domain.ActivityWaitingInput, domain.ActivityActive)
}

func TestChatWatcher_ReconnectsFromTheLastSeqAndReplaysNothing(t *testing.T) {
	r := startWatcher(t, spawned(t), domain.ActivityIdle)
	r.subscribed(t, 1)
	r.add(t, chatproto.DirOut, canUseTool("r-1"))

	r.host.drop()
	if d := r.nextWait(t); d != chatRetryFirst {
		t.Fatalf("first retry after %v, want %v", d, chatRetryFirst)
	}
	answered := r.host.add(t, chatproto.DirIn, answer(t, "r-1"))
	r.fire <- time.Time{}
	r.subscribed(t, 2)
	r.add(t, chatproto.DirOut, canUseTool("r-2"))

	r.wantSignals(t, domain.ActivityWaitingInput, domain.ActivityActive, domain.ActivityWaitingInput)
	froms, _, _ := r.host.snapshot()
	if want := []uint64{1, answered}; !slices.Equal(froms, want) {
		t.Fatalf("subscribed from %v, want %v", froms, want)
	}
}

func TestChatWatcher_RetriesWithBackoffWhileTheHostIsDown(t *testing.T) {
	host := spawned(t)
	host.setDown(true)
	r := startWatcher(t, host, domain.ActivityIdle)
	var waits []time.Duration
	for range 6 {
		waits = append(waits, r.nextWait(t))
		r.fire <- time.Time{}
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 10 * time.Second, 10 * time.Second}
	if !slices.Equal(waits, want) {
		t.Fatalf("retry waits = %v, want %v", waits, want)
	}

	if d := r.nextWait(t); d != 10*time.Second {
		t.Fatalf("retry wait = %v, want %v", d, 10*time.Second)
	}
	host.setDown(false)
	r.fire <- time.Time{}
	r.subscribed(t, 1)
	host.drop()
	if d := r.nextWait(t); d != chatRetryFirst {
		t.Fatalf("retry after a connection = %v, want the backoff reset to %v", d, chatRetryFirst)
	}
	r.wantSignals(t)
}

func TestChatWatcher_StartsFromTheHostStateAndTheStoredActivity(t *testing.T) {
	cases := []struct {
		name    string
		stored  domain.ActivityState
		pending bool
		want    []domain.ActivityState
	}{
		{"a request pending at start is signalled without replay", domain.ActivityIdle, true, []domain.ActivityState{domain.ActivityWaitingInput}},
		{"a session already waiting is not told again", domain.ActivityWaitingInput, true, nil},
		{"a request settled while nobody watched is reported", domain.ActivityWaitingInput, false, []domain.ActivityState{domain.ActivityActive}},
		{"an idle session with nothing pending is left alone", domain.ActivityIdle, false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host := spawned(t)
			if tc.pending {
				host.add(t, chatproto.DirOut, canUseTool("r-1"))
			}
			last := host.lastSeq()
			r := startWatcher(t, host, tc.stored)
			r.subscribed(t, 1)
			r.add(t, chatproto.DirOut, assistantText)

			froms, _, _ := host.snapshot()
			if froms[0] != last {
				t.Fatalf("subscribed from %d, want the state's last seq %d", froms[0], last)
			}
			r.wantSignals(t, tc.want...)
		})
	}
}

func TestChatWatch_ReconcileFollowsTheLiveChatSessions(t *testing.T) {
	w := newChatWiring(t)
	chatA := w.session(t, "p-a", domain.TransportChat)
	w.session(t, "p-term", domain.TransportTerminal)

	var mu sync.Mutex
	hosts := map[domain.SessionID]*fakeChatHost{}
	host := func(id domain.SessionID) *fakeChatHost {
		mu.Lock()
		defer mu.Unlock()
		return hosts[id]
	}
	sigs := &signalLog{}
	watch := &chatWatch{
		sessions: w.store,
		host: func(id domain.SessionID) chatHost {
			mu.Lock()
			defer mu.Unlock()
			if hosts[id] == nil {
				hosts[id] = spawned(t)
			}
			return hosts[id]
		},
		rec: sigs, feed: sigs, log: slog.New(slog.DiscardHandler),
		after: func(time.Duration) <-chan time.Time { return nil },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tick := make(chan time.Time)
	done := make(chan struct{})
	go func() {
		defer close(done)
		watch.run(ctx, tick, func() {})
	}()
	reconciled := func() { tick <- time.Time{} }
	streams := func(id domain.SessionID) int {
		_, n, _ := host(id).snapshot()
		return n
	}

	reconciled()
	waitUntil(t, "a watcher on the chat session", func() bool { return host(chatA.ID) != nil && streams(chatA.ID) == 1 })
	mu.Lock()
	if len(hosts) != 1 {
		t.Fatalf("watched %v, want only the chat session %s", slices.Collect(maps.Keys(hosts)), chatA.ID)
	}
	mu.Unlock()

	chatB := w.session(t, "p-b", domain.TransportChat)
	reconciled()
	waitUntil(t, "a watcher on the new chat session", func() bool { return host(chatB.ID) != nil && streams(chatB.ID) == 1 })
	if froms, _, _ := host(chatA.ID).snapshot(); len(froms) != 1 {
		t.Fatalf("the first session was subscribed %d times, want once", len(froms))
	}

	rec, _, err := w.store.GetSession(ctx, chatA.ID)
	if err != nil {
		t.Fatal(err)
	}
	rec.IsTerminated = true
	if err := w.store.UpdateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
	reconciled()
	reconciled()
	if streams(chatA.ID) != 0 || streams(chatB.ID) != 1 {
		t.Fatalf("streams: terminated %d, live %d; want 0 and 1", streams(chatA.ID), streams(chatB.ID))
	}

	cancel()
	select {
	case <-done:
	case <-time.After(watchTimeout):
		t.Fatal("run did not return when its context ended")
	}
	if streams(chatB.ID) != 0 {
		t.Fatal("a watcher outlived the watch")
	}
	if got := sigs.got(); len(got) != 0 {
		t.Fatalf("signals = %v, want none for sessions with nothing pending", got)
	}
}
