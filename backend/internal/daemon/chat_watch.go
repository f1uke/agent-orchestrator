package daemon

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/activity"
	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/looptelemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionhost"
)

const (
	chatWatchInterval = 5 * time.Second
	chatRetryFirst    = time.Second
	chatRetryCeiling  = 10 * time.Second
)

type pendingSet map[string]struct{}

func pendingFromState(st sessionhost.State) pendingSet {
	p := make(pendingSet, len(st.Pending))
	for _, r := range st.Pending {
		p[r.RequestID] = struct{}{}
	}
	return p
}

func foldPending(p pendingSet, e chatproto.Entry) pendingSet {
	if e.Dir == chatproto.DirHost {
		note, err := chatproto.ParseHostNote(e.Payload)
		if err != nil || (note.Kind != chatproto.NoteSpawn && note.Kind != chatproto.NoteExit) || len(p) == 0 {
			return p
		}
		return pendingSet{}
	}
	ev, err := chatproto.ParseLine(e.Payload)
	if err != nil {
		return p
	}
	switch ev := ev.(type) {
	case chatproto.ControlRequest:
		if _, ok := ev.Body.(chatproto.CanUseTool); ok && e.Dir == chatproto.DirOut {
			return addPending(p, ev.RequestID)
		}
	case chatproto.ControlResponse:
		return dropPending(p, ev.RequestID)
	case chatproto.ControlCancel:
		return dropPending(p, ev.RequestID)
	}
	return p
}

func addPending(p pendingSet, id string) pendingSet {
	if _, ok := p[id]; ok {
		return p
	}
	next := maps.Clone(p)
	if next == nil {
		next = pendingSet{}
	}
	next[id] = struct{}{}
	return next
}

func dropPending(p pendingSet, id string) pendingSet {
	if _, ok := p[id]; !ok {
		return p
	}
	next := maps.Clone(p)
	delete(next, id)
	return next
}

type chatHost interface {
	State(ctx context.Context) (sessionhost.State, error)
	Subscribe(ctx context.Context, fromSeq uint64, fn func(sessionhost.Frame) error) error
}

type liveChatSessions interface {
	LiveChatSessions(ctx context.Context) (map[domain.SessionID]domain.ActivityState, error)
}

type chatWatch struct {
	sessions liveChatSessions
	host     func(domain.SessionID) chatHost
	rec      activity.Recorder
	feed     activity.Publisher
	log      *slog.Logger
	after    func(time.Duration) <-chan time.Time

	watchers map[domain.SessionID]runningWatcher
}

type runningWatcher struct {
	cancel context.CancelFunc
	done   <-chan struct{}
}

func startChatWatch(ctx context.Context, sessions liveChatSessions, dataDir string, rec activity.Recorder, feed activity.Publisher, reg *looptelemetry.Registry, log *slog.Logger) <-chan struct{} {
	loop := reg.Register(looptelemetry.Spec{
		Name:        "chat-session-watch",
		Display:     "Chat session requests",
		Description: "Follows each chat session's host and reports it as needing input while a permission, question or plan waits for an answer.",
		Interval:    chatWatchInterval,
	})
	w := &chatWatch{
		sessions: sessions,
		host: func(id domain.SessionID) chatHost {
			_, socket := sessionhost.Paths(dataDir, string(id))
			return sessionhost.NewClient(socket)
		},
		rec: rec, feed: feed, log: log, after: time.After,
	}
	t := time.NewTicker(chatWatchInterval)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer t.Stop()
		w.run(ctx, t.C, loop.Tick)
	}()
	return done
}

func (w *chatWatch) run(ctx context.Context, tick <-chan time.Time, onTick func()) {
	w.watchers = map[domain.SessionID]runningWatcher{}
	defer func() {
		for id, r := range w.watchers {
			r.cancel()
			<-r.done
			delete(w.watchers, id)
		}
	}()
	for {
		onTick()
		if err := w.reconcile(ctx); err != nil && ctx.Err() == nil {
			w.log.Warn("chat session watch failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick:
		}
	}
}

func (w *chatWatch) reconcile(ctx context.Context) error {
	live, err := w.sessions.LiveChatSessions(ctx)
	if err != nil {
		return err
	}
	for id, r := range w.watchers {
		if _, ok := live[id]; !ok {
			r.cancel()
			<-r.done
			delete(w.watchers, id)
		}
	}
	for id, state := range live {
		if _, ok := w.watchers[id]; ok {
			continue
		}
		wctx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		cw := &chatWatcher{
			id: id, host: w.host(id), rec: w.rec, feed: w.feed, log: w.log, after: w.after,
			reportedWaiting: state == domain.ActivityWaitingInput,
		}
		go func() {
			defer close(done)
			cw.run(wctx)
		}()
		w.watchers[id] = runningWatcher{cancel: cancel, done: done}
	}
	return nil
}

type chatWatcher struct {
	id    domain.SessionID
	host  chatHost
	rec   activity.Recorder
	feed  activity.Publisher
	log   *slog.Logger
	after func(time.Duration) <-chan time.Time

	reportedWaiting bool
	pending         pendingSet
}

func (c *chatWatcher) run(ctx context.Context) {
	retry := chatRetryFirst
	for {
		connected, err := c.follow(ctx)
		if ctx.Err() != nil {
			return
		}
		if connected {
			retry = chatRetryFirst
		}
		c.log.Debug("chat session host stream ended", "session", c.id, "err", err, "retry", retry)
		select {
		case <-ctx.Done():
			return
		case <-c.after(retry):
		}
		retry = min(retry*2, chatRetryCeiling)
	}
}

func (c *chatWatcher) follow(ctx context.Context) (connected bool, err error) {
	st, err := c.host.State(ctx)
	if err != nil {
		return false, err
	}
	c.pending = pendingFromState(st)
	c.reportWaitingChange(ctx)
	return true, c.host.Subscribe(ctx, st.LastSeq, func(f sessionhost.Frame) error {
		if f.Entry != nil {
			c.pending = foldPending(c.pending, *f.Entry)
			c.reportWaitingChange(ctx)
		}
		return nil
	})
}

func (c *chatWatcher) reportWaitingChange(ctx context.Context) {
	want := len(c.pending) > 0
	if want == c.reportedWaiting {
		return
	}
	state := domain.ActivityActive
	if want {
		state = domain.ActivityWaitingInput
	}
	err := activity.Signal(ctx, c.rec, c.feed, c.id, ports.ActivitySignal{Valid: true, State: state}, nil)
	switch {
	case err == nil:
		c.reportedWaiting = want
	case errors.Is(err, ports.ErrSessionNotFound) || ctx.Err() != nil:
		c.log.Debug("chat session activity not recorded", "session", c.id, "state", state, "err", err)
	default:
		c.log.Warn("chat session activity not recorded", "session", c.id, "state", state, "err", err)
	}
}
