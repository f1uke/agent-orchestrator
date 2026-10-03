// Package simowner is the machine-wide record of who owns which iOS Simulator.
//
// A simulator is one resource per machine, but AO's lease lives in a daemon's
// own database, and a machine runs more than one daemon: the human's, and any
// sandbox daemon a worker starts from its branch with its own AO_DATA_DIR to
// verify a change. Each saw only its own leases, so a device driven through a
// sandbox daemon read as unclaimed everywhere else - and on 2026-10-03 a session
// on the main daemon claimed one mid-run, reset its keychain and shut it down
// twice, killing the Maestro runs another worker had on it.
//
// This package is the one piece of state every daemon on the machine shares. It
// is a SQLite file at a fixed path under ~/.ao that does NOT follow AO_DATA_DIR
// (everything else still does): a single conditional upsert per claim, so two
// daemons claiming one device at the same instant resolve to exactly one winner
// with no lock held anywhere - the same schema-is-the-exclusion rule the
// per-daemon sim_lease table is built on, now across processes.
//
// It is a second tier, not a replacement. A daemon's own sim_lease table still
// arbitrates between its own sessions and still carries everything only that
// daemon can act on - the gesture hold, the trigger that releases a lease when
// its session ends, the XCTest runner and the recorder. This file answers one
// question only: which DAEMON may hand out leases on a device right now.
//
// A dead holder can never keep a device: a row counts only while the daemon
// that wrote it is alive (by pid) and its lease has not lapsed (the TTL is
// mirrored from the daemon's own lease, so it also bounds a reused pid). There
// is no sweeper - both are evaluated on read, and a claim that finds a dead
// holder removes that row and takes the device in the same call.
//
// Times are stored as unix milliseconds rather than as text: two AO builds share
// this file, and an integer compares the same in every one of them.
package simowner

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"

	// The pure-Go SQLite driver, registered as "sqlite" - the same one the
	// daemon's own store uses.
	_ "modernc.org/sqlite"
)

// FileName is the registry's name under the AO home directory.
const FileName = "sim-ownership.db"

// EnvPath overrides where the registry lives. It exists for tests and test
// harnesses that must not touch the real machine's record; a sandbox daemon
// that sets it opts out of the very sharing this package exists for.
const EnvPath = "AO_SIM_OWNERSHIP_DB"

// schemaVersion is PRAGMA user_version. Daemons of different AO builds open
// the same file, so the schema may only ever grow additively.
const schemaVersion = 1

const schema = `
CREATE TABLE IF NOT EXISTS sim_owner (
    udid        TEXT PRIMARY KEY,
    owner_dir   TEXT NOT NULL,
    owner_pid   INTEGER NOT NULL,
    owner_port  INTEGER NOT NULL DEFAULT 0,
    session_id  TEXT NOT NULL,
    acquired_at INTEGER NOT NULL,
    expires_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sim_boot (
    udid        TEXT PRIMARY KEY,
    owner_dir   TEXT NOT NULL,
    owner_pid   INTEGER NOT NULL,
    owner_port  INTEGER NOT NULL DEFAULT 0,
    phase       TEXT NOT NULL,
    started_at  INTEGER NOT NULL,
    expires_at  INTEGER NOT NULL
);
`

// DefaultPath is where the registry lives: ~/.ao/sim-ownership.db, or
// AO_SIM_OWNERSHIP_DB when set. It deliberately ignores AO_DATA_DIR - a sandbox
// daemon has its own data dir precisely so nothing else is shared, and this is
// the one thing that must be.
//
// ⚠ "~" is the ACCOUNT's home, not $HOME. A sandbox harness isolates a daemon
// by pointing HOME somewhere else too (frontend/e2e-device does), and a
// registry that followed $HOME would hand that daemon a private copy - the
// exact blindness this package exists to end - while it drives the very
// simulator the human's daemon is arbitrating. The account's home comes from
// the user database, which HOME does not change; $HOME is only the fallback
// where that cannot be read.
func DefaultPath() (string, error) {
	if p, ok := os.LookupEnv(EnvPath); ok && p != "" {
		return p, nil
	}
	home, err := accountHome()
	if err != nil {
		return "", fmt.Errorf("resolve the AO home directory: %w", err)
	}
	return filepath.Join(home, ".ao", FileName), nil
}

