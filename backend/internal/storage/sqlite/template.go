package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Test binaries only: a migrated, empty database image, built once per binary
// and copied in place of migrating every fresh database from scratch.
//
// Why it exists: the test suites run with -race, and under the race detector
// the pure-Go driver needs about two seconds to apply the full migration chain
// (SQLite re-parses the whole schema after every DDL statement, and each parse
// is instrumented). The store package alone opens a fresh database ~110 times,
// which put it at 586 of its 600 allowed seconds on CI - a timeout waiting for
// the next test anyone added. Copying a pre-migrated image costs a file write.
//
// It is inert outside a test binary (testing.Testing), and only ever seeds a
// database file that does not exist yet, so production opens - and any test
// that prepares its own file - migrate exactly as before. The copy is then
// opened through the normal path, so goose still checks it is at the latest
// version.
var (
	templateOnce  sync.Once
	templateImage []byte
	templateErr   error
)

// seedFromTemplate writes the migrated image to path when running under `go
// test` and path does not exist. Any failure leaves path absent, so Open falls
// back to migrating it the slow way.
func seedFromTemplate(path string) {
	if !testing.Testing() {
		return
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		return
	}
	templateOnce.Do(func() { templateImage, templateErr = buildTemplate() })
	if templateErr != nil {
		return
	}
	if err := os.WriteFile(path, templateImage, 0o600); err != nil {
		_ = os.Remove(path)
	}
}

// buildTemplate migrates a throwaway database and returns its file. Closing the
// only connection checkpoints the WAL into the main file, so the one file is the
// whole database.
func buildTemplate() ([]byte, error) {
	dir, err := os.MkdirTemp("", "ao-sqlite-template-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, "ao.db")
	db, err := sql.Open("sqlite", "file:"+path+pragmas)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := db.Close(); err != nil {
		return nil, err
	}
	if _, err := os.Stat(path + "-wal"); err == nil {
		return nil, fmt.Errorf("template database left a WAL behind")
	}
	return os.ReadFile(path) //nolint:gosec // a file this function just created
}
