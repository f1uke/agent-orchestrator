package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMigration0088SessionTransport(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "ao.db")+pragmas)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	upTo(t, db, 87)
	for _, stmt := range []string{
		`INSERT INTO projects (id, path, registered_at) VALUES ('mer', '/tmp/mer', '2026-10-30')`,
		`INSERT INTO sessions (id, project_id, num, kind, activity_state, activity_last_at, created_at, updated_at)
		 VALUES ('mer-1', 'mer', 1, 'worker', 'idle', '2026-10-30', '2026-10-30', '2026-10-30')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("seed at 0087 (%s): %v", stmt, err)
		}
	}

	upTo(t, db, 88)
	var transport string
	if err := db.QueryRow(`SELECT transport FROM sessions WHERE id = 'mer-1'`).Scan(&transport); err != nil {
		t.Fatal(err)
	}
	if transport != "terminal" {
		t.Fatalf("a row from before 0088 reads transport %q, want terminal", transport)
	}

	downTo(t, db, 87)
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name = 'transport'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("transport column survived Down")
	}
}
