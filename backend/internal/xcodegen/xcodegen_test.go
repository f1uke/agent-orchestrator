package xcodegen

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// fakeXcodegen behaves like xcodegen where the stale check can tell: `cache`
// writes the spec's text and its source file list, `generate` rewrites the
// project file.
func fakeXcodegen(t *testing.T, calls *[]string) Exec {
	t.Helper()
	return func(_ context.Context, dir, _ string, args ...string) ([]byte, int, error) {
		*calls = append(*calls, args[0]+" "+filepath.Base(dir))
		spec, err := os.ReadFile(filepath.Join(dir, SpecFile))
		if err != nil {
			return []byte("no spec"), 1, err
		}
		switch args[0] {
		case "cache":
			var files []string
			_ = filepath.WalkDir(filepath.Join(dir, "Sources"), func(path string, d os.DirEntry, _ error) error {
				if d != nil && !d.IsDir() {
					files = append(files, path)
				}
				return nil
			})
			sort.Strings(files)
			body := "# XCODEGEN VERSION\n2.45.3\n\n# SPEC\n{\"name\":\"Demo\",\"text\":" + quote(string(spec)) + "}\n\n# FILES\n" + strings.Join(files, "\n")
			return nil, 0, os.WriteFile(args[2], []byte(body), 0o600)
		case "generate":
			if strings.Contains(string(spec), "broken") {
				return []byte("Spec validation error"), 1, nil
			}
			project := filepath.Join(dir, "Demo.xcodeproj")
			if err := os.MkdirAll(project, 0o750); err != nil {
				return nil, -1, err
			}
			return []byte("Created project"), 0, os.WriteFile(filepath.Join(project, "project.pbxproj"), []byte(time.Now().String()), 0o600)
		}
		return nil, 1, nil
	}
}

func quote(s string) string {
	return "\"" + strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n").Replace(s) + "\""
}

func newGenerator(t *testing.T) (*Generator, *[]string) {
	t.Helper()
	var calls []string
	g := New(filepath.Join(t.TempDir(), "xcodegen.json"),
		WithExec(fakeXcodegen(t, &calls)),
		WithBinary(func() (string, error) { return "/bin/xcodegen", nil }))
	return g, &calls
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// later moves a file's mtime forward, so a test never sleeps for the clock.
func later(t *testing.T, path string, by time.Duration) {
	t.Helper()
	at := time.Now().Add(by)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func project(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "App", SpecFile), "name: Demo\n")
	write(t, filepath.Join(root, "App", "Sources", "A.swift"), "struct A {}\n")
	return root
}

func staleOf(t *testing.T, g *Generator, root string) bool {
	t.Helper()
	_, specs := g.Specs(context.Background(), root)
	if len(specs) != 1 {
		t.Fatalf("specs = %+v, want one", specs)
	}
	return specs[0].Stale
}

func TestSpecsSaysANeverGeneratedProjectIsStale(t *testing.T) {
	g, _ := newGenerator(t)
	if !staleOf(t, g, project(t)) {
		t.Fatal("a spec with no generated project has to be generated")
	}
}

func TestSpecsIsFreshRightAfterAGenerate(t *testing.T) {
	g, _ := newGenerator(t)
	root := project(t)
	if res := g.Generate(context.Background(), root); res.Status != StatusRan || !res.Results[0].OK || res.Results[0].Dir != "App" {
		t.Fatalf("generate = %+v", res)
	}
	if staleOf(t, g, root) {
		t.Fatal("nothing changed since the generate")
	}
}

func TestSpecsSeesAnEditedSpec(t *testing.T) {
	g, _ := newGenerator(t)
	root := project(t)
	g.Generate(context.Background(), root)
	write(t, filepath.Join(root, "App", SpecFile), "name: Demo\nsettings: {}\n")
	if !staleOf(t, g, root) {
		t.Fatal("project.yml changed after the generate")
	}
}

func TestSpecsSeesAnAddedSourceFile(t *testing.T) {
	g, _ := newGenerator(t)
	root := project(t)
	g.Generate(context.Background(), root)
	write(t, filepath.Join(root, "App", "Sources", "B.swift"), "struct B {}\n")
	if !staleOf(t, g, root) {
		t.Fatal("a file xcodegen has not listed is invisible to the build until it generates")
	}
}

func TestSpecsIgnoresAnEditInsideASourceFile(t *testing.T) {
	g, _ := newGenerator(t)
	root := project(t)
	g.Generate(context.Background(), root)
	write(t, filepath.Join(root, "App", "Sources", "A.swift"), "struct A { let x = 1 }\n")
	if staleOf(t, g, root) {
		t.Fatal("editing a file the project already lists needs no generate")
	}
}

