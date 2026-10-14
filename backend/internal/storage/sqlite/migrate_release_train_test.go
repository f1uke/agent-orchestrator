package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMigration0084ReleaseTrainObservations(t *testing.T) {
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
	count := func(q string) int {
		t.Helper()
		var n int
		if err := db.QueryRow(q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	upTo(t, db, 83)
	exec("seed at 0083",
		`INSERT INTO projects (id, path, registered_at) VALUES ('demo', '/tmp/demo', '2026-10-14')`)
	changeLogBefore := count(`SELECT COUNT(*) FROM change_log`)

	upTo(t, db, 84)
	exec("observe one resource",
		`INSERT INTO release_resource (kind, scope, app_id, sync_mode, fact_version, planned_at) VALUES ('gitlab.mrs', 'p:1001', 'di', 'incremental', 1, '2026-10-14')`,
		`INSERT INTO release_item (kind, scope, item_key, body) VALUES ('gitlab.mrs', 'p:1001', '7', '{}')`)
	if _, err := db.Exec(`INSERT INTO release_resource (kind, scope, sync_mode, fact_version, planned_at, error_class) VALUES ('gitlab.tags', 'p:1', 'snapshot', 1, '2026-10-14', 'flaky')`); err == nil {
		t.Fatal("an unknown error_class was accepted")
	}
	if _, err := db.Exec(`INSERT INTO release_item (kind, scope, item_key, body) VALUES ('gitlab.tags', 'p:404', 'x', '{}')`); err == nil {
		t.Fatal("an item without its resource was accepted")
	}
	if n := count(`SELECT COUNT(*) FROM change_log`); n != changeLogBefore {
		t.Fatalf("change_log grew from %d to %d; observations are not CDC", changeLogBefore, n)
	}
	exec("delete the resource", `DELETE FROM release_resource WHERE kind = 'gitlab.mrs'`)
	if n := count(`SELECT COUNT(*) FROM release_item`); n != 0 {
		t.Fatalf("%d items outlived their resource; the cascade must drop them", n)
	}

	downTo(t, db, 83)
	if n := count(`SELECT COUNT(*) FROM sqlite_master WHERE name IN ('release_resource', 'release_item')`); n != 0 {
		t.Fatalf("%d release tables after Down", n)
	}
	if n := count(`SELECT COUNT(*) FROM projects`); n != 1 {
		t.Fatalf("projects after Down = %d, want the seed kept", n)
	}
}
