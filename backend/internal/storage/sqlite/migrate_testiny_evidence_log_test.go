package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMigration0079TestinyEvidenceLog(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "ao.db")+pragmas)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	upTo(t, db, 79)
	for _, stmt := range []string{
		`INSERT INTO projects (id, path, registered_at) VALUES ('tny', '/tmp/tny', '2026-10-07')`,
		`INSERT INTO sessions (id, project_id, num, kind, activity_state, activity_last_at, created_at, updated_at)
		 VALUES ('tny-1', 'tny', 1, 'worker', 'idle', '2026-10-07', '2026-10-07', '2026-10-07')`,
		`INSERT INTO testiny_run_link (session_id, run_id, created_at) VALUES ('tny-1', 191, '2026-10-07')`,
		`INSERT INTO testiny_run_link (session_id, run_id, created_at) VALUES ('tny-1', 192, '2026-10-07')`,
		`INSERT INTO testiny_evidence_log (session_id, run_id, event, file, created_at) VALUES ('tny-1', 191, 'uploaded', 'README.md', '2026-10-07')`,
		`INSERT INTO testiny_evidence_log (session_id, run_id, event, file, case_id, drive_id, comment_id, set_by, created_at)
		 VALUES ('tny-1', 191, 'linked', 'TC-1 pass - iPhone 15.png', 1, '1abc', 2601, 'tny-2', '2026-10-07')`,
		`INSERT INTO testiny_evidence_log (session_id, run_id, event, file, case_id, drive_id, created_at) VALUES ('tny-1', 192, 'uploaded', 'TC-9 pass.png', 9, '1def', '2026-10-07')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("seed (%s): %v", stmt, err)
		}
	}
	for name, stmt := range map[string]string{
		"unknown event":          `INSERT INTO testiny_evidence_log (session_id, run_id, event, file, created_at) VALUES ('tny-1', 191, 'deleted', 'a.png', '2026-10-07')`,
		"no file":                `INSERT INTO testiny_evidence_log (session_id, run_id, event, file, created_at) VALUES ('tny-1', 191, 'uploaded', '', '2026-10-07')`,
		"linked with no case":    `INSERT INTO testiny_evidence_log (session_id, run_id, event, file, drive_id, comment_id, created_at) VALUES ('tny-1', 191, 'linked', 'a.png', '1a', 1, '2026-10-07')`,
		"linked with no drive":   `INSERT INTO testiny_evidence_log (session_id, run_id, event, file, case_id, comment_id, created_at) VALUES ('tny-1', 191, 'linked', 'a.png', 1, 1, '2026-10-07')`,
		"linked with no comment": `INSERT INTO testiny_evidence_log (session_id, run_id, event, file, case_id, drive_id, created_at) VALUES ('tny-1', 191, 'linked', 'a.png', 1, '1a', '2026-10-07')`,
		"unlinked run":           `INSERT INTO testiny_evidence_log (session_id, run_id, event, file, created_at) VALUES ('tny-1', 999, 'uploaded', 'a.png', '2026-10-07')`,
	} {
		if _, err := db.Exec(stmt); err == nil {
			t.Errorf("%s: insert succeeded, want a constraint error", name)
		}
	}

	count := func(where string) int {
		t.Helper()
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM testiny_evidence_log WHERE ` + where).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if _, err := db.Exec(`DELETE FROM testiny_run_link WHERE session_id = 'tny-1' AND run_id = 191`); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	if n := count(`run_id = 191`); n != 0 {
		t.Fatalf("%d log rows outlived their run's link", n)
	}
	if n := count(`run_id = 192`); n != 1 {
		t.Fatalf("unlinking run 191 removed run 192's log (%d rows left)", n)
	}
	for _, stmt := range []string{`DELETE FROM change_log WHERE session_id = 'tny-1'`, `DELETE FROM sessions WHERE id = 'tny-1'`} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("delete session (%s): %v", stmt, err)
		}
	}
	if n := count(`1 = 1`); n != 0 {
		t.Fatalf("%d log rows outlived their task", n)
	}

	var n int
	downTo(t, db, 78)
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'testiny_evidence_log'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("testiny_evidence_log still exists after Down")
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'testiny_result_log'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("Down of 0079 dropped testiny_result_log too")
	}
}
