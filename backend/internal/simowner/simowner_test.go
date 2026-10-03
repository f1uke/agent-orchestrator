package simowner

import (
	"context"
	"os/user"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const udid = "11111111-2222-3333-4444-555555555555"

var t0 = time.Date(2026, 10, 3, 20, 0, 0, 0, time.UTC)

// pids is the fake process table: a test kills a daemon by flipping its pid.
type pids struct {
	mu   sync.Mutex
	dead map[int]bool
}

func (p *pids) alive(pid int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.dead[pid]
}

func (p *pids) kill(pid int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.dead == nil {
		p.dead = map[int]bool{}
	}
	p.dead[pid] = true
}

// daemon opens the shared registry file as one daemon would: its own
// connection, so nothing in-process serialises two daemons - only the file.
func daemon(t *testing.T, path string, procs *pids, dataDir string, pid, port int) *Registry {
	t.Helper()
	reg, err := Open(path, Self{DataDir: dataDir, PID: pid, Port: port}, WithLiveness(procs.alive))
	if err != nil {
		t.Fatalf("open registry for %s: %v", dataDir, err)
	}
	t.Cleanup(func() { _ = reg.Close() })
	return reg
}

func lease(session string, now time.Time, ttl time.Duration) domain.SimLease {
	return domain.SimLease{UDID: udid, SessionID: domain.SessionID(session), AcquiredAt: now, ExpiresAt: now.Add(ttl)}
}

type pair struct {
	main, sandbox *Registry
	procs         *pids
	sandboxDir    string
}

func twoDaemons(t *testing.T) pair {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	procs := &pids{}
	sandboxDir := filepath.Join(dir, "sandbox-data")
	return pair{
		main:       daemon(t, path, procs, filepath.Join(dir, "main-data"), 100, 3001),
		sandbox:    daemon(t, path, procs, sandboxDir, 200, 3399),
		procs:      procs,
		sandboxDir: sandboxDir,
	}
}

func TestClaim_AnotherDaemonsLeaseIsRefusedWithItsHolderAndDaemon(t *testing.T) {
	d := twoDaemons(t)
	ctx := context.Background()

	if _, granted, err := d.sandbox.Claim(ctx, lease("ao-360", t0, 10*time.Minute), t0); err != nil || !granted {
		t.Fatalf("sandbox claim: granted=%v err=%v", granted, err)
	}
	holder, granted, err := d.main.Claim(ctx, lease("ao-361", t0.Add(time.Minute), 10*time.Minute), t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if granted {
		t.Fatal("main daemon was granted a device the sandbox daemon's session holds")
	}
	if holder.SessionID != "ao-360" || holder.OtherDaemon == nil {
		t.Fatalf("holder = %+v, want ao-360 with its daemon", holder)
	}
	if holder.OtherDaemon.DataDir != d.sandboxDir || holder.OtherDaemon.PID != 200 || holder.OtherDaemon.Port != 3399 {
		t.Fatalf("other daemon = %+v", *holder.OtherDaemon)
	}
	if !holder.ExpiresAt.Equal(t0.Add(10 * time.Minute)) {
		t.Fatalf("expiresAt = %s", holder.ExpiresAt)
	}
}

func TestClaim_SameDaemonRenewsAndHandsToItsOtherSessions(t *testing.T) {
	// Which of a daemon's OWN sessions holds a device is that daemon's
	// sim_lease table's call, not this file's: the registry only arbitrates
	// between daemons, so a daemon's second claim always lands.
	d := twoDaemons(t)
	ctx := context.Background()
	for i, session := range []string{"ao-1", "ao-1", "ao-2"} {
		now := t0.Add(time.Duration(i) * time.Second)
		if _, granted, err := d.main.Claim(ctx, lease(session, now, time.Minute), now); err != nil || !granted {
			t.Fatalf("claim %d by %s: granted=%v err=%v", i, session, granted, err)
		}
	}
	got, ok, err := d.sandbox.Foreign(ctx, udid, t0.Add(3*time.Second))
	if err != nil || !ok || got.SessionID != "ao-2" {
		t.Fatalf("foreign from sandbox = %+v ok=%v err=%v, want ao-2", got, ok, err)
	}
}

func TestClaim_ExpiredLeaseIsTakenOver(t *testing.T) {
	d := twoDaemons(t)
	ctx := context.Background()
	if _, granted, _ := d.sandbox.Claim(ctx, lease("ao-360", t0, 30*time.Second), t0); !granted {
		t.Fatal("sandbox claim refused")
	}
	later := t0.Add(31 * time.Second)
	if _, granted, err := d.main.Claim(ctx, lease("ao-361", later, time.Minute), later); err != nil || !granted {
		t.Fatalf("claim after expiry: granted=%v err=%v", granted, err)
	}
}

func TestClaim_DeadHolderNeverKeepsTheDevice(t *testing.T) {
	// The sandbox daemon is killed holding a device with most of its TTL left.
	// The device is free the moment its process is gone, not ten minutes later.
	d := twoDaemons(t)
	ctx := context.Background()
	if _, granted, _ := d.sandbox.Claim(ctx, lease("ao-360", t0, 10*time.Minute), t0); !granted {
		t.Fatal("sandbox claim refused")
	}
	if _, granted, _ := d.main.Claim(ctx, lease("ao-361", t0, 10*time.Minute), t0); granted {
		t.Fatal("claimed while the holder was alive")
	}
	d.procs.kill(200)
	if _, ok, _ := d.main.Foreign(ctx, udid, t0); ok {
		t.Fatal("a dead daemon's lease still reads as held")
	}
	if others, _ := d.main.Others(ctx, t0); len(others) != 0 {
		t.Fatalf("others = %+v, want none once the holder died", others)
	}
	if _, granted, err := d.main.Claim(ctx, lease("ao-361", t0, 10*time.Minute), t0); err != nil || !granted {
		t.Fatalf("claim after the holder died: granted=%v err=%v", granted, err)
	}
}

func TestClaim_ConcurrentDaemonsHaveExactlyOneWinner(t *testing.T) {
	// Every racer is its own daemon with its own connection to the file, so
	// nothing in Go orders them - the conditional upsert has to.
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	procs := &pids{}
	const racers = 8
	regs := make([]*Registry, racers)
	for i := range regs {
		regs[i] = daemon(t, path, procs, filepath.Join(dir, "data", string(rune('a'+i))), 1000+i, 4000+i)
	}
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		winners int
		errs    []error
	)
	start := make(chan struct{})
	for i, reg := range regs {
		wg.Add(1)
		go func(i int, reg *Registry) {
			defer wg.Done()
			<-start
			_, granted, err := reg.Claim(context.Background(), lease("racer", t0, time.Minute), t0)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
			}
			if granted {
				winners++
			}
		}(i, reg)
	}
	close(start)
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("claim errors: %v", errs)
	}
	if winners != 1 {
		t.Fatalf("winners = %d, want exactly 1", winners)
	}
}

