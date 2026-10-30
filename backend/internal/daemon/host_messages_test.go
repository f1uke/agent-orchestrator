package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/runtimeselect"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/inputgate"
	"github.com/aoagents/agent-orchestrator/backend/internal/msgdelivery"
	"github.com/aoagents/agent-orchestrator/backend/internal/msgorigin"
	"github.com/aoagents/agent-orchestrator/backend/internal/msgqueue"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	reviewcore "github.com/aoagents/agent-orchestrator/backend/internal/review"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionhost"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

type hostRequest struct {
	Op     string `json:"op"`
	Text   string `json:"text"`
	Origin *struct {
		Via  string `json:"via"`
		From string `json:"from"`
	} `json:"origin"`
}

type fakeHost struct {
	mu   sync.Mutex
	reqs []hostRequest
}

func (h *fakeHost) requests() []hostRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]hostRequest(nil), h.reqs...)
}

func startFakeHost(t *testing.T, dataDir string, id domain.SessionID) *fakeHost {
	t.Helper()
	_, sock, err := sessionhost.PreparePaths(dataDir, string(id))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sock, dataDir) {
		t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(sock)) })
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	h := &fakeHost{}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			line, _ := bufio.NewReader(conn).ReadBytes('\n')
			var req hostRequest
			_ = json.Unmarshal(line, &req)
			h.mu.Lock()
			h.reqs = append(h.reqs, req)
			h.mu.Unlock()
			_, _ = conn.Write([]byte(`{"ok":true,"uuid":"u-1"}` + "\n"))
			_ = conn.Close()
		}
	}()
	return h
}

type memJournal struct {
	mu      sync.Mutex
	entries []msgdelivery.Entry
}

func (j *memJournal) Append(e msgdelivery.Entry) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.entries = append(j.entries, e)
	return nil
}

func (j *memJournal) only(t *testing.T) msgdelivery.Entry {
	t.Helper()
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.entries) != 1 {
		t.Fatalf("journal has %d entries, want 1: %+v", len(j.entries), j.entries)
	}
	return j.entries[0]
}

type chatWiring struct {
	store   *sqlite.Store
	dataDir string
	inner   *fakeInnerRuntime
	gate    *inputgate.Gate
	journal *memJournal
	rt      runtimeselect.Runtime
}

