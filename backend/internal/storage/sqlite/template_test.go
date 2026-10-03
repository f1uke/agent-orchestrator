package sqlite

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// The test-binary template must be indistinguishable from migrating: same
// schema, triggers and indexes, and goose at the same version. Otherwise every
// store test would run against a database production never has.
func TestTemplate_MatchesAFullMigration(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir) // a fresh file under go test: seeded from the template
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	seededDB, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "ao.db")+pragmas)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = seededDB.Close() })

	scratch, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "ao.db")+pragmas)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scratch.Close() })
	if err := migrate(scratch); err != nil {
		t.Fatal(err)
	}

	read := func(db *sql.DB, q string) string {
		t.Helper()
		rows, err := db.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		var out []string
		for rows.Next() {
			var s sql.NullString
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			out = append(out, s.String)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return strings.Join(out, "\n")
	}
	for _, q := range []string{
		"SELECT type || ' ' || name || ' ' || COALESCE(sql, '') FROM sqlite_master ORDER BY type, name",
		"SELECT MAX(version_id) FROM goose_db_version WHERE is_applied",
	} {
		if got, want := read(seededDB, q), read(scratch, q); got != want {
			t.Errorf("template differs from a full migration for %q:\n got: %.300s\nwant: %.300s", q, got, want)
		}
	}
}
