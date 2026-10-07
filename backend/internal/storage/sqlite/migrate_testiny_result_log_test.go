package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMigration0074TestinyResultLog(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "ao.db")+pragmas)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	upTo(t, db, 74)
	for _, stmt := range []string{
		`INSERT INTO projects (id, path, registered_at) VALUES ('tny', '/tmp/tny', '2026-10-07')`,
		`INSERT INTO sessions (id, project_id, num, kind, activity_state, activity_last_at, created_at, updated_at)
		 VALUES ('tny-1', 'tny', 1, 'worker', 'idle', '2026-10-07', '2026-10-07', '2026-10-07')`,
		`INSERT INTO testiny_run_link (session_id, run_id, created_at) VALUES ('tny-1', 632, '2026-10-07')`,
		`INSERT INTO testiny_run_link (session_id, run_id, created_at) VALUES ('tny-1', 633, '2026-10-07')`,
		`INSERT INTO testiny_result_log (session_id, run_id, case_id, status, created_at) VALUES ('tny-1', 632, 7166, 'PASSED', '2026-10-07')`,
		`INSERT INTO testiny_result_log (session_id, run_id, case_id, status, comment, set_by, sha, created_at)
		 VALUES ('tny-1', 633, 7167, 'FAILED', 'ปุ่มไม่แสดง', 'tny-2', '4f2c9e1', '2026-10-07')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("seed (%s): %v", stmt, err)
		}
	}
	for name, stmt := range map[string]string{
		"unknown status": `INSERT INTO testiny_result_log (session_id, run_id, case_id, status, created_at) VALUES ('tny-1', 632, 1, 'UNTESTED', '2026-10-07')`,
		"unlinked run":   `INSERT INTO testiny_result_log (session_id, run_id, case_id, status, created_at) VALUES ('tny-1', 999, 1, 'PASSED', '2026-10-07')`,
	} {
		if _, err := db.Exec(stmt); err == nil {
			t.Errorf("%s: insert succeeded, want a constraint error", name)
		}
	}

	count := func(where string) int {
		t.Helper()
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM testiny_result_log WHERE ` + where).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if _, err := db.Exec(`DELETE FROM testiny_run_link WHERE session_id = 'tny-1' AND run_id = 632`); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	if n := count(`run_id = 632`); n != 0 {
		t.Fatalf("%d log rows outlived their run's link", n)
	}
	if n := count(`run_id = 633`); n != 1 {
		t.Fatalf("unlinking run 632 removed run 633's log (%d rows left)", n)
	}
	// The session's change_log rows go first, as PurgeSession does: they hold
	// the session row without a cascade.
	for _, stmt := range []string{`DELETE FROM change_log WHERE session_id = 'tny-1'`, `DELETE FROM sessions WHERE id = 'tny-1'`} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("delete session (%s): %v", stmt, err)
		}
	}
	if n := count(`1 = 1`); n != 0 {
		t.Fatalf("%d log rows outlived their task", n)
	}

	var n int
	downTo(t, db, 73)
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'testiny_result_log'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("testiny_result_log still exists after Down")
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'testiny_run_link'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("Down of 0074 dropped testiny_run_link too")
	}
}
