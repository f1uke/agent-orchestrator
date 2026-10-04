package sqlite

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigration0069BackfillsHistoryAndUndoState guards the proposals decided
// before decisions were kept as events: each gets one event, a snoozed one its
// snooze, and an applied new memory the index line undo must take out.
func TestMigration0069BackfillsHistoryAndUndoState(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "ao.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	upTo(t, db, 68)

	seed := []struct{ id, action, status, snoozed, decided, reason, resolution string }{
		{"1", "create_memory", "applied", "", "2026-10-04T10:00:00Z", "", ""},
		{"2", "create_memory", "rejected", "", "2026-10-04T10:01:00Z", "one-off", ""},
		{"3", "create_memory", "pending", "2027-01-02T00:00:00Z", "", "", ""},
		{"4", "create_memory", "pending", "", "", "", ""},
		{"5", "conflict", "applied", "", "2026-10-04T10:02:00Z", "", "words_win"},
		{"6", "update_memory", "applied", "", "2026-10-04T10:03:00Z", "", ""},
	}
	for _, s := range seed {
		snoozed, decided := any(nil), any(nil)
		if s.snoozed != "" {
			snoozed = s.snoozed
		}
		if s.decided != "" {
			decided = s.decided
		}
		if _, err := db.Exec(`INSERT INTO learn_proposal (id, project_id, task_key, action, target_path, scope, title, index_line, status,
			snoozed_until, decided_at, reject_reason, resolution, created_at, updated_at)
			VALUES (?, 'p', 't', ?, '/m/x'||?||'.md', 'project:p', 't', '- [X](x.md) - x', ?, ?, ?, ?, ?, '2026-10-04T09:00:00Z', '2026-10-04T09:30:00Z')`,
			s.id, s.action, s.id, s.status, snoozed, decided, s.reason, s.resolution); err != nil {
			t.Fatalf("seed %s: %v", s.id, err)
		}
	}
	upTo(t, db, 69)

	rows, err := db.Query(`SELECT proposal_id, kind, status, note, snoozed_until IS NOT NULL FROM learn_proposal_event ORDER BY proposal_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var id, kind, status, note string
		var snoozed bool
		if err := rows.Scan(&id, &kind, &status, &note, &snoozed); err != nil {
			t.Fatal(err)
		}
		e := id + ":" + kind + ":" + status + ":" + note
		if snoozed {
			e += ":until"
		}
		got = append(got, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := "1:approved:applied:,2:rejected:rejected:one-off,3:snoozed:pending::until,5:approved:applied:words_win,6:approved:applied:"
	if strings.Join(got, ",") != want {
		t.Errorf("events =\n %s\nwant\n %s", strings.Join(got, ","), want)
	}

	var line1, line6 string
	if err := db.QueryRow(`SELECT applied_index_line FROM learn_proposal WHERE id = 1`).Scan(&line1); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT applied_index_line FROM learn_proposal WHERE id = 6`).Scan(&line6); err != nil {
		t.Fatal(err)
	}
	if line1 != "- [X](x.md) - x" || line6 != "" {
		t.Errorf("applied_index_line = %q / %q, want the line for the applied new memory only", line1, line6)
	}
}
