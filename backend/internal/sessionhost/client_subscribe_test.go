package sessionhost

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
)

type clientSub struct {
	frames chan Frame
	done   chan error
}

func subscribeClient(ctx context.Context, c Client, from uint64) *clientSub {
	s := &clientSub{frames: make(chan Frame, 4096), done: make(chan error, 1)}
	go func() {
		s.done <- c.Subscribe(ctx, from, nil, func(f Frame) error {
			s.frames <- f
			return nil
		})
	}()
	return s
}

func (s *clientSub) entries(t *testing.T, n int) []chatproto.Entry {
	t.Helper()
	var got []chatproto.Entry
	for len(got) < n {
		select {
		case f := <-s.frames:
			if f.Entry != nil {
				got = append(got, *f.Entry)
			}
		case err := <-s.done:
			t.Fatalf("Subscribe returned %v after %d of %d entries", err, len(got), n)
		case <-time.After(testTimeout):
			t.Fatalf("got %d of %d entries", len(got), n)
		}
	}
	return got
}

func (s *clientSub) end(t *testing.T) error {
	t.Helper()
	select {
	case err := <-s.done:
		return err
	case <-time.After(testTimeout):
		t.Fatal("Subscribe did not return")
		return nil
	}
}

func TestClientSubscribe(t *testing.T) {
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
	turn = append(turn, fixtureLine{chatproto.DirOut, string(first[33].Payload)})
	cfg, _ := fakeConfig(t, dir, waitingFixture(t, dir, turn...))
	r := startHost(t, cfg)
	c := NewClient(cfg.SocketPath)
	ctx := context.Background()
	eventually(t, "initialize answered", func() bool { return state(t, cfg.SocketPath).LastSeq == 3 })

	all := subscribeClient(ctx, c, 0)
	if got := all.entries(t, 3); got[0].Seq != 1 || got[2].Seq != 3 {
		t.Fatalf("replay from 0 = seqs %d..%d, want 1..3", got[0].Seq, got[2].Seq)
	}
	later := subscribeClient(ctx, c, 2)
	if got := later.entries(t, 1); got[0].Seq != 3 {
		t.Fatalf("replay from 2 starts at seq %d, want 3", got[0].Seq)
	}

	if _, err := c.Send(ctx, chatproto.UserInput{Text: "first"}, nil); err != nil {
		t.Fatal(err)
	}
	var live []chatproto.Entry
	gotDeltas := 0
	for len(live) == 0 || messageKind(live[len(live)-1].Payload) != "result/" {
		select {
		case f := <-all.frames:
			switch {
			case f.Entry != nil:
				live = append(live, *f.Entry)
			case messageKind(f.Delta) == "stream_event/":
				gotDeltas++
			default:
				t.Fatalf("frame is neither an entry nor a stream event: %+v", f)
			}
		case <-time.After(testTimeout):
			t.Fatalf("no result after %d live entries", len(live))
		}
	}
	if live[0].Seq != 4 || gotDeltas != nDeltas {
		t.Errorf("live stream started at seq %d with %d deltas, want seq 4 and %d deltas", live[0].Seq, gotDeltas, nDeltas)
	}
	if got := later.entries(t, len(live)); got[0].Seq != 4 || got[len(got)-1].Seq != live[len(live)-1].Seq {
		t.Errorf("the later subscriber's live seqs are %d..%d, want 4..%d", got[0].Seq, got[len(got)-1].Seq, live[len(live)-1].Seq)
	}

	stop := errors.New("stop")
	if err := c.Subscribe(ctx, 0, nil, func(Frame) error { return stop }); !errors.Is(err, stop) {
		t.Errorf("Subscribe with a failing fn returned %v, want fn's error", err)
	}
	cctx, cancel := context.WithCancel(ctx)
	idle := subscribeClient(cctx, c, live[len(live)-1].Seq-1)
	idle.entries(t, 1)
	cancel()
	if err := idle.end(t); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled Subscribe returned %v, want context.Canceled", err)
	}

	r.cancel()
	r.wait(t)
	if err := all.end(t); err != nil {
		t.Errorf("Subscribe returned %v when the host exited, want nil", err)
	}
	if err := later.end(t); err != nil {
		t.Errorf("the later Subscribe returned %v when the host exited, want nil", err)
	}
}

func TestClientSubscribeReportsAttachBeforeTheFirstFrame(t *testing.T) {
	dir := shortDir(t)
	cfg, _ := fakeConfig(t, dir, waitingFixture(t, dir))
	r := startHost(t, cfg)
	eventually(t, "initialize answered", func() bool { return state(t, cfg.SocketPath).LastSeq == 3 })

	attached := 0
	stop := errors.New("stop")
	err := NewClient(cfg.SocketPath).Subscribe(context.Background(), 0, func() { attached++ }, func(Frame) error {
		if attached != 1 {
			t.Errorf("the first frame came with attached called %d times, want 1", attached)
		}
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatalf("Subscribe returned %v, want fn's error", err)
	}

	r.cancel()
	r.wait(t)
	attached = 0
	if err := NewClient(cfg.SocketPath).Subscribe(context.Background(), 0, func() { attached++ }, func(Frame) error { return nil }); err == nil {
		t.Fatal("Subscribe to a host that has exited returned nil")
	}
	if attached != 0 {
		t.Errorf("attached was called %d times with no host, want 0", attached)
	}
}