func TestSpecsTrustsAGenerateRunInTheTerminal(t *testing.T) {
	g, _ := newGenerator(t)
	root := project(t)
	g.Generate(context.Background(), root)
	write(t, filepath.Join(root, "App", SpecFile), "name: Demo\nsettings: {}\n")
	if !staleOf(t, g, root) {
		t.Fatal("precondition: the edit is seen")
	}
	var none []string
	if _, _, err := fakeXcodegen(t, &none)(context.Background(), filepath.Join(root, "App"), "xcodegen", "generate"); err != nil {
		t.Fatal(err)
	}
	later(t, filepath.Join(root, "App", "Demo.xcodeproj", "project.pbxproj"), time.Minute)
	if staleOf(t, g, root) {
		t.Fatal("the project was regenerated outside AO, so it matches the spec again")
	}
}

func TestSpecsSeesAnIncludedSpecChangedBeforeAOFirstLooked(t *testing.T) {
	g, _ := newGenerator(t)
	root := project(t)
	write(t, filepath.Join(root, "App", SpecFile), "name: Demo\ninclude:\n  - path: shared/targets.yml\n")
	write(t, filepath.Join(root, "App", "shared", "targets.yml"), "targets: {}\n")
	var none []string
	if _, _, err := fakeXcodegen(t, &none)(context.Background(), filepath.Join(root, "App"), "xcodegen", "generate"); err != nil {
		t.Fatal(err)
	}
	later(t, filepath.Join(root, "App", "shared", "targets.yml"), time.Minute)
	if !staleOf(t, g, root) {
		t.Fatal("an included spec newer than the project is a spec the project has not seen")
	}
}

func TestGenerateReportsAFailedSpecAndRecordsNothingForIt(t *testing.T) {
	g, _ := newGenerator(t)
	root := project(t)
	write(t, filepath.Join(root, "App", SpecFile), "name: Demo\nbroken: yes\n")
	res := g.Generate(context.Background(), root)
	if res.Results[0].OK || *res.Results[0].ExitCode != 1 || res.Results[0].Output != "Spec validation error" {
		t.Fatalf("result = %+v, want the failure and xcodegen's words", res.Results[0])
	}
	if !staleOf(t, g, root) {
		t.Fatal("a spec that failed to generate is still ahead of its project")
	}
}

func TestGenerateWithoutXcodegenSaysSo(t *testing.T) {
	g := New("", WithBinary(func() (string, error) { return "", exec.ErrNotFound }))
	if res := g.Generate(context.Background(), project(t)); res.Status != StatusNotInstalled {
		t.Fatalf("status = %q", res.Status)
	}
	installed, specs := g.Specs(context.Background(), project(t))
	if installed || len(specs) != 1 || specs[0].Stale {
		t.Fatalf("installed=%v specs=%+v, want the spec listed and nothing called stale", installed, specs)
	}
}

func TestFindSpecDirsSkipsDependenciesAndBuildOutput(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{".", "App", "Pods/Lib", "node_modules/x", ".git/y", "derivedDataPath/z", "Modules/Core"} {
		write(t, filepath.Join(root, dir, SpecFile), "name: X\n")
	}
	var rel []string
	for _, dir := range FindSpecDirs(root) {
		r, _ := filepath.Rel(root, dir)
		rel = append(rel, r)
	}
	if strings.Join(rel, ",") != ".,App,Modules/Core" {
		t.Fatalf("found %v", rel)
	}
}

func TestStaleAgainstTheRealXcodegen(t *testing.T) {
	binary, err := lookPath()
	if err != nil {
		t.Skip("xcodegen is not installed")
	}
	root := t.TempDir()
	write(t, filepath.Join(root, SpecFile), "name: Probe\ntargets:\n  Probe:\n    type: framework\n    platform: iOS\n    deploymentTarget: \"17.0\"\n    sources: [Sources]\n")
	write(t, filepath.Join(root, "Sources", "A.swift"), "struct A {}\n")
	g := New(filepath.Join(t.TempDir(), "xcodegen.json"), WithBinary(func() (string, error) { return binary, nil }))
	if res := g.Generate(context.Background(), root); res.Status != StatusRan || !res.Results[0].OK {
		t.Fatalf("generate = %+v", res)
	}
	if staleOf(t, g, root) {
		t.Fatal("fresh after a real generate")
	}
	write(t, filepath.Join(root, "Sources", "B.swift"), "struct B {}\n")
	if !staleOf(t, g, root) {
		t.Fatal("the real cache key lists source files, so an added file is stale")
	}
}
