package simrunner

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// The runner is built from these on the user's machine, so a file missing
// from the embed is a build that fails there and nowhere else.
func TestInstallSources_WritesABuildableProject(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "src", "x")
	project, err := installSources(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		"AORunner.xcodeproj/project.pbxproj",
		"AORunner.xcodeproj/xcshareddata/xcschemes/AORunner.xcscheme",
		"UITests/RunnerTests.swift",
		"Host/App.swift",
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("%s is not in the installed sources: %v", rel, err)
		}
	}
	if project != filepath.Join(dir, projectName) {
		t.Fatalf("project = %s", project)
	}
	// A second install is a no-op on a complete directory.
	if again, err := installSources(dir); err != nil || again != project {
		t.Fatalf("reinstall = %s, %v", again, err)
	}
}

func TestSourceHash_IsStable(t *testing.T) {
	a, err := sourceHash()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := sourceHash()
	if a == "" || a != b {
		t.Fatalf("hash %q then %q", a, b)
	}
}

// A Swift file in the sources that the generated project does not list is
// never compiled, and the runner fails on the user's machine with a missing
// symbol. project.yml is the source of truth: run `xcodegen generate` in
// xctest/ after adding a file, and commit the project too.
func TestProject_CompilesEverySwiftSource(t *testing.T) {
	project, err := sources.ReadFile(path.Join(sourceRoot, projectName, "project.pbxproj"))
	if err != nil {
		t.Fatal(err)
	}
	err = fs.WalkDir(sources, sourceRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Ext(p) != ".swift" {
			return err
		}
		if !strings.Contains(string(project), path.Base(p)+" in Sources") {
			t.Errorf("%s is not in %s - run `xcodegen generate` in xctest/", p, projectName)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