func TestRelease_TouchesOnlyItsOwnRow(t *testing.T) {
	d := twoDaemons(t)
	ctx := context.Background()
	if _, granted, _ := d.sandbox.Claim(ctx, lease("ao-360", t0, time.Minute), t0); !granted {
		t.Fatal("sandbox claim refused")
	}
	// Same session id, other daemon: ids are per database, so this is not the
	// holder and must not free the device.
	if err := d.main.Release(ctx, udid, "ao-360"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := d.main.Foreign(ctx, udid, t0); !ok {
		t.Fatal("another daemon released a lease that was not its own")
	}
	if err := d.sandbox.Release(ctx, udid, "ao-360"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := d.main.Foreign(ctx, udid, t0); ok {
		t.Fatal("the holder's own release did not free the device")
	}
}

func TestOthers_ListsOnlyOtherLiveDaemons(t *testing.T) {
	d := twoDaemons(t)
	ctx := context.Background()
	other := domain.SimLease{UDID: "AAAA", SessionID: "ao-1", AcquiredAt: t0, ExpiresAt: t0.Add(time.Minute)}
	if _, granted, _ := d.main.Claim(ctx, lease("ao-9", t0, time.Minute), t0); !granted {
		t.Fatal("main claim refused")
	}
	if _, granted, _ := d.sandbox.Claim(ctx, other, t0); !granted {
		t.Fatal("sandbox claim refused")
	}
	mainSees, err := d.main.Others(ctx, t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(mainSees) != 1 || mainSees[0].UDID != "AAAA" || mainSees[0].OtherDaemon == nil {
		t.Fatalf("main sees %+v, want only the sandbox's AAAA", mainSees)
	}
	if expired, _ := d.main.Others(ctx, t0.Add(2*time.Minute)); len(expired) != 0 {
		t.Fatalf("an expired lease is still listed: %+v", expired)
	}
}

func TestSync_DropsEndedLeasesAndReclaimsAfterARestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	procs := &pids{}
	mainDir := filepath.Join(dir, "main-data")
	ctx := context.Background()

	main := daemon(t, path, procs, mainDir, 100, 3001)
	sandbox := daemon(t, path, procs, filepath.Join(dir, "sandbox-data"), 200, 3399)
	held := lease("ao-1", t0, 10*time.Minute)
	if _, granted, _ := main.Claim(ctx, held, t0); !granted {
		t.Fatal("main claim refused")
	}

	// The main daemon restarts: same data dir, new pid. Until it syncs, its
	// rows carry the dead pid, so they read as free to everybody else...
	procs.kill(100)
	restarted := daemon(t, path, procs, mainDir, 101, 3001)
	if _, ok, _ := sandbox.Foreign(ctx, udid, t0); ok {
		t.Fatal("a dead pid's lease still reads as held")
	}
	// ...and syncing puts the lease it still holds back under the new pid.
	lost, err := restarted.Sync(ctx, []domain.SimLease{held}, t0)
	if err != nil || len(lost) != 0 {
		t.Fatalf("sync: lost=%+v err=%v", lost, err)
	}
	if got, ok, _ := sandbox.Foreign(ctx, udid, t0); !ok || got.OtherDaemon.PID != 101 {
		t.Fatalf("after the sync the sandbox sees %+v ok=%v, want the restarted daemon's lease", got, ok)
	}

	// The session ends: its lease is gone from the daemon's own table, so the
	// next sync drops the machine-wide row and the device is free elsewhere.
	if _, err := restarted.Sync(ctx, nil, t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, granted, _ := sandbox.Claim(ctx, lease("ao-360", t0.Add(time.Second), time.Minute), t0.Add(time.Second)); !granted {
		t.Fatal("the device was not freed when the holding session's lease ended")
	}
}

func TestSync_ReportsALeaseAnotherDaemonTookMeanwhile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	procs := &pids{}
	mainDir := filepath.Join(dir, "main-data")
	ctx := context.Background()

	main := daemon(t, path, procs, mainDir, 100, 3001)
	sandbox := daemon(t, path, procs, filepath.Join(dir, "sandbox-data"), 200, 3399)
	held := lease("ao-1", t0, 10*time.Minute)
	if _, granted, _ := main.Claim(ctx, held, t0); !granted {
		t.Fatal("main claim refused")
	}
	procs.kill(100) // main is down; the sandbox takes the device, rightly
	if _, granted, _ := sandbox.Claim(ctx, lease("ao-360", t0, time.Minute), t0); !granted {
		t.Fatal("sandbox could not take a dead daemon's device")
	}
	restarted := daemon(t, path, procs, mainDir, 101, 3001)
	lost, err := restarted.Sync(ctx, []domain.SimLease{held}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(lost) != 1 || lost[0].SessionID != "ao-1" {
		t.Fatalf("lost = %+v, want the main daemon's ao-1 lease", lost)
	}
	if got, ok, _ := restarted.Foreign(ctx, udid, t0); !ok || got.SessionID != "ao-360" {
		t.Fatalf("the sandbox's lease was overwritten: %+v ok=%v", got, ok)
	}
}

func TestBoots_CountAcrossDaemonsUntilSettledOrDead(t *testing.T) {
	d := twoDaemons(t)
	ctx := context.Background()
	if err := d.sandbox.NoteBoot(ctx, udid, "booting", t0, t0.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := d.sandbox.NoteBoot(ctx, udid, "slimming", t0, t0.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	boots, err := d.main.OtherBoots(ctx, t0.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(boots) != 1 || boots[0].Phase != "slimming" || boots[0].Daemon.PID != 200 || !boots[0].StartedAt.Equal(t0) {
		t.Fatalf("main sees boots %+v, want the sandbox's slimming boot", boots)
	}
	if own, _ := d.sandbox.OtherBoots(ctx, t0); len(own) != 0 {
		t.Fatalf("a daemon's own boot is listed as another's: %+v", own)
	}
	if err := d.sandbox.ClearBoot(ctx, udid); err != nil {
		t.Fatal(err)
	}
	if boots, _ := d.main.OtherBoots(ctx, t0); len(boots) != 0 {
		t.Fatalf("a settled boot is still counted: %+v", boots)
	}

	_ = d.sandbox.NoteBoot(ctx, udid, "booting", t0, t0.Add(10*time.Minute))
	d.procs.kill(200)
	if boots, _ := d.main.OtherBoots(ctx, t0); len(boots) != 0 {
		t.Fatalf("a dead daemon's boot is still counted: %+v", boots)
	}
}

func TestOpen_DropsItsOwnPreviousProcessesBoots(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	procs := &pids{}
	ctx := context.Background()
	first := daemon(t, path, procs, filepath.Join(dir, "main"), 100, 3001)
	if err := first.NoteBoot(ctx, udid, "booting", t0, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	_ = daemon(t, path, procs, filepath.Join(dir, "main"), 101, 3001)
	observer := daemon(t, path, procs, filepath.Join(dir, "other"), 300, 3500)
	if boots, _ := observer.OtherBoots(ctx, t0); len(boots) != 0 {
		t.Fatalf("a boot that did not survive its daemon's restart is still counted: %+v", boots)
	}
}

func TestDefaultPath_IgnoresAODataDirAndHOME(t *testing.T) {
	// A sandbox daemon gets its own data dir and, in the e2e harness, its own
	// HOME. Neither may give it a private registry.
	account, err := user.Current()
	if err != nil || account.HomeDir == "" {
		t.Skipf("no account home to compare against: %v", err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AO_DATA_DIR", filepath.Join(t.TempDir(), "sandbox"))
	t.Setenv(EnvPath, "")
	got, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(account.HomeDir, ".ao", FileName); got != want {
		t.Fatalf("DefaultPath = %s, want %s - a sandbox daemon must share the machine's record", got, want)
	}
	t.Setenv(EnvPath, "/tmp/elsewhere.db")
	if got, _ := DefaultPath(); got != "/tmp/elsewhere.db" {
		t.Fatalf("DefaultPath with %s = %s", EnvPath, got)
	}
}

func TestAlive_CachesTheProbeButNotForItself(t *testing.T) {
	calls := 0
	clock := t0
	r := &Registry{
		self:   Self{PID: 7},
		probe:  func(int) bool { calls++; return true },
		clock:  func() time.Time { return clock },
		probed: map[int]probed{},
	}
	r.alive(7)
	if calls != 0 {
		t.Fatal("probed this daemon's own pid")
	}
	r.alive(9)
	r.alive(9)
	if calls != 1 {
		t.Fatalf("probe calls = %d, want 1 inside the cache window", calls)
	}
	clock = clock.Add(livenessTTL)
	r.alive(9)
	if calls != 2 {
		t.Fatalf("probe calls = %d, want a fresh probe once the window passed", calls)
	}
}
