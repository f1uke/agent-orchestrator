package simpower

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/simslim"
)

// fakeLedger is the machine-wide boot record, from one daemon's side.
type fakeLedger struct {
	mu      sync.Mutex
	phases  []string
	open    map[string]bool
	cleared int
	others  []domain.SimBoot
}

func (l *fakeLedger) NoteBoot(_ context.Context, udid, phase string, _, _ time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.open == nil {
		l.open = map[string]bool{}
	}
	l.open[udid] = true
	l.phases = append(l.phases, phase)
	return nil
}

func (l *fakeLedger) ClearBoot(_ context.Context, udid string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.open, udid)
	l.cleared++
	return nil
}

func (l *fakeLedger) OtherBoots(context.Context, time.Time) ([]domain.SimBoot, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]domain.SimBoot(nil), l.others...), nil
}

func (l *fakeLedger) snapshot() ([]string, int, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.phases...), len(l.open), l.cleared
}

func TestBoot_IsRecordedMachineWideUntilItSettles(t *testing.T) {
	release := make(chan struct{})
	rec := &recorder{reply: func(_ context.Context, args []string) ([]byte, error) {
		if len(args) > 0 && args[0] == "verify" {
			<-release
		}
		return nil, nil
	}}
	p := newTestPower(t, rec)
	ledger := &fakeLedger{}
	p.SetBootLedger(ledger)
	req := &simslim.Request{Profile: &simslim.Profile{Keep: []string{"com.apple.apsd"}}}

	if err := p.Start(context.Background(), testUDID, Boot, &Setup{Profile: req}, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, func() bool {
		phases, open, _ := ledger.snapshot()
		return open == 1 && len(phases) == 2
	}, "the slimming phase - the window where a rebooting device is not Booted - never reached the ledger")
	if phases, _, _ := ledger.snapshot(); phases[0] != PhaseBooting || phases[1] != PhaseSlimming {
		t.Fatalf("phases = %v", phases)
	}

	close(release)
	p.wait()
	if _, open, cleared := ledger.snapshot(); open != 0 || cleared != 1 {
		t.Fatalf("after settling: open=%d cleared=%d, want the boot gone from the ledger", open, cleared)
	}
}

func TestShutdown_IsNotABoot(t *testing.T) {
	p := newTestPower(t, &recorder{})
	ledger := &fakeLedger{}
	p.SetBootLedger(ledger)
	if err := p.Start(context.Background(), testUDID, Shutdown, nil, nil); err != nil {
		t.Fatal(err)
	}
	p.wait()
	if phases, _, cleared := ledger.snapshot(); len(phases) != 0 || cleared != 0 {
		t.Fatalf("a shutdown touched the boot ledger: phases=%v cleared=%d", phases, cleared)
	}
}

func TestAll_CountsAnotherDaemonsBootButPrefersItsOwn(t *testing.T) {
	const other = "AAAAAAAA-0000-0000-0000-000000000000"
	block := make(chan struct{})
	rec := &recorder{reply: func(context.Context, []string) ([]byte, error) { <-block; return nil, nil }}
	p := newTestPower(t, rec)
	started := time.Date(2026, 10, 3, 20, 0, 0, 0, time.UTC)
	daemon := domain.SimDaemon{DataDir: "/tmp/sandbox", PID: 200, Port: 3399}
	p.SetBootLedger(&fakeLedger{others: []domain.SimBoot{
		{UDID: other, Phase: PhaseSlimming, StartedAt: started, Daemon: daemon},
		{UDID: testUDID, Phase: PhaseBooting, StartedAt: started, Daemon: daemon},
	}})
	if err := p.Start(context.Background(), testUDID, Boot, nil, nil); err != nil {
		t.Fatal(err)
	}
	defer close(block)

	all := p.All()
	got, ok := all[other]
	if !ok || got.Op != Boot || got.State != Running || got.Phase != PhaseSlimming || got.OtherDaemon == nil {
		t.Fatalf("another daemon's boot = %+v ok=%v, want a running boot marked as theirs", got, ok)
	}
	if mine := all[testUDID]; mine.OtherDaemon != nil {
		t.Fatalf("this daemon's own boot was replaced by another's: %+v", mine)
	}
}
