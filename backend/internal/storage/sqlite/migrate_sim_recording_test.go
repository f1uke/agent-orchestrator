package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// 0040 rebuilds both sim_recording tables in order to put a foreign key on
// session_id, and a rebuild is exactly where recorded evidence goes missing:
// the steps hang off the parent by a cascading foreign key, so dropping the old
// parent with foreign_keys ON would take them with it. This runs the real
// upgrade - a database already carrying a recording and its steps at 0039,
// opened with the same pragmas the daemon uses - and asserts the rows are still
// there afterwards, plus the trigger and the key the migration exists to add.
func TestMigration0040KeepsRecordingsAndAddsTheSessionEndTrigger(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "ao.db")+pragmas)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	// Stop at 0039: sim_recording exists with no key on session_id and no
	// trigger closing it when the session ends.
	upTo(t, db, 39)

	const udid = "087DF306-1FC9-4E5A-B9ED-AD36D6A1A0F1"
	for _, stmt := range []string{
		`INSERT INTO projects (id, path, registered_at) VALUES ('mer', '/tmp/mer', '2026-08-17')`,
		`INSERT INTO sessions (id, project_id, num, kind, activity_state, activity_last_at, created_at, updated_at)
		 VALUES ('mer-1', 'mer', 1, 'worker', 'idle', '2026-08-17', '2026-08-17', '2026-08-17')`,
		`INSERT INTO sim_recording (udid, session_id, name, started_at, updated_at)
		 VALUES ('` + udid + `', 'mer-1', 'checkout', '2026-08-17', '2026-08-17')`,
		`INSERT INTO sim_recording_step (udid, seq, at, kind, selector)
		 VALUES ('` + udid + `', 1, '2026-08-17', 'tap', 'Continue')`,
		`INSERT INTO sim_recording_step (udid, seq, at, kind, selector)
		 VALUES ('` + udid + `', 2, '2026-08-17', 'swipe', '')`,
		// An orphan whose session row no longer exists: it cannot satisfy the
		// new key, and the migration must drop it rather than fail.
		`INSERT INTO sim_recording (udid, session_id, name, started_at, updated_at)
		 VALUES ('ORPHAN-UDID', 'gone-9', 'stale', '2026-08-17', '2026-08-17')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("seed at 0039 (%s): %v", stmt, err)
		}
	}

	upTo(t, db, 40)

	var steps int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sim_recording_step WHERE udid = ?`, udid).Scan(&steps); err != nil {
		t.Fatalf("count steps: %v", err)
	}
	if steps != 2 {
		t.Fatalf("steps after the rebuild = %d, want the 2 that were recorded - a recording is evidence and must survive it", steps)
	}
	var name string
	if err := db.QueryRow(`SELECT name FROM sim_recording WHERE udid = ?`, udid).Scan(&name); err != nil {
		t.Fatalf("read recording: %v", err)
	}
	if name != "checkout" {
		t.Fatalf("recording name = %q, want checkout", name)
	}
	var orphans int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sim_recording WHERE udid = 'ORPHAN-UDID'`).Scan(&orphans); err != nil {
		t.Fatalf("count orphans: %v", err)
	}
	if orphans != 0 {
		t.Fatalf("a recording whose session row is gone cannot satisfy the new key, got %d left", orphans)
	}

	// The key itself: a recording naming a session that does not exist must now
	// be refused by the database rather than merely frowned upon.
	if _, err := db.Exec(`INSERT INTO sim_recording (udid, session_id, name, started_at, updated_at)
		VALUES ('OTHER-UDID', 'nobody', '', '2026-08-17', '2026-08-17')`); err == nil {
		t.Fatal("session_id must be a foreign key onto sessions after 0040")
	}

	// And the trigger: ending the session closes the recording and keeps it.
	if _, err := db.Exec(`UPDATE sessions SET is_terminated = 1, updated_at = '2026-08-18' WHERE id = 'mer-1'`); err != nil {
		t.Fatalf("terminate session: %v", err)
	}
	var stoppedAt sql.NullString
	if err := db.QueryRow(`SELECT stopped_at FROM sim_recording WHERE udid = ?`, udid).Scan(&stoppedAt); err != nil {
		t.Fatalf("read recording after terminate: %v", err)
	}
	if !stoppedAt.Valid {
		t.Fatal("ending a session must close its open recording, or the device is blocked for the next session for ever")
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM sim_recording_step WHERE udid = ?`, udid).Scan(&steps); err != nil {
		t.Fatalf("count steps after terminate: %v", err)
	}
	if steps != 2 {
		t.Fatalf("steps after the session ended = %d, want 2 kept - the flow is emitted from them afterwards", steps)
	}
}