func accountHome() (string, error) {
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		return u.HomeDir, nil
	}
	return os.UserHomeDir()
}

// Self is the daemon a Registry speaks for.
type Self struct {
	// DataDir is the daemon's identity. One daemon runs per data dir, and a
	// daemon that restarts keeps it, so a restart reclaims its own rows rather
	// than racing everybody else for them.
	DataDir string
	PID     int
	Port    int
}

// livenessTTL is how long a pid's liveness answer is reused. The probe forks
// `ps` (a zombie answers kill(0) like a live process), and the Device tab reads
// the listing every second; a holder that died is still noticed within this.
const livenessTTL = 2 * time.Second

// Registry is one daemon's handle on the machine-wide record.
type Registry struct {
	db    *sql.DB
	self  Self
	probe func(pid int) bool
	clock func() time.Time

	mu     sync.Mutex
	probed map[int]probed
}

type probed struct {
	alive bool
	at    time.Time
}

// Option customizes a Registry.
type Option func(*Registry)

// WithLiveness overrides the pid liveness probe, for tests. Its answers are
// not cached, so a test can kill a holder and see the effect at once.
func WithLiveness(alive func(pid int) bool) Option {
	return func(r *Registry) { r.probe, r.clock = alive, nil }
}

// Open opens (creating if needed) the registry at path on behalf of self.
func Open(path string, self Self, opts ...Option) (*Registry, error) {
	if self.DataDir == "" {
		return nil, errors.New("simowner: a daemon needs a data dir to be told apart from the others")
	}
	if abs, err := filepath.Abs(self.DataDir); err == nil {
		self.DataDir = filepath.Clean(abs)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("simowner: create %s: %w", filepath.Dir(path), err)
	}
	// busy_timeout is what makes the file shareable: another daemon's write in
	// progress is waited out rather than reported as an error.
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("simowner: open %s: %w", path, err)
	}
	// One connection: every statement here is a single short one, and a
	// second connection in the same process would only contend with the first.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("simowner: create schema in %s: %w", path, err)
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("simowner: stamp schema version in %s: %w", path, err)
	}
	r := &Registry{db: db, self: self, probe: processalive.Alive, clock: time.Now, probed: map[int]probed{}}
	for _, opt := range opts {
		opt(r)
	}
	// A boot this daemon's previous process had in flight did not survive it:
	// whatever became of the device, simctl now says so.
	if _, err := db.Exec(`DELETE FROM sim_boot WHERE owner_dir = ?`, self.DataDir); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("simowner: clear stale boots in %s: %w", path, err)
	}
	return r, nil
}

// Close closes the registry.
func (r *Registry) Close() error { return r.db.Close() }

// Self is the daemon this registry speaks for, with its data dir canonical.
func (r *Registry) Self() Self { return r.self }

// row is one sim_owner row.
type row struct {
	udid       string
	ownerDir   string
	ownerPID   int
	ownerPort  int
	sessionID  string
	acquiredAt int64
	expiresAt  int64
	updatedAt  int64
}

func (w row) lease() domain.SimLease {
	return domain.SimLease{
		UDID:       w.udid,
		SessionID:  domain.SessionID(w.sessionID),
		AcquiredAt: fromMillis(w.acquiredAt),
		ExpiresAt:  fromMillis(w.expiresAt),
	}
}

func (w row) daemon() *domain.SimDaemon {
	return &domain.SimDaemon{DataDir: w.ownerDir, PID: w.ownerPID, Port: w.ownerPort}
}

