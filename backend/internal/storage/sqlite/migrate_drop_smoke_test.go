package sqlite

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

var smokeTables = []string{"smoke_check", "smoke_evidence", "smoke_checklist_state", "smoke_run"}

func TestMigration0072DropsSmokeAndItsDownRestoresEmptyTables(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "ao.db")+pragmas)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	upTo(t, db, 71)
	shapeAt71 := smokeShape(t, db)

	for _, stmt := range []string{
		`INSERT INTO projects (id, path, registered_at) VALUES ('smk', '/tmp/smk', '2026-10-01')`,
		`INSERT INTO sessions (id, project_id, num, kind, activity_state, activity_last_at, created_at, updated_at)
		 VALUES ('smk-1', 'smk', 1, 'worker', 'idle', '2026-10-01', '2026-10-01', '2026-10-01')`,
		`INSERT INTO smoke_check (id, session_id, project_id, seq, name, created_at, updated_at)
		 VALUES ('c1', 'smk-1', 'smk', 1, 'the tab stays live', '2026-10-01', '2026-10-01')`,
		`INSERT INTO smoke_run (id, check_id, session_id, seq, verdict, created_at, updated_at)
		 VALUES ('r1', 'c1', 'smk-1', 1, 'pass', '2026-10-01', '2026-10-01')`,
		`INSERT INTO smoke_evidence (id, check_id, session_id, kind, created_at, run_id)
		 VALUES ('e1', 'c1', 'smk-1', 'image', '2026-10-01', 'r1')`,
		`INSERT INTO smoke_checklist_state (session_id, stood_down_at, reason, created_at, updated_at)
		 VALUES ('smk-1', '2026-10-01', 'no ui', '2026-10-01', '2026-10-01')`,
		`INSERT INTO learn_excerpt (project_id, transcript_path, turn_uuid, turn_at, source_class, human_text, created_at)
		 VALUES ('smk', '/t.jsonl', 'u1', '2026-10-01', 'smoke_report', 'CHECK 1 fails', '2026-10-01'),
		        ('smk', '/t.jsonl', 'u2', '2026-10-01', 'typed', 'keep the tab', '2026-10-01')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("seed at 0071 (%s): %v", stmt, err)
		}
	}

	upTo(t, db, 72)

	for _, table := range smokeTables {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = ?`, table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%s still exists after 0072", table)
		}
	}
	got := rowStrings(t, db, `SELECT turn_uuid || ':' || source_class FROM learn_excerpt ORDER BY turn_uuid`)
	if want := []string{"u1:app_send", "u2:typed"}; !reflect.DeepEqual(got, want) {
		t.Errorf("learn_excerpt source_class = %v, want %v", got, want)
	}

	downTo(t, db, 71)

	for _, table := range smokeTables {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatalf("count %s after Down: %v", table, err)
		}
		if n != 0 {
			t.Errorf("%s has %d rows after Down, want an empty table", table, n)
		}
	}
	if shape := smokeShape(t, db); !reflect.DeepEqual(shape, shapeAt71) {
		t.Errorf("smoke tables after Down differ from 0071:\n got  %v\n want %v", shape, shapeAt71)
	}
	for _, bad := range []string{
		`INSERT INTO smoke_check (id, session_id, project_id, name, authored_by_role, created_at, updated_at)
		 VALUES ('x', 'smk-1', 'smk', 'n', 'boss', '2026-10-01', '2026-10-01')`,
		`INSERT INTO smoke_run (id, check_id, session_id, verdict, created_at, updated_at)
		 VALUES ('x', 'c1', 'smk-1', 'maybe', '2026-10-01', '2026-10-01')`,
		`INSERT INTO smoke_evidence (id, check_id, session_id, kind, created_at, source)
		 VALUES ('x', 'c1', 'smk-1', 'image', '2026-10-01', 'robot')`,
		`INSERT INTO smoke_checklist_state (session_id, stood_down_at, stood_down_by_role, reason, created_at, updated_at)
		 VALUES ('smk-1', '2026-10-01', 'boss', 'r', '2026-10-01', '2026-10-01')`,
	} {
		if _, err := db.Exec(bad); err == nil || !strings.Contains(err.Error(), "CHECK") {
			t.Errorf("Down lost a CHECK constraint, insert gave %v:\n%s", err, bad)
		}
	}
}

// smokeShape renders each smoke table's columns, foreign keys and indexes, so
// the shape Down rebuilds can be compared with the one 0027..0056 left behind.
func smokeShape(t *testing.T, db *sql.DB) map[string][]string {
	t.Helper()
	shape := map[string][]string{}
	for _, table := range smokeTables {
		shape[table] = append(shape[table], rowStrings(t, db, `PRAGMA table_info(`+table+`)`)...)
		shape[table] = append(shape[table], rowStrings(t, db, `PRAGMA foreign_key_list(`+table+`)`)...)
		for _, idx := range rowStrings(t, db, `SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = '`+table+`' AND sql IS NOT NULL ORDER BY name`) {
			shape[table] = append(shape[table], "index "+idx)
			shape[table] = append(shape[table], rowStrings(t, db, `PRAGMA index_info(`+idx+`)`)...)
		}
	}
	return shape
}

func rowStrings(t *testing.T, db *sql.DB, query string) []string {
	t.Helper()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprint(vals...))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func downTo(t *testing.T, db *sql.DB, version int64) {
	t.Helper()
	gooseMu.Lock()
	defer gooseMu.Unlock()
	goose.SetBaseFS(migrationsFS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("set dialect: %v", err)
	}
	if err := goose.DownTo(db, "migrations", version); err != nil {
		t.Fatalf("migrate down to %d: %v", version, err)
	}
}
