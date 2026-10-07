package simhealth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"

	// The sqlite driver, registered as "sqlite".
	_ "modernc.org/sqlite"
)

// TrustStorePath is where a simulator keeps the root CAs it trusts, under the
// device's data directory. `simctl keychain add-root-cert` writes a row here.
func TrustStorePath(dataPath string) string {
	return filepath.Join(dataPath, "private", "var", "protected", "trustd", "private", "TrustStore.sqlite3")
}

// TrustStoreHas reports whether a trust store holds a root whose certificate
// DER hashes to sum. A device that has never trusted anything has no store at
// all, which reads as "not trusted" rather than as an error.
//
// The store belongs to the device's trustd, which may have it open, so it is
// opened read-only (`mode=ro`): the doctor must never be the thing that
// changes a device's trust.
func TrustStoreHas(ctx context.Context, store string, sum [sha256.Size]byte) (bool, error) {
	if _, err := os.Stat(store); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	db, err := sql.Open("sqlite", "file:"+(&url.URL{Path: store}).EscapedPath()+"?mode=ro")
	if err != nil {
		return false, err
	}
	defer func() { _ = db.Close() }()
	var n int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM tsettings WHERE sha256 = ?", sum[:]).Scan(&n); err != nil {
		return false, fmt.Errorf("query tsettings: %w", err)
	}
	return n > 0, nil
}