// Claim records that this daemon's session holds a device, or says who does.
//
// granted=true when the device was free machine-wide, already this daemon's,
// held by a lease that has lapsed, or held by a daemon that is no longer
// running. granted=false returns the holder, with OtherDaemon set.
//
// The exclusion is the one statement: udid is the primary key and the update
// only fires on a row this daemon owns or one that has expired, so two daemons
// claiming at once cannot both win. A holder whose process has died is the one
// case SQL cannot see, so that row is removed - only if it is still exactly the
// row that was read - and the claim is tried once more.
func (r *Registry) Claim(ctx context.Context, lease domain.SimLease, now time.Time) (domain.SimLease, bool, error) {
	for attempt := 0; attempt < 2; attempt++ {
		res, err := r.db.ExecContext(ctx, `
INSERT INTO sim_owner (udid, owner_dir, owner_pid, owner_port, session_id, acquired_at, expires_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (udid) DO UPDATE SET
    owner_dir = excluded.owner_dir,
    owner_pid = excluded.owner_pid,
    owner_port = excluded.owner_port,
    session_id = excluded.session_id,
    acquired_at = excluded.acquired_at,
    expires_at = excluded.expires_at,
    updated_at = excluded.updated_at
WHERE sim_owner.owner_dir = excluded.owner_dir
   OR sim_owner.expires_at <= excluded.updated_at`,
			lease.UDID, r.self.DataDir, r.self.PID, r.self.Port, string(lease.SessionID),
			millis(lease.AcquiredAt), millis(lease.ExpiresAt), millis(now))
		if err != nil {
			return domain.SimLease{}, false, fmt.Errorf("simowner: claim %s: %w", lease.UDID, err)
		}
		if n, err := res.RowsAffected(); err != nil {
			return domain.SimLease{}, false, fmt.Errorf("simowner: claim %s: %w", lease.UDID, err)
		} else if n > 0 {
			return lease, true, nil
		}
		holder, ok, err := r.get(ctx, lease.UDID)
		if err != nil {
			return domain.SimLease{}, false, err
		}
		if !ok {
			continue // released between the two statements; claim again
		}
		if r.alive(holder.ownerPID) {
			out := holder.lease()
			out.OtherDaemon = holder.daemon()
			return out, false, nil
		}
		if _, err := r.db.ExecContext(ctx, `
DELETE FROM sim_owner
WHERE udid = ? AND owner_dir = ? AND owner_pid = ? AND updated_at = ?`,
			holder.udid, holder.ownerDir, holder.ownerPID, holder.updatedAt); err != nil {
			return domain.SimLease{}, false, fmt.Errorf("simowner: clear dead holder of %s: %w", lease.UDID, err)
		}
	}
	// Two rounds lost to a holder that kept dying and being replaced is a
	// machine in churn, not a free device: report whoever is there now.
	holder, ok, err := r.get(ctx, lease.UDID)
	if err != nil || !ok {
		return domain.SimLease{}, false, fmt.Errorf("simowner: claim %s: the device changed hands twice while being claimed", lease.UDID)
	}
	out := holder.lease()
	out.OtherDaemon = holder.daemon()
	return out, false, nil
}

