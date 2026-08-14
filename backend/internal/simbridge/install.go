// Package simbridge is the only thing in AO that knows how a simulator's screen
// is actually read and touched.
//
// Everything above it - the `ao sim` commands, the device lease, the gesture
// hold - talks to the Driver interface and never to the mechanism. That matters
// because the mechanism is the risky part: a vendored native addon that dlopens
// private Apple frameworks, which can break on any Xcode upgrade. When Apple's
// own IDEDeviceInteraction/`xcrun mcp-server` route becomes usable, it slots in
// as a second Driver implementation and no command changes.
//
// There is no helper process, no port, no socket and no frame capture: one
// command runs one short-lived `node` process that does one thing and exits.
package simbridge

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// Version is the serve-sim release the vendored addon was taken from. It is in
// the asset's filename and in the install path so two AO builds with different
// addons cannot overwrite each other's copy.
const Version = "0.1.45"

const addonAsset = "assets/serve-sim-native-" + Version + ".node"

//go:embed assets/bridge.mjs assets/capture.mjs assets/serve-sim-native-0.1.45.node assets/LICENSE-serve-sim.txt assets/NOTICE-serve-sim.txt
var assets embed.FS

// Toolchain is where an installed bridge lives on disk.
type Toolchain struct {
	// Dir is the versioned directory holding every file below.
	Dir string
	// Script is our own bridge.mjs: one gesture or one read, then it exits.
	Script string
	// Capture is our own capture.mjs: the long-lived frame stream a human
	// watches. It is installed alongside the one-shot bridge because they share
	// the addon, and one copy of a vendored native binary on disk is the point.
	Capture string
	// Addon is the vendored serve-sim native addon.
	Addon string
}

// Install materializes the bridge under dataDir and returns where it landed.
// It is safe to call concurrently and on every command: each file is written to
// a temporary name and renamed into place, so a half-written addon can never be
// loaded, and an already-correct file is left alone.
//
// The install lives under the AO data dir (never a repository, never an OS
// application-support directory) because it is app state like everything else
// AO writes.
func Install(dataDir string) (Toolchain, error) {
	dir := filepath.Join(dataDir, "sim", "native", Version)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return Toolchain{}, fmt.Errorf("create simulator bridge directory: %w", err)
	}
	tc := Toolchain{
		Dir:     dir,
		Script:  filepath.Join(dir, "bridge.mjs"),
		Capture: filepath.Join(dir, "capture.mjs"),
		Addon:   filepath.Join(dir, filepath.Base(addonAsset)),
	}
	files := []struct {
		asset string
		path  string
		mode  os.FileMode
	}{
		{"assets/bridge.mjs", tc.Script, 0o640},
		{"assets/capture.mjs", tc.Capture, 0o640},
		{addonAsset, tc.Addon, 0o750},
		{"assets/LICENSE-serve-sim.txt", filepath.Join(dir, "LICENSE-serve-sim.txt"), 0o640},
		{"assets/NOTICE-serve-sim.txt", filepath.Join(dir, "NOTICE-serve-sim.txt"), 0o640},
	}
	for _, f := range files {
		want, err := assets.ReadFile(f.asset)
		if err != nil {
			return Toolchain{}, fmt.Errorf("read embedded %s: %w", f.asset, err)
		}
		if err := writeIfChanged(f.path, want, f.mode); err != nil {
			return Toolchain{}, err
		}
	}
	return tc, nil
}

// writeIfChanged rewrites path only when its contents differ, and does it
// atomically. Rewriting an identical file on every command would be pointless
// churn; writing in place would let one command read another's half-written
// addon.
func writeIfChanged(path string, want []byte, mode os.FileMode) error {
	if got, err := os.ReadFile(path); err == nil && sha256.Sum256(got) == sha256.Sum256(want) {
		return nil
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("stage %s: %w", filepath.Base(path), err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(want); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return fmt.Errorf("chmod %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("install %s: %w", filepath.Base(path), err)
	}
	return nil
}

// AddonDigest is the sha256 of the vendored addon, for `ao sim doctor`-style
// reporting and for the NOTICE to be checkable.
func AddonDigest() (string, error) {
	data, err := assets.ReadFile(addonAsset)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
