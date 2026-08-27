package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// 0053 turns the machine's single overwritten result into a run history, and the
// interesting database is the one that already exists: a case whose agent_*
// columns hold ONE result (every earlier one destroyed by the next
// `ao smoke record`) with a pile of screenshots underneath it that may or may
// not be from that result.
//
// The migration has to do two different things with those two facts. The result
// still exists, so it becomes a run. The images cannot be tied to it - they may
// come from a round whose verdict was overwritten, possibly one that said the
// opposite - so they stay unattributed and are shown as an unknown run. Guessing
// there is the exact failure this work exists to remove: a stale capture read as
// current evidence for a verdict it contradicts.
func TestMigration0053BackfillsTheResultAndDoesNotClaimTheEvidence(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "ao.db")+pragmas)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	// Stop at 0052: the machine's result is four columns on the case row.
	upTo(t, db, 52)

	for _, stmt := range []string{
		`INSERT INTO projects (id, path, registered_at) VALUES ('smk', '/tmp/smk', '2026-08-20')`,
		`INSERT INTO sessions (id, project_id, num, kind, activity_state, activity_last_at, created_at, updated_at)
		 VALUES ('smk-1', 'smk', 1, 'worker', 'idle', '2026-08-20', '2026-08-20', '2026-08-20')`,
		// played: a machine result AND two captures under it.
		`INSERT INTO smoke_check (id, session_id, project_id, seq, name, verdict, agent_verdict, agent_note, agent_ran_at, agent_sha, created_at, updated_at)
		 VALUES ('played', 'smk-1', 'smk', 1, 'the tab stays live', 'pending', 'fail', 'inverted from d44ad432c', '2026-08-21T10:00:00Z', 'd44ad432c', '2026-08-20', '2026-08-22')`,
		`INSERT INTO smoke_evidence (id, check_id, session_id, kind, filename, mime, size_bytes, created_at, source)
		 VALUES ('ev1', 'played', 'smk-1', 'image', 'a.png', 'image/png', 10, '2026-08-21T09:00:00Z', 'agent')`,
		`INSERT INTO smoke_evidence (id, check_id, session_id, kind, filename, mime, size_bytes, created_at, source)
		 VALUES ('ev2', 'played', 'smk-1', 'image', 'b.png', 'image/png', 10, '2026-08-21T10:00:00Z', 'agent')`,
		// mine: the user's own screenshot. It is not in the machine's lane at all
		// and must not acquire a run.
		`INSERT INTO smoke_evidence (id, check_id, session_id, kind, filename, mime, size_bytes, created_at, source)
		 VALUES ('ev3', 'played', 'smk-1', 'image', 'mine.png', 'image/png', 10, '2026-08-21T11:00:00Z', 'user')`,
		// untouched: no machine has ever run it, so it gets no run.
		`INSERT INTO smoke_check (id, session_id, project_id, seq, name, verdict, created_at, updated_at)
		 VALUES ('untouched', 'smk-1', 'smk', 2, 'the drag scrolls', 'pending', '2026-08-20', '2026-08-20')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("seed at 0052 (%s): %v", stmt, err)
		}
	}

	upTo(t, db, 53)

	var runs int
	if err := db.QueryRow(`SELECT COUNT(*) FROM smoke_run`).Scan(&runs); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if runs != 1 {
		t.Fatalf("runs = %d, want exactly 1 - the result that still exists becomes a run, and no row is invented for the ones already overwritten", runs)
	}

	var seq int
	var verdict, note, sha string
	var recordedAt sql.NullString
	if err := db.QueryRow(`SELECT seq, verdict, note, sha, recorded_at FROM smoke_run WHERE check_id = 'played'`).
		Scan(&seq, &verdict, &note, &sha, &recordedAt); err != nil {
		t.Fatalf("read the backfilled run: %v", err)
	}
	if seq != 1 || verdict != "fail" || note != "inverted from d44ad432c" || sha != "d44ad432c" {
		t.Errorf("backfilled run = seq %d %q/%q/%q, want the case's own result verbatim", seq, verdict, note, sha)
	}
	if !recordedAt.Valid {
		t.Error("the backfilled run has no recorded_at, so it reads as a round that never concluded - it did conclude, that is the whole reason it survived")
	}

	// The captures stay unattributed. Attributing them to the backfilled run
	// would make an image that may belong to an OPPOSITE, overwritten verdict
	// render as the evidence for this one.
	rows, err := db.Query(`SELECT id, run_id FROM smoke_evidence ORDER BY id`)
	if err != nil {
		t.Fatalf("read evidence: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, runID string
		if err := rows.Scan(&id, &runID); err != nil {
			t.Fatalf("scan evidence: %v", err)
		}
		if runID != "" {
			t.Errorf("evidence %s was attributed to run %q; legacy captures belong to no known run and must say so", id, runID)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate evidence: %v", err)
	}

	// The four columns are gone: one fact, one place.
	if _, err := db.Exec(`SELECT agent_verdict FROM smoke_check`); err == nil {
		t.Error("smoke_check.agent_verdict still exists - the case row would be a second source of truth for the machine's result")
	}
}
