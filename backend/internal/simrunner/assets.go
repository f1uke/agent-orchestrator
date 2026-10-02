package simrunner

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// The runner ships as SOURCE, embedded in the binary, and is built on the
// machine that runs it. A prebuilt XCTest bundle is tied to the Xcode that
// built it; building here ties it to the Xcode that will run it, takes about
// six seconds once per AO build and Xcode version, and leaves nothing opaque
// in the repository.
//
//go:embed all:xctest
var sources embed.FS

// sourceRoot is the embedded directory, and projectName the project in it.
const (
	sourceRoot  = "xctest"
	projectName = "AORunner.xcodeproj"
	schemeName  = "AORunner"
)

// RunnerBundleID is the test runner app XCTest installs on the device. It is
// what `simctl terminate` stops when the process that launched it is gone.
const RunnerBundleID = "dev.aoagents.simrunner.uitests.xctrunner"

// sourceHash names this AO build's runner sources, so two AO builds never
// share a build directory and a changed source is always rebuilt.
func sourceHash() (string, error) {
	var paths []string
	err := fs.WalkDir(sources, sourceRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, path := range paths {
		body, err := sources.ReadFile(path)
		if err != nil {
			return "", err
		}
		_, _ = fmt.Fprintf(h, "%s\x00%d\x00", path, len(body))
		_, _ = h.Write(body)
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

// installSources writes the embedded sources under dir, which is named by
// their hash, and returns the project path. A directory that already exists
// is complete: it is only ever made by renaming a finished one into place.
func installSources(dir string) (string, error) {
	project := filepath.Join(dir, projectName)
	if _, err := os.Stat(project); err == nil {
		return project, nil
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o750); err != nil {
		return "", fmt.Errorf("create the runner source directory: %w", err)
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), ".src-")
	if err != nil {
		return "", fmt.Errorf("create the runner source directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	err = fs.WalkDir(sources, sourceRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return err
		}
		target := filepath.Join(tmp, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		body, err := sources.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, body, 0o600)
	})
	if err != nil {
		return "", fmt.Errorf("write the runner sources: %w", err)
	}
	if err := os.Rename(tmp, dir); err != nil && !os.IsExist(err) {
		if _, statErr := os.Stat(project); statErr != nil {
			return "", fmt.Errorf("install the runner sources: %w", err)
		}
	}
	return project, nil
}