func newChatWiring(t *testing.T) chatWiring {
	t.Helper()
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.UpsertProject(context.Background(), domain.ProjectRecord{ID: "p", Path: "/repo/p", RegisteredAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	w := chatWiring{
		store: store, dataDir: t.TempDir(), inner: &fakeInnerRuntime{}, journal: &memJournal{},
		gate: inputgate.New(inputgate.WithQuietWindow(time.Minute), inputgate.WithMaxDefer(time.Minute)),
	}
	w.rt = newGatedRuntime(w.inner, w.gate, &hostRouter{sessions: store, dataDir: w.dataDir, journal: w.journal})
	return w
}

func (w chatWiring) session(t *testing.T, handle string, transport domain.Transport) domain.SessionRecord {
	t.Helper()
	rec, err := w.store.CreateSession(context.Background(), domain.SessionRecord{
		ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Transport: transport,
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: time.Now()},
		Metadata: domain.SessionMetadata{RuntimeHandleID: handle},
	})
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestChatSession_SendReachesTheHostWithItsOrigin(t *testing.T) {
	w := newChatWiring(t)
	rec := w.session(t, "p-chat", domain.TransportChat)
	host := startFakeHost(t, w.dataDir, rec.ID)
	w.gate.NoteInput("p-chat")

	ctx, cancel := context.WithTimeout(msgorigin.WithSender(context.Background(), "p-9"), 5*time.Second)
	defer cancel()
	outcome, err := newSessionMessenger(w.store, w.rt, nil, nil).Send(ctx, rec.ID, "[from @p-9] hi")
	if err != nil {
		t.Fatal(err)
	}
	reqs := host.requests()
	if len(reqs) != 1 || reqs[0].Op != "send" || reqs[0].Text != "[from @p-9] hi" || reqs[0].Origin == nil ||
		reqs[0].Origin.Via != "ao_send" || reqs[0].Origin.From != "p-9" {
		t.Fatalf("host got %+v, want one send with origin ao_send from p-9", reqs)
	}
	if w.inner.sentCount() != 0 {
		t.Fatal("a chat session's message was also typed into its pane")
	}
	if outcome.Delivery.Path != msgdelivery.PathHost || outcome.Delivery.MsgID != "u-1" {
		t.Fatalf("delivery = %+v, want host with the host's uuid", outcome.Delivery)
	}
	if e := w.journal.only(t); e.Path != msgdelivery.PathHost || e.Session != string(rec.ID) || e.Trigger != msgdelivery.TriggerSend {
		t.Fatalf("journal = %+v, want a host delivery of a send to %s", e, rec.ID)
	}
}

func TestChatSession_QueueDrainReachesTheHost(t *testing.T) {
	w := newChatWiring(t)
	rec := w.session(t, "p-chat", domain.TransportChat)
	host := startFakeHost(t, w.dataDir, rec.ID)
	now := time.Now()
	q := msgqueue.New(w.store, w.rt, nil, nil,
		msgqueue.WithClock(func() time.Time { return now }),
		msgqueue.WithSettle(time.Second, time.Second))
	ctx := context.Background()
	if _, _, err := q.Enqueue(ctx, rec.ID, "held for later"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := q.Drain(ctx); err != nil {
			t.Fatal(err)
		}
		now = now.Add(2 * time.Second)
	}
	reqs := host.requests()
	if len(reqs) != 1 || !strings.Contains(reqs[0].Text, "held for later") {
		t.Fatalf("host got %+v, want the held message", reqs)
	}
	if w.inner.sentCount() != 0 {
		t.Fatal("the drain typed into a chat session's pane")
	}
	if e := w.journal.only(t); e.Path != msgdelivery.PathHost || e.Trigger != msgdelivery.TriggerQueueDrain {
		t.Fatalf("journal = %+v, want a host delivery by the queue drain", e)
	}
}

type fixedReviewer struct{ ports.Reviewer }

func (fixedReviewer) ReviewMessage(context.Context, ports.ReviewInvocation) (string, error) {
	return "review the new commit", nil
}

type fixedReviewers struct{}

func (fixedReviewers) Reviewer(domain.ReviewerHarness) (ports.Reviewer, bool) {
	return fixedReviewer{}, true
}

func TestChatSession_ReviewerPaneStaysOnThePane(t *testing.T) {
	w := newChatWiring(t)
	rec := w.session(t, "p-chat", domain.TransportChat)
	host := startFakeHost(t, w.dataDir, rec.ID)
	w.gate = nil
	w.rt = newGatedRuntime(w.inner, nil, &hostRouter{sessions: w.store, dataDir: w.dataDir, journal: w.journal})

	launcher := reviewcore.NewLauncher(fixedReviewers{}, w.rt, "")
	if err := launcher.Notify(context.Background(), "review-"+string(rec.ID), reviewcore.LaunchSpec{WorkerID: rec.ID, Harness: "claude-code"}); err != nil {
		t.Fatal(err)
	}
	if w.inner.sentCount() != 1 || len(host.requests()) != 0 {
		t.Fatalf("pane got %d, host got %d; want the reviewer brief in the reviewer pane only", w.inner.sentCount(), len(host.requests()))
	}
}

func TestTerminalSession_SendStillGoesToThePane(t *testing.T) {
	w := newChatWiring(t)
	w.gate = nil
	w.rt = newGatedRuntime(w.inner, nil, &hostRouter{sessions: w.store, dataDir: w.dataDir, journal: w.journal})
	rec := w.session(t, "p-term", domain.TransportTerminal)
	host := startFakeHost(t, w.dataDir, rec.ID)

	if _, err := newSessionMessenger(w.store, w.rt, nil, nil).Send(context.Background(), rec.ID, "hi"); err != nil {
		t.Fatal(err)
	}
	if w.inner.sentCount() != 1 || len(host.requests()) != 0 {
		t.Fatalf("pane got %d, host got %d; want a terminal session's message in its pane", w.inner.sentCount(), len(host.requests()))
	}
}

func TestChatSession_UnreachableHostFailsWithoutTyping(t *testing.T) {
	w := newChatWiring(t)
	rec := w.session(t, "p-chat", domain.TransportChat)

	_, err := newSessionMessenger(w.store, w.rt, nil, nil).Send(context.Background(), rec.ID, "hi")
	if !errors.Is(err, sessionhost.ErrNotReachable) || !strings.Contains(err.Error(), "session host") {
		t.Fatalf("err = %v, want the host's dial failure as ErrNotReachable", err)
	}
	if w.inner.sentCount() != 0 {
		t.Fatal("an unreachable host fell back to typing into the pane")
	}
	if e := w.journal.only(t); e.Path != msgdelivery.PathNone || e.Error == "" {
		t.Fatalf("journal = %+v, want a failed delivery with its error", e)
	}
}

func TestChatSession_PaneOnlyIsRefused(t *testing.T) {
	w := newChatWiring(t)
	rec := w.session(t, "p-chat", domain.TransportChat)
	host := startFakeHost(t, w.dataDir, rec.ID)

	ctx := msgdelivery.WithWire(context.Background(), msgdelivery.WirePane)
	_, err := newSessionMessenger(w.store, w.rt, nil, nil).Send(ctx, rec.ID, "hi")
	if !errors.Is(err, msgdelivery.ErrNotDelivered) || !strings.Contains(err.Error(), "--pane-only") {
		t.Fatalf("err = %v, want ErrNotDelivered naming --pane-only", err)
	}
	if w.inner.sentCount() != 0 || len(host.requests()) != 0 {
		t.Fatal("a refused send was delivered somewhere")
	}
}
