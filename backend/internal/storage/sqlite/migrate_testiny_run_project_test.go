package sqlite

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMigration0080TestinyRunProject(t *testing.T) {
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
	config := func(id string) map[string]any {
		t.Helper()
		var raw sql.NullString
		if err := db.QueryRow(`SELECT config FROM projects WHERE id = ?`, id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if !raw.Valid {
			return nil
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(raw.String), &out); err != nil {
			t.Fatalf("config of %s is not JSON: %v", id, err)
		}
		return out
	}

	upTo(t, db, 79)
	exec("seed at 0079",
		`INSERT INTO projects (id, path, registered_at, config) VALUES ('mob', '/tmp/mob', '2026-10-07', '{"defaultBranch":"develop","testinyProject":"MOB","hasIOSSimulator":true}')`,
		`INSERT INTO projects (id, path, registered_at, config) VALUES ('blank', '/tmp/blank', '2026-10-07', '{"testinyProject":"  "}')`,
		`INSERT INTO projects (id, path, registered_at, config) VALUES ('web', '/tmp/web', '2026-10-07', '{"defaultBranch":"main","hasWebUI":true}')`,
		`INSERT INTO projects (id, path, registered_at) VALUES ('bare', '/tmp/bare', '2026-10-07')`,
		`INSERT INTO sessions (id, project_id, num, kind, activity_state, activity_last_at, created_at, updated_at)
		 VALUES ('mob-1', 'mob', 1, 'worker', 'idle', '2026-10-07', '2026-10-07', '2026-10-07')`,
		`INSERT INTO testiny_run_link (session_id, run_id, created_at) VALUES ('mob-1', 632, '2026-10-07')`,
	)

	upTo(t, db, 80)
	for id, want := range map[string]map[string]any{
		"mob":   {"defaultBranch": "develop", "usesTestiny": true, "hasIOSSimulator": true},
		"blank": {},
		"web":   {"defaultBranch": "main", "hasWebUI": true},
		"bare":  nil,
	} {
		if got := config(id); !reflect.DeepEqual(got, want) {
			t.Errorf("config of %s after Up = %v, want %v", id, got, want)
		}
	}
	var (
		projectID        int64
		projectKey, name string
	)
	if err := db.QueryRow(`SELECT project_id, project_key, project_name FROM testiny_run_link WHERE run_id = 632`).Scan(&projectID, &projectKey, &name); err != nil {
		t.Fatal(err)
	}
	if projectID != 0 || projectKey != "" || name != "" {
		t.Fatalf("an existing link got project %d %q %q, want it unset until the service fills it", projectID, projectKey, name)
	}
	exec("link a run with its project",
		`INSERT INTO testiny_run_link (session_id, run_id, project_id, project_key, project_name, created_at) VALUES ('mob-1', 700, 3, 'STAR', 'STAR', '2026-10-07')`)

	downTo(t, db, 79)
	var columns int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('testiny_run_link') WHERE name LIKE 'project_%'`).Scan(&columns); err != nil || columns != 0 {
		t.Fatalf("project columns after Down: %d, %v", columns, err)
	}
	var links int
	if err := db.QueryRow(`SELECT COUNT(*) FROM testiny_run_link`).Scan(&links); err != nil || links != 2 {
		t.Fatalf("%d links after Down, want both kept (%v)", links, err)
	}
}
