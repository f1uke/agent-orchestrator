// Package xcodeproj answers three questions about a worktree, and nothing else:
// is there an Xcode project in it, what can be built from that project, and
// where did a build put the app.
//
// It exists because three surfaces now need the same answers and they must not
// give different ones: the run bar above the terminal (is this an iOS project,
// what are its environments), `ao sim run` (build this scheme), and the desktop
// app's Open in Xcode menu, whose detection rule this package deliberately
// copies rather than reinvents.
//
// Everything here is read-only against the machine. Nothing in this package
// builds, installs or launches anything - `ao sim run` composes those from what
// this package tells it, so asking what a project contains can never change a
// project or a device.
package xcodeproj

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Binary is how xcodebuild is reached. Via xcrun, like simctl, so it resolves
// against the selected Xcode rather than whatever happens to be first on PATH.
const Binary = "xcrun"

// Sentinels. Each one means "no answer", never "a wrong answer": a scheme list
// that might be from a different project is worse than none.
var (
	// ErrNoProject: this directory has no top-level Xcode project. It is the
	// normal answer on most worktrees, not a failure - the run bar's whole
	// visibility test is this error.
	ErrNoProject = errors.New("xcodeproj: no .xcodeproj or .xcworkspace in this directory")
	// ErrNoSchemes: there is a project, and xcodebuild listed nothing buildable
	// in it.
	ErrNoSchemes = errors.New("xcodeproj: the project has no schemes")
	// ErrNoProduct: the build settings named no app bundle, so there is nothing
	// to install.
	ErrNoProduct = errors.New("xcodeproj: the build produced no .app bundle")
)

// Runner executes a command in a directory and returns its combined output,
// matching cli.Deps.CommandOutputInDir. Injected so this package is testable
// without Xcode.
type Runner func(ctx context.Context, dir, name string, args ...string) ([]byte, error)

// Kind is which of the two container formats a project is. They differ in one
// flag on every xcodebuild invocation, which is the only reason it is recorded.
type Kind string

const (
	// KindWorkspace is an .xcworkspace, built with -workspace.
	KindWorkspace Kind = "workspace"
	// KindProject is an .xcodeproj, built with -project.
	KindProject Kind = "project"
)

// Project is the Xcode container a worktree is built from.
type Project struct {
	// Kind decides the xcodebuild flag: -workspace or -project.
	Kind Kind `json:"kind"`
	// Name is the file name, e.g. "Nter.xcworkspace" - what a human calls it.
	Name string `json:"name"`
	// Path is absolute, so a caller running xcodebuild from elsewhere still
	// names the right container.
	Path string `json:"path"`
}

// Flag renders the project as the pair of arguments xcodebuild wants.
func (p Project) Flag() []string {
	if p.Kind == KindWorkspace {
		return []string{"-workspace", p.Path}
	}
	return []string{"-project", p.Path}
}

// Find locates the Xcode container at the root of dir.
//
// The rule is copied from the desktop app's Open in Xcode menu
// (frontend/src/main/open-in-targets.ts) on purpose: a worktree that offers
// "Open in Xcode" and a worktree that shows a run bar must be the same set, or
// the app contradicts itself about what kind of project this is.
//
//   - a top-level .xcworkspace wins over a top-level .xcodeproj, because that is
//     what a project with CocoaPods or SPM sub-projects must be opened as;
//   - only the top level is searched. A nested ios/App.xcodeproj is a monorepo,
//     and guessing which of several apps a monorepo means is the guess this
//     whole CLI refuses to make;
//   - ties inside one kind are broken by name, so the answer is stable rather
//     than whatever order the filesystem returned.
func Find(dir string) (Project, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Project{}, fmt.Errorf("read %s: %w", dir, err)
	}
	var workspaces, projects []string
	for _, entry := range entries {
		switch name := entry.Name(); {
		case strings.HasSuffix(name, ".xcworkspace"):
			workspaces = append(workspaces, name)
		case strings.HasSuffix(name, ".xcodeproj"):
			projects = append(projects, name)
		}
	}
	sort.Strings(workspaces)
	sort.Strings(projects)
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Project{}, fmt.Errorf("resolve %s: %w", dir, err)
	}
	if len(workspaces) > 0 {
		return Project{Kind: KindWorkspace, Name: workspaces[0], Path: filepath.Join(abs, workspaces[0])}, nil
	}
	if len(projects) > 0 {
		return Project{Kind: KindProject, Name: projects[0], Path: filepath.Join(abs, projects[0])}, nil
	}
	return Project{}, ErrNoProject
}

