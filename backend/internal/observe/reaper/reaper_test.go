package reaper

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var ctx = context.Background()

type fakeLCM struct {
	observed map[domain.SessionID]ports.RuntimeFacts
}

func (l *fakeLCM) ApplyRuntimeObservation(_ context.Context, id domain.SessionID, f ports.RuntimeFacts) error {
	if l.observed == nil {
		l.observed = map[domain.SessionID]ports.RuntimeFacts{}
	}
	l.observed[id] = f
	return nil
}

type fakeSessions struct{ rows []domain.SessionRecord }

func (s fakeSessions) ListAllSessions(context.Context) ([]domain.SessionRecord, error) {
	return s.rows, nil
}

type fakeRuntime struct {
	alive bool
	err   error
}

func (r fakeRuntime) IsAlive(context.Context, ports.RuntimeHandle) (bool, error) {
	return r.alive, r.err
}

// fakeAgentRuntime is a runtime that can also see the agent process.
type fakeAgentRuntime struct {
	fakeRuntime
	agent    bool
	agentErr error
	probes   *int
}

func (r fakeAgentRuntime) AgentAlive(context.Context, ports.RuntimeHandle) (bool, error) {
	*r.probes++
	return r.agent, r.agentErr
}

func probableSession(id domain.SessionID) domain.SessionRecord {
	return domain.SessionRecord{
		ID:       id,
		Activity: domain.Activity{State: domain.ActivityActive},
		Metadata: domain.SessionMetadata{RuntimeHandleID: "h1"},
	}
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newReaper(lcm *fakeLCM, sessions fakeSessions, rt runtimeProber) *Reaper {
	return New(lcm, sessions, rt, Config{Logger: quietLogger()})
}

func TestStart_OnTickFiresEachCycle(t *testing.T) {
	loopCtx, cancel := context.WithCancel(context.Background())
	var ticks atomic.Int32
	sessions := fakeSessions{rows: []domain.SessionRecord{probableSession("mer-1")}}
	r := New(&fakeLCM{}, sessions, fakeRuntime{alive: true}, Config{
		Tick:   5 * time.Millisecond,
		Logger: quietLogger(),
		OnTick: func() { ticks.Add(1) },
	})
	done := r.Start(loopCtx)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ticks.Load() >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if ticks.Load() < 2 {
		t.Fatalf("want >=2 OnTick calls, got %d", ticks.Load())
	}
}

func TestTick_ReportsAliveProbe(t *testing.T) {
	lcm := &fakeLCM{}
	sessions := fakeSessions{rows: []domain.SessionRecord{probableSession("mer-1")}}
	if err := newReaper(lcm, sessions, fakeRuntime{alive: true}).Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if lcm.observed["mer-1"].Probe != ports.ProbeAlive {
		t.Fatalf("want alive probe, got %q", lcm.observed["mer-1"].Probe)
	}
}

func TestTick_ReportsProbeErrorAsFailed(t *testing.T) {
	lcm := &fakeLCM{}
	sessions := fakeSessions{rows: []domain.SessionRecord{probableSession("mer-1")}}
	if err := newReaper(lcm, sessions, fakeRuntime{err: errors.New("tmux gone")}).Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if lcm.observed["mer-1"].Probe != ports.ProbeFailed {
		t.Fatalf("probe error must be reported as failed, got %q", lcm.observed["mer-1"].Probe)
	}
}

func TestTick_SkipsTerminatedSession(t *testing.T) {
	lcm := &fakeLCM{}
	dead := probableSession("mer-1")
	dead.IsTerminated = true
	sessions := fakeSessions{rows: []domain.SessionRecord{dead}}
	if err := newReaper(lcm, sessions, fakeRuntime{alive: true}).Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if _, probed := lcm.observed["mer-1"]; probed {
		t.Fatal("terminated sessions must not be probed")
	}
}

func TestTick_SkipsSuspendedSession(t *testing.T) {
	lcm := &fakeLCM{}
	paused := probableSession("mer-1")
	paused.IsSuspended = true
	sessions := fakeSessions{rows: []domain.SessionRecord{paused}}
	// A suspended session's tmux is intentionally gone; probing it would report a
	// dead runtime and re-terminate it. It must be skipped like a terminated one.
	if err := newReaper(lcm, sessions, fakeRuntime{alive: false}).Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if _, probed := lcm.observed["mer-1"]; probed {
		t.Fatal("suspended sessions must not be probed")
	}
}

func TestTick_SkipsSessionWithoutHandle(t *testing.T) {
	lcm := &fakeLCM{}
	noHandle := domain.SessionRecord{ID: "mer-1"} // no runtime metadata
	sessions := fakeSessions{rows: []domain.SessionRecord{noHandle}}
	if err := newReaper(lcm, sessions, fakeRuntime{alive: true}).Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if _, probed := lcm.observed["mer-1"]; probed {
		t.Fatal("a session without a runtime handle must be skipped")
	}
}

func backgroundSession(id domain.SessionID) domain.SessionRecord {
	rec := probableSession(id)
	rec.Activity.State = domain.ActivityBackground
	return rec
}

func TestTick_ReportsTheAgentOfASessionWaitingOnBackgroundWork(t *testing.T) {
	tests := []struct {
		name     string
		agent    bool
		agentErr error
		want     ports.ProbeResult
	}{
		{"agent alive", true, nil, ports.ProbeAlive},
		{"agent dead", false, nil, ports.ProbeDead},
		{"agent probe error", false, errors.New("ps failed"), ports.ProbeFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lcm := &fakeLCM{}
			probes := 0
			rt := fakeAgentRuntime{fakeRuntime: fakeRuntime{alive: true}, agent: tt.agent, agentErr: tt.agentErr, probes: &probes}
			sessions := fakeSessions{rows: []domain.SessionRecord{backgroundSession("mer-1")}}
			if err := newReaper(lcm, sessions, rt).Tick(ctx); err != nil {
				t.Fatal(err)
			}
			got := lcm.observed["mer-1"]
			if got.Probe != ports.ProbeAlive || got.Agent != tt.want {
				t.Fatalf("got runtime %q agent %q, want runtime alive agent %q", got.Probe, got.Agent, tt.want)
			}
		})
	}
}

// The agent probe costs a process lookup per session per tick, so it runs only
// where its answer changes a reading.
func TestTick_DoesNotProbeTheAgentOfOtherSessions(t *testing.T) {
	lcm := &fakeLCM{}
	probes := 0
	rt := fakeAgentRuntime{fakeRuntime: fakeRuntime{alive: true}, probes: &probes}
	sessions := fakeSessions{rows: []domain.SessionRecord{probableSession("mer-1")}}
	if err := newReaper(lcm, sessions, rt).Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if probes != 0 || lcm.observed["mer-1"].Agent != "" {
		t.Fatalf("an active session's agent must not be probed: %d probes, agent %q", probes, lcm.observed["mer-1"].Agent)
	}
}

func TestTick_DoesNotProbeTheAgentInsideADeadRuntime(t *testing.T) {
	lcm := &fakeLCM{}
	probes := 0
	rt := fakeAgentRuntime{fakeRuntime: fakeRuntime{alive: false}, probes: &probes}
	sessions := fakeSessions{rows: []domain.SessionRecord{backgroundSession("mer-1")}}
	if err := newReaper(lcm, sessions, rt).Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if probes != 0 || lcm.observed["mer-1"].Probe != ports.ProbeDead {
		t.Fatalf("got %d agent probes, runtime %q; want 0 probes and a dead runtime", probes, lcm.observed["mer-1"].Probe)
	}
}
