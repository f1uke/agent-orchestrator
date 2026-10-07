package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMigration0076TestinyStepResults(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "ao.db")+pragmas)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	exec := func(what string, stmts ...string) {
		t.Helper()
		for _, stmt := range stmts {
			if _, err := db.Exec(stmt); err != nil {
				t.Fatalf("%s (%s): %v", what, stmt, err)
			}
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

	upTo(t, db, 75)
	exec("seed at 0075",
		`INSERT INTO projects (id, path, registered_at) VALUES ('tny', '/tmp/tny', '2026-10-07')`,
		`INSERT INTO sessions (id, project_id, num, kind, activity_state, activity_last_at, created_at, updated_at)
		 VALUES ('tny-1', 'tny', 1, 'worker', 'idle', '2026-10-07', '2026-10-07', '2026-10-07')`,
		`INSERT INTO testiny_run_link (session_id, run_id, created_at) VALUES ('tny-1', 632, '2026-10-07')`,
		`INSERT INTO testiny_run_link (session_id, run_id, created_at) VALUES ('tny-1', 633, '2026-10-07')`,
		`INSERT INTO testiny_result_log (session_id, run_id, case_id, status, comment, set_by, sha, created_at)
		 VALUES ('tny-1', 632, 7167, 'FAILED', 'ปุ่มไม่แสดง', 'tny-2', '4f2c9e1', '2026-10-07')`,
	)

	upTo(t, db, 76)
	if n := count(`case_id = 7167 AND status = 'FAILED' AND comment = 'ปุ่มไม่แสดง' AND set_by = 'tny-2' AND sha = '4f2c9e1' AND steps = '[]'`); n != 1 {
		t.Fatalf("the logged result did not survive the rebuild whole (%d rows match)", n)
	}
	exec("log step results",
		`INSERT INTO testiny_result_log (session_id, run_id, case_id, status, steps, set_by, created_at)
		 VALUES ('tny-1', 632, 7168, '', '[{"n":2,"status":"FAILED"}]', 'tny-2', '2026-10-07')`,
		`INSERT INTO testiny_result_log (session_id, run_id, case_id, status, steps, created_at)
		 VALUES ('tny-1', 633, 7169, 'PASSED', '[{"n":1,"status":"PASSED"}]', '2026-10-07')`,
	)
	for name, stmt := range map[string]string{
		"neither a status nor steps": `INSERT INTO testiny_result_log (session_id, run_id, case_id, status, created_at) VALUES ('tny-1', 632, 1, '', '2026-10-07')`,
		"a comment with no status":   `INSERT INTO testiny_result_log (session_id, run_id, case_id, status, comment, steps, created_at) VALUES ('tny-1', 632, 1, '', 'x', '[{"n":1,"status":"FAILED"}]', '2026-10-07')`,
		"steps that are not JSON":    `INSERT INTO testiny_result_log (session_id, run_id, case_id, status, steps, created_at) VALUES ('tny-1', 632, 1, 'PASSED', 'step 1', '2026-10-07')`,
		"unknown status":             `INSERT INTO testiny_result_log (session_id, run_id, case_id, status, created_at) VALUES ('tny-1', 632, 1, 'UNTESTED', '2026-10-07')`,
		"unlinked run":               `INSERT INTO testiny_result_log (session_id, run_id, case_id, status, created_at) VALUES ('tny-1', 999, 1, 'PASSED', '2026-10-07')`,
	} {
		if _, err := db.Exec(stmt); err == nil {
			t.Errorf("%s: insert succeeded, want a constraint error", name)
		}
	}
	var indexes int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_testiny_result_log_case'`).Scan(&indexes); err != nil || indexes != 1 {
		t.Fatalf("the case index is gone after the rebuild (%d, %v)", indexes, err)
	}

	// Down keeps every row the old table can hold: a write that set only
	// steps cannot exist there, and a step result is dropped from the rest.
	downTo(t, db, 75)
	if n := count(`1 = 1`); n != 2 {
		t.Fatalf("%d rows after Down, want the 2 that set a case status", n)
	}
	if n := count(`case_id = 7168`); n != 0 {
		t.Fatal("Down kept a write that set only steps")
	}
	var columns int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('testiny_result_log') WHERE name = 'steps'`).Scan(&columns); err != nil || columns != 0 {
		t.Fatalf("steps column after Down: %d, %v", columns, err)
	}
	exec("unlink after Down", `DELETE FROM testiny_run_link WHERE session_id = 'tny-1' AND run_id = 632`)
	if n := count(`run_id = 632`); n != 0 {
		t.Fatalf("%d log rows outlived their run's link after Down", n)
	}
}