// listOutput is the shape of `xcodebuild -list -json`. A workspace reports its
// schemes under "workspace", a project under "project"; only one is ever
// populated, so both are read and whichever answered is used.
type listOutput struct {
	Workspace struct {
		Name    string   `json:"name"`
		Schemes []string `json:"schemes"`
	} `json:"workspace"`
	Project struct {
		Name    string   `json:"name"`
		Schemes []string `json:"schemes"`
	} `json:"project"`
}

// Schemes lists what can be built from a project.
//
// Schemes are the app environments this feature offers, and they are read live
// rather than configured: a project that gains a Staging scheme should offer it
// the next time somebody opens the run bar, without anybody editing AO.
//
// dir is where xcodebuild runs, which matters for a project whose schemes are
// resolved relative to the checkout.
func Schemes(ctx context.Context, run Runner, dir string, project Project) ([]string, error) {
	args := append([]string{"xcodebuild", "-list", "-json"}, project.Flag()...)
	out, err := run(ctx, dir, Binary, args...)
	if err != nil {
		return nil, fmt.Errorf("`xcodebuild -list` failed for %s: %w: %s", project.Name, err, tail(out))
	}
	var parsed listOutput
	if err := json.Unmarshal(trimToJSON(out), &parsed); err != nil {
		return nil, fmt.Errorf("could not read `xcodebuild -list -json` for %s: %w", project.Name, err)
	}
	schemes := parsed.Workspace.Schemes
	if len(schemes) == 0 {
		schemes = parsed.Project.Schemes
	}
	if len(schemes) == 0 {
		return nil, ErrNoSchemes
	}
	return own(dir, schemes), nil
}

// dependencyDirs are the checkout directories a dependency manager owns. A
// scheme defined inside one is not an app environment - see own().
var dependencyDirs = map[string]bool{
	"Pods":        true, // CocoaPods
	"Carthage":    true,
	"Checkouts":   true, // SPM, when a project vendors one
	".build":      true,
	"DerivedData": true,
	// Not a source of schemes, but the largest directory in any JS project -
	// and a React Native app is exactly the shape that has both. Walking it
	// would spend seconds to find nothing.
	"node_modules": true,
}

// own narrows xcodebuild's scheme list to the ones that belong to THIS app.
//
// 🗝 Measured on a real project, and it is not a nicety: a CocoaPods workspace
// reports **108 schemes** from `xcodebuild -list`, because every pod's generated
// project contributes one. 107 of them are AFNetworking, Alamofire, FBSDKCoreKit
// and friends. A picker offering those as "app environments" is not a picker,
// and the refusal that lists them is a wall of text - which is exactly what the
// first version of this printed.
//
// The rule is where the scheme is DEFINED, not what it is called: an .xcscheme
// under a dependency manager's checkout directory belongs to a dependency, and
// one anywhere else belongs to the project. Nothing here parses the scheme or
// guesses from its name.
//
// xcodebuild stays the source of truth - this only ever REMOVES from what it
// said, never adds - and a filter that removes everything is discarded. A
// project laid out in some way this does not anticipate then behaves exactly as
// it did before, which is the failure mode worth having: too many schemes is an
// annoyance, and none at all is a feature that does not work.
func own(dir string, schemes []string) []string {
	defined := definedOutsideDependencies(dir)
	if len(defined) == 0 {
		return schemes
	}
	kept := make([]string, 0, len(schemes))
	for _, scheme := range schemes {
		if defined[scheme] {
			kept = append(kept, scheme)
		}
	}
	if len(kept) == 0 {
		return schemes
	}
	return kept
}

// definedOutsideDependencies is every scheme with an .xcscheme file under dir
// that is not inside a dependency manager's checkout. Empty when the tree
// cannot be walked, which own() reads as "no opinion".
func definedOutsideDependencies(dir string) map[string]bool {
	found := map[string]bool{}
	// Depth is bounded because a scheme lives at a known depth below its project
	// container and an unbounded walk is seconds this answer does not have.
	// Eight, not the five a top-level project needs: advisor-ios-app already
	// keeps one at `FNCore/FNCoreProject/FNCore.xcodeproj/xcshareddata/xcschemes`,
	// and a bound that only just fits the projects in front of me is a bound
	// that silently drops the next one.
	const maxDepth = 8
	root := filepath.Clean(dir)
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // intentional: an unreadable subtree is not an answer, it is one fewer place to look
		}
		if !entry.IsDir() {
			if strings.HasSuffix(path, ".xcscheme") && strings.Contains(path, "xcschemes") {
				found[strings.TrimSuffix(entry.Name(), ".xcscheme")] = true
			}
			return nil
		}
		name := entry.Name()
		if path == root {
			return nil
		}
		if dependencyDirs[name] || (strings.HasPrefix(name, ".") && name != ".build") {
			return fs.SkipDir
		}
		if depth(root, path) >= maxDepth {
			return fs.SkipDir
		}
		return nil
	})
	return found
}

