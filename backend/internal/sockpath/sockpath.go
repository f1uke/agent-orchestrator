// Package sockpath keeps unix socket paths under the length every supported
// platform accepts, by falling back to hashed short names.
package sockpath

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

// MaxLen is the longest unix socket path every supported platform accepts:
// sun_path is 104 bytes on macOS (108 on Linux), NUL included.
const MaxLen = 103

// HashedNameLen is the length of the fallback socket file name In uses when a
// name does not fit.
const HashedNameLen = len("s-") + 16

// Dir returns dir when a hashed socket name still fits under it. Otherwise it
// returns a short directory under the OS temp dir, named tempPrefix plus a hash
// of dir, so two data dirs never share it.
func Dir(dir, tempPrefix string) string {
	if len(dir)+1+HashedNameLen <= MaxLen {
		return dir
	}
	sum := sha256.Sum256([]byte(dir))
	return filepath.Join(os.TempDir(), tempPrefix+hex.EncodeToString(sum[:6]))
}

// In names a socket inside dir: name itself, or a hash of it when the name would
// push the path past MaxLen.
func In(dir, name string) string {
	if p := filepath.Join(dir, name); len(p) <= MaxLen {
		return p
	}
	sum := sha256.Sum256([]byte(name))
	return filepath.Join(dir, "s-"+hex.EncodeToString(sum[:8]))
}
