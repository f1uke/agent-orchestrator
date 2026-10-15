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

func TestMigration0085ReleaseTrainHumanState(t *testing.T) {
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
	refused := func(what, stmt string) {
		t.Helper()
		if _, err := db.Exec(stmt); err == nil {
			t.Fatalf("%s was accepted: %s", what, stmt)
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

	upTo(t, db, 84)
	exec("seed at 0084",
		`INSERT INTO release_resource (kind, scope, app_id, sync_mode, fact_version, planned_at) VALUES ('gitlab.mrs', 'p:1001', 'di', 'incremental', 1, '2026-10-15')`)
	changeLogBefore := count(`SELECT COUNT(*) FROM change_log`)

	upTo(t, db, 85)
	exec("write human state",
		`INSERT INTO release_rule_setting (rule_id, enabled, updated_at) VALUES ('C2', TRUE, '2026-10-15')`,
		`INSERT INTO release_hit_hide (hit_id, evidence, severity, reason, hidden_at) VALUES ('D3~v/21-2026/di', 'abc', 'risk', 'test', '2026-10-15')`,
		`INSERT INTO release_llb_plan (id, name, target_kind, target_train, version, updated_by, updated_at) VALUES ('demo-llb', 'Demo LLB', 'train', '22-2026', 1, 'cli', '2026-10-15')`,
		`INSERT INTO release_llb_plan (id, name, target_kind, version, updated_by, updated_at) VALUES ('other-llb', 'Other', 'unplanned', 1, 'cli', '2026-10-15')`,
		`INSERT INTO release_llb_plan_branch (plan_id, app_id, branch) VALUES ('demo-llb', 'di', 'feature/demo-llb')`,
		`INSERT INTO release_llb_plan_accepted (plan_id, mr_ref, reason) VALUES ('demo-llb', 'di!52', 'agreed')`)
	refused("a severity outside the enum",
		`INSERT INTO release_hit_hide (hit_id, evidence, severity, hidden_at) VALUES ('x', 'e', 'urgent', '2026-10-15')`)
	refused("a train target without a train",
		`INSERT INTO release_llb_plan (id, name, target_kind, version, updated_by, updated_at) VALUES ('p3', 'P', 'train', 1, 'cli', '2026-10-15')`)
	refused("a dropped target with a train",
		`INSERT INTO release_llb_plan (id, name, target_kind, target_train, version, updated_by, updated_at) VALUES ('p4', 'P', 'dropped', '22-2026', 1, 'cli', '2026-10-15')`)
	refused("an unknown target kind",
		`INSERT INTO release_llb_plan (id, name, target_kind, version, updated_by, updated_at) VALUES ('p5', 'P', 'later', 1, 'cli', '2026-10-15')`)
	refused("one branch in two plans",
		`INSERT INTO release_llb_plan_branch (plan_id, app_id, branch) VALUES ('other-llb', 'di', 'feature/demo-llb')`)
	refused("a branch of no plan",
		`INSERT INTO release_llb_plan_branch (plan_id, app_id, branch) VALUES ('nope', 'da', 'feature/x')`)
	if n := count(`SELECT COUNT(*) FROM change_log`); n != changeLogBefore {
		t.Fatalf("change_log grew from %d to %d; human state is not CDC", changeLogBefore, n)
	}

	exec("delete the plan", `DELETE FROM release_llb_plan WHERE id = 'demo-llb'`)
	if n := count(`SELECT (SELECT COUNT(*) FROM release_llb_plan_branch) + (SELECT COUNT(*) FROM release_llb_plan_accepted)`); n != 0 {
		t.Fatalf("%d branch or accepted rows outlived their plan; the cascade must drop them", n)
	}

	downTo(t, db, 84)
	if n := count(`SELECT COUNT(*) FROM sqlite_master WHERE name LIKE 'release_rule_setting' OR name LIKE 'release_hit_hide' OR name LIKE 'release_llb_plan%'`); n != 0 {
		t.Fatalf("%d human-state tables after Down", n)
	}
	if n := count(`SELECT COUNT(*) FROM release_resource`); n != 1 {
		t.Fatalf("release_resource after Down = %d, want the seed kept", n)
	}
}

func TestMigration0086ReleaseTrainFixOutbox(t *testing.T) {
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
	refused := func(what, stmt string) {
		t.Helper()
		if _, err := db.Exec(stmt); err == nil {
			t.Fatalf("%s was accepted: %s", what, stmt)
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
	fix := func(id, state string) string {
		return `INSERT INTO release_fix (id, hit_id, rule_id, plan, plan_fingerprint, state, created_at, updated_at) VALUES ('` +
			id + `', 'E1~v/21-2026/di', 'E1', '{}', 'fp1', '` + state + `', '2026-10-15', '2026-10-15')`
	}

	upTo(t, db, 85)
	exec("seed at 0085",
		`INSERT INTO release_llb_plan (id, name, target_kind, version, updated_by, updated_at) VALUES ('demo-llb', 'Demo', 'unplanned', 1, 'cli', '2026-10-15')`)
	changeLogBefore := count(`SELECT COUNT(*) FROM change_log`)

	upTo(t, db, 86)
	exec("record a dry run", fix("fx-1", "dry_run"),
		`INSERT INTO release_fix_event (fix_id, at, actor, kind, detail) VALUES ('fx-1', '2026-10-15', 'cli', 'dry_run_recorded', '{}')`)
	refused("a second live fix for the same hit and plan", fix("fx-2", "applied"))
	refused("an unknown state", fix("fx-3", "queued"))
	refused("an unknown event kind",
		`INSERT INTO release_fix_event (fix_id, at, actor, kind, detail) VALUES ('fx-1', '2026-10-15', 'cli', 'sent', '{}')`)
	refused("an event of no fix",
		`INSERT INTO release_fix_event (fix_id, at, actor, kind, detail) VALUES ('nope', '2026-10-15', 'cli', 'withdrawn', '{}')`)
	refused("an edited event", `UPDATE release_fix_event SET actor = 'someone else'`)
	refused("a deleted event", `DELETE FROM release_fix_event`)
	refused("a deleted fix with events", `DELETE FROM release_fix WHERE id = 'fx-1'`)

	exec("withdraw it, then confirm the same plan again",
		`UPDATE release_fix SET state = 'withdrawn' WHERE id = 'fx-1'`, fix("fx-2", "dry_run"))
	if n := count(`SELECT COUNT(*) FROM release_fix`); n != 2 {
		t.Fatalf("release_fix rows = %d, want the withdrawn fix kept beside the new one", n)
	}
	if n := count(`SELECT COUNT(*) FROM change_log`); n != changeLogBefore {
		t.Fatalf("change_log grew from %d to %d; the fix outbox is not CDC", changeLogBefore, n)
	}

	downTo(t, db, 85)
	if n := count(`SELECT COUNT(*) FROM sqlite_master WHERE name LIKE 'release_fix%'`); n != 0 {
		t.Fatalf("%d fix outbox objects after Down", n)
	}
	if n := count(`SELECT COUNT(*) FROM release_llb_plan`); n != 1 {
		t.Fatalf("release_llb_plan after Down = %d, want the seed kept", n)
	}
}