func depth(root, path string) int {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return 0
	}
	return len(strings.Split(rel, string(filepath.Separator)))
}

// GenericSimulatorDestination is what a build for the Simulator is aimed at.
//
// 🗝 It names no device, and that is the point rather than a convenience.
// `xcodebuild -destination id=<udid>` consults no AO lease, so pointing a build
// at a specific simulator is exactly the call that can walk over a device
// another session is driving. A generic destination cannot: it produces the
// same .app for every simulator, and only the install and the launch - both of
// which go through the lease - ever name a device.
const GenericSimulatorDestination = "generic/platform=iOS Simulator"

// BuildArgs is the xcodebuild invocation that produces an installable app.
func BuildArgs(project Project, scheme, configuration string) []string {
	return append(append([]string{"xcodebuild"}, project.Flag()...),
		"-scheme", scheme,
		"-configuration", configuration,
		"-destination", GenericSimulatorDestination,
		// The simulator has no code signing, and a project whose team is not
		// configured on this machine would otherwise fail at the very end of a
		// build that was otherwise fine.
		"CODE_SIGNING_ALLOWED=NO",
		"build",
	)
}

// settingsOutput is the shape of `xcodebuild -showBuildSettings -json`: one
// entry per target, each with a flat map of settings.
type settingsOutput struct {
	Action        string            `json:"action"`
	Target        string            `json:"target"`
	BuildSettings map[string]string `json:"buildSettings"`
}

// ProductPath asks the build system where the app it just built is.
//
// It is a second xcodebuild invocation rather than a path assembled from the
// configuration name, because the answer depends on things AO does not know:
// a custom DerivedData location, a per-project CONFIGURATION_BUILD_DIR, a
// product name that is not the scheme's. Guessing produces a path that is
// usually right, and an install that silently puts yesterday's build on the
// device the rest of the time.
func ProductPath(ctx context.Context, run Runner, dir string, project Project, scheme, configuration string) (string, error) {
	args := append(append([]string{"xcodebuild", "-showBuildSettings", "-json"}, project.Flag()...),
		"-scheme", scheme,
		"-configuration", configuration,
		"-destination", GenericSimulatorDestination,
	)
	out, err := run(ctx, dir, Binary, args...)
	if err != nil {
		return "", fmt.Errorf("`xcodebuild -showBuildSettings` failed for %s: %w: %s", scheme, err, tail(out))
	}
	var parsed []settingsOutput
	if err := json.Unmarshal(trimToJSON(out), &parsed); err != nil {
		return "", fmt.Errorf("could not read the build settings for %s: %w", scheme, err)
	}
	for _, entry := range parsed {
		dir, product := entry.BuildSettings["TARGET_BUILD_DIR"], entry.BuildSettings["FULL_PRODUCT_NAME"]
		if dir == "" || !strings.HasSuffix(product, ".app") {
			continue
		}
		return filepath.Join(dir, product), nil
	}
	return "", ErrNoProduct
}

// trimToJSON drops anything xcodebuild printed before its JSON.
//
// ⚠ It looks for a line that BEGINS the document, not for the first brace
// anywhere. The difference is a real failure, found by running it: the tools
// xcodebuild spawns log through os_log, whose prefix carries a bracket -
//
//	2026-09-18 13:03:02.285 appintentsnltrainingprocessor[80601:415773] Parsing options
//
// - and a scan for the first `[` starts the document inside that pid, which
// fails to parse with "invalid character ':' after array element". The JSON
// xcodebuild emits always starts its own line, so that is what is looked for.
func trimToJSON(out []byte) []byte {
	rest := out
	for len(rest) > 0 {
		line := rest
		if end := bytes.IndexByte(rest, '\n'); end >= 0 {
			line = rest[:end]
		}
		if start := bytes.TrimLeft(line, " \t"); len(start) > 0 && (start[0] == '{' || start[0] == '[') {
			// The document begins here, indentation and all.
			return rest[len(line)-len(start):]
		}
		rest = rest[len(line):]
		if len(rest) > 0 {
			rest = rest[1:] // the newline
		}
	}
	return out
}

// tail is the last of what a failing xcodebuild said, for an error message.
// The whole output of a failed build is thousands of lines and has already
// been streamed to the terminal; repeating it in the error would bury the one
// sentence that says what to do.
func tail(out []byte) string {
	const keep = 12
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) > keep {
		lines = lines[len(lines)-keep:]
	}
	return strings.Join(lines, "\n")
}