// Release drops this daemon's record of a session's lease. Another daemon's
// row, or another session's, is never touched.
func (r *Registry) Release(ctx context.Context, udid string, sessionID domain.SessionID) error {
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM sim_owner WHERE udid = ? AND owner_dir = ? AND session_id = ?`,
		udid, r.self.DataDir, string(sessionID)); err != nil {
		return fmt.Errorf("simowner: release %s: %w", udid, err)
	}
	return nil
}

// Foreign returns the live lease another, running daemon holds on a device.
// This daemon's own row is not foreign: its own sim_lease table is the
// authority on which of its sessions holds the device.
func (r *Registry) Foreign(ctx context.Context, udid string, now time.Time) (domain.SimLease, bool, error) {
	w, ok, err := r.get(ctx, udid)
	if err != nil || !ok {
		return domain.SimLease{}, false, err
	}
	if !r.countsAsForeign(w, now) {
		return domain.SimLease{}, false, nil
	}
	out := w.lease()
	out.OtherDaemon = w.daemon()
	return out, true, nil
}

// Others lists every live lease held through another running daemon, by udid.
func (r *Registry) Others(ctx context.Context, now time.Time) ([]domain.SimLease, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT udid, owner_dir, owner_pid, owner_port, session_id, acquired_at, expires_at, updated_at
FROM sim_owner WHERE owner_dir <> ? AND expires_at > ? ORDER BY udid`, r.self.DataDir, millis(now))
	if err != nil {
		return nil, fmt.Errorf("simowner: list leases: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []domain.SimLease
	for rows.Next() {
		var w row
		if err := rows.Scan(&w.udid, &w.ownerDir, &w.ownerPID, &w.ownerPort, &w.sessionID,
			&w.acquiredAt, &w.expiresAt, &w.updatedAt); err != nil {
			return nil, fmt.Errorf("simowner: list leases: %w", err)
		}
		if !r.alive(w.ownerPID) {
			continue
		}
		lease := w.lease()
		lease.OtherDaemon = w.daemon()
		out = append(out, lease)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("simowner: list leases: %w", err)
	}
	return out, nil
}

// Sync brings this daemon's rows in line with its own live leases, and returns
// the leases it holds locally that another daemon owns machine-wide - which
// the caller must give up.
//
// It is how the registry follows what never passes through a claim or a
// release: a session that ends (sim_lease's trigger deletes its lease in the
// daemon's own database, which this file cannot see), and a daemon that
// restarted, whose rows still carry its previous pid and read as dead to
// everybody else until they are reclaimed.
func (r *Registry) Sync(ctx context.Context, local []domain.SimLease, now time.Time) ([]domain.SimLease, error) {
	mine, err := r.own(ctx)
	if err != nil {
		return nil, err
	}
	wanted := make(map[string]domain.SimLease, len(local))
	for _, lease := range local {
		wanted[lease.UDID] = lease
	}
	for udid, w := range mine {
		if lease, ok := wanted[udid]; ok && lease.SessionID == domain.SessionID(w.sessionID) {
			continue
		}
		if _, err := r.db.ExecContext(ctx,
			`DELETE FROM sim_owner WHERE udid = ? AND owner_dir = ? AND updated_at = ?`,
			udid, r.self.DataDir, w.updatedAt); err != nil {
			return nil, fmt.Errorf("simowner: drop ended lease on %s: %w", udid, err)
		}
	}
	var lost []domain.SimLease
	for _, lease := range local {
		if w, ok := mine[lease.UDID]; ok && w.ownerPID == r.self.PID && w.ownerPort == r.self.Port &&
			w.sessionID == string(lease.SessionID) && w.expiresAt == millis(lease.ExpiresAt) {
			continue
		}
		_, granted, err := r.Claim(ctx, lease, now)
		if err != nil {
			return nil, err
		}
		if !granted {
			lost = append(lost, lease)
		}
	}
	return lost, nil
}

// NoteBoot records a boot this daemon has in flight, until deadline at most.
func (r *Registry) NoteBoot(ctx context.Context, udid, phase string, startedAt, deadline time.Time) error {
	if _, err := r.db.ExecContext(ctx, `
INSERT INTO sim_boot (udid, owner_dir, owner_pid, owner_port, phase, started_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (udid) DO UPDATE SET
    owner_dir = excluded.owner_dir,
    owner_pid = excluded.owner_pid,
    owner_port = excluded.owner_port,
    phase = excluded.phase,
    started_at = excluded.started_at,
    expires_at = excluded.expires_at`,
		udid, r.self.DataDir, r.self.PID, r.self.Port, phase, millis(startedAt), millis(deadline)); err != nil {
		return fmt.Errorf("simowner: note boot of %s: %w", udid, err)
	}
	return nil
}

// ClearBoot drops this daemon's record of a boot once it has settled.
func (r *Registry) ClearBoot(ctx context.Context, udid string) error {
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM sim_boot WHERE udid = ? AND owner_dir = ?`, udid, r.self.DataDir); err != nil {
		return fmt.Errorf("simowner: clear boot of %s: %w", udid, err)
	}
	return nil
}

// OtherBoots lists the boots other running daemons have in flight.
func (r *Registry) OtherBoots(ctx context.Context, now time.Time) ([]domain.SimBoot, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT udid, owner_dir, owner_pid, owner_port, phase, started_at
FROM sim_boot WHERE owner_dir <> ? AND expires_at > ? ORDER BY udid`, r.self.DataDir, millis(now))
	if err != nil {
		return nil, fmt.Errorf("simowner: list boots: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []domain.SimBoot
	for rows.Next() {
		var (
			b       domain.SimBoot
			started int64
		)
		if err := rows.Scan(&b.UDID, &b.Daemon.DataDir, &b.Daemon.PID, &b.Daemon.Port, &b.Phase, &started); err != nil {
			return nil, fmt.Errorf("simowner: list boots: %w", err)
		}
		if !r.alive(b.Daemon.PID) {
			continue
		}
		b.StartedAt = fromMillis(started)
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("simowner: list boots: %w", err)
	}
	return out, nil
}

// alive reports whether a holder's daemon is still running. This daemon is,
// by definition.
func (r *Registry) alive(pid int) bool {
	if pid == r.self.PID {
		return true
	}
	if r.clock == nil {
		return r.probe(pid)
	}
	now := r.clock()
	r.mu.Lock()
	if p, ok := r.probed[pid]; ok && now.Sub(p.at) < livenessTTL {
		r.mu.Unlock()
		return p.alive
	}
	r.mu.Unlock()
	alive := r.probe(pid)
	r.mu.Lock()
	r.probed[pid] = probed{alive: alive, at: now}
	r.mu.Unlock()
	return alive
}

func (r *Registry) countsAsForeign(w row, now time.Time) bool {
	return w.ownerDir != r.self.DataDir && w.expiresAt > millis(now) && r.alive(w.ownerPID)
}

func (r *Registry) get(ctx context.Context, udid string) (row, bool, error) {
	var w row
	err := r.db.QueryRowContext(ctx, `
SELECT udid, owner_dir, owner_pid, owner_port, session_id, acquired_at, expires_at, updated_at
FROM sim_owner WHERE udid = ?`, udid).Scan(&w.udid, &w.ownerDir, &w.ownerPID, &w.ownerPort, &w.sessionID,
		&w.acquiredAt, &w.expiresAt, &w.updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return row{}, false, nil
	}
	if err != nil {
		return row{}, false, fmt.Errorf("simowner: read %s: %w", udid, err)
	}
	return w, true, nil
}

func (r *Registry) own(ctx context.Context) (map[string]row, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT udid, owner_dir, owner_pid, owner_port, session_id, acquired_at, expires_at, updated_at
FROM sim_owner WHERE owner_dir = ?`, r.self.DataDir)
	if err != nil {
		return nil, fmt.Errorf("simowner: list own leases: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]row{}
	for rows.Next() {
		var w row
		if err := rows.Scan(&w.udid, &w.ownerDir, &w.ownerPID, &w.ownerPort, &w.sessionID,
			&w.acquiredAt, &w.expiresAt, &w.updatedAt); err != nil {
			return nil, fmt.Errorf("simowner: list own leases: %w", err)
		}
		out[w.udid] = w
	}
	return out, rows.Err()
}

func millis(t time.Time) int64 { return t.UnixMilli() }

func fromMillis(ms int64) time.Time { return time.UnixMilli(ms).UTC() }
