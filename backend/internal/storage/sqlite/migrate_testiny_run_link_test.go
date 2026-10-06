package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMigration0073TestinyRunLink(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "ao.db")+pragmas)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	upTo(t, db, 73)
	for _, stmt := range []string{
		`INSERT INTO projects (id, path, registered_at) VALUES ('tny', '/tmp/tny', '2026-10-06')`,
		`INSERT INTO sessions (id, project_id, num, kind, activity_state, activity_last_at, created_at, updated_at)
		 VALUES ('tny-1', 'tny', 1, 'worker', 'idle', '2026-10-06', '2026-10-06', '2026-10-06')`,
		`INSERT INTO testiny_run_link (session_id, run_id, created_at) VALUES ('tny-1', 632, '2026-10-06')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("seed (%s): %v", stmt, err)
		}
	}
	for name, stmt := range map[string]string{
		"run id 0":        `INSERT INTO testiny_run_link (session_id, run_id, created_at) VALUES ('tny-1', 0, '2026-10-06')`,
		"unknown session": `INSERT INTO testiny_run_link (session_id, run_id, created_at) VALUES ('nope', 1, '2026-10-06')`,
		"duplicate":       `INSERT INTO testiny_run_link (session_id, run_id, created_at) VALUES ('tny-1', 632, '2026-10-07')`,
	} {
		if _, err := db.Exec(stmt); err == nil {
			t.Errorf("%s: insert succeeded, want a constraint error", name)
		}
	}

	var n int
	downTo(t, db, 72)
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'testiny_run_link'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("testiny_run_link still exists after Down")
	}
}
