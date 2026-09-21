package xcodeproj

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mkdirs(t *testing.T, dir string, names ...string) string {
	t.Helper()
	for _, name := range names {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestFind_PrefersTheWorkspaceOverTheProject(t *testing.T) {
	// A project with CocoaPods or SPM sub-projects MUST be opened as its
	// workspace; building the bare .xcodeproj misses half of it.
	dir := mkdirs(t, t.TempDir(), "MyApp.xcodeproj", "MyApp.xcworkspace", "Pods")

	project, err := Find(dir)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if project.Kind != KindWorkspace || project.Name != "MyApp.xcworkspace" {
		t.Fatalf("found %+v, want the workspace", project)
	}
	if !filepath.IsAbs(project.Path) {
		t.Fatalf("the path must be absolute so xcodebuild can be run from anywhere: %s", project.Path)
	}
	if got := strings.Join(project.Flag(), " "); got != "-workspace "+project.Path {
		t.Fatalf("flag %q, want -workspace", got)
	}
}

func TestFind_TakesTheProjectWhenThereIsNoWorkspace(t *testing.T) {
	dir := mkdirs(t, t.TempDir(), "Weather.xcodeproj")

	project, err := Find(dir)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if project.Kind != KindProject || project.Name != "Weather.xcodeproj" {
		t.Fatalf("found %+v, want the project", project)
	}
	if got := strings.Join(project.Flag(), " "); got != "-project "+project.Path {
		t.Fatalf("flag %q, want -project", got)
	}
}

// A monorepo keeping its app in ios/ is not an iOS project at this level, and
// guessing which of several apps it means is exactly the guess this refuses.
func TestFind_IgnoresANestedProject(t *testing.T) {
	dir := mkdirs(t, t.TempDir(), filepath.Join("ios", "App.xcodeproj"), "backend")

	if _, err := Find(dir); !errors.Is(err, ErrNoProject) {
		t.Fatalf("find: %v, want ErrNoProject", err)
	}
}

func TestFind_IsStableWhenSeveralProjectsSitSideBySide(t *testing.T) {
	dir := mkdirs(t, t.TempDir(), "Zebra.xcodeproj", "Alpha.xcodeproj")

	for range 5 {
		project, err := Find(dir)
		if err != nil {
			t.Fatalf("find: %v", err)
		}
		if project.Name != "Alpha.xcodeproj" {
			t.Fatalf("found %s; the answer must not depend on directory order", project.Name)
		}
	}
}

func runner(out string, err error) (Runner, *[][]string) {
	var calls [][]string
	return func(_ context.Context, dir, name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{dir, name}, args...))
		return []byte(out), err
	}, &calls
}

func TestSchemes_ReadsThemLiveFromTheWorkspace(t *testing.T) {
	run, calls := runner(`{"workspace":{"name":"Nter","schemes":["Nter","NterDev","NterStaging"]}}`, nil)

	schemes, err := Schemes(context.Background(), run, "/w", Project{Kind: KindWorkspace, Name: "Nter.xcworkspace", Path: "/w/Nter.xcworkspace"})
	if err != nil {
		t.Fatalf("schemes: %v", err)
	}
	if strings.Join(schemes, ",") != "Nter,NterDev,NterStaging" {
		t.Fatalf("schemes %v", schemes)
	}
	ran := strings.Join((*calls)[0], " ")
	if !strings.Contains(ran, "-list -json -workspace /w/Nter.xcworkspace") {
		t.Fatalf("ran %q", ran)
	}
}

func TestSchemes_ReadsAProjectsSchemesToo(t *testing.T) {
	run, _ := runner(`{"project":{"name":"Weather","schemes":["Weather"]}}`, nil)

	schemes, err := Schemes(context.Background(), run, "/w", Project{Kind: KindProject, Path: "/w/Weather.xcodeproj"})
	if err != nil {
		t.Fatalf("schemes: %v", err)
	}
	if len(schemes) != 1 || schemes[0] != "Weather" {
		t.Fatalf("schemes %v", schemes)
	}
}

// xcodebuild warns about unrelated things before it prints its JSON, and the
// combined output those land in is what a plain Unmarshal chokes on.
func TestSchemes_SurvivesXcodebuildsPreamble(t *testing.T) {
	run, _ := runner("2026-09-18 10:00:00 note: stale module cache\n"+
		`{"project":{"schemes":["Weather"]}}`, nil)

	schemes, err := Schemes(context.Background(), run, "/w", Project{Kind: KindProject})
	if err != nil {
		t.Fatalf("schemes: %v", err)
	}
	if len(schemes) != 1 {
		t.Fatalf("schemes %v", schemes)
	}
}

func TestSchemes_EmptyIsItsOwnAnswer(t *testing.T) {
	run, _ := runner(`{"project":{"schemes":[]}}`, nil)

	if _, err := Schemes(context.Background(), run, "/w", Project{Kind: KindProject}); !errors.Is(err, ErrNoSchemes) {
		t.Fatalf("schemes: %v, want ErrNoSchemes", err)
	}
}

// The build must never name a device: `xcodebuild -destination id=<udid>`
// consults no lease, so a build aimed at one simulator is the call that can
// walk over a session driving it.
func TestBuildArgs_NamesNoDevice(t *testing.T) {
	args := BuildArgs(Project{Kind: KindWorkspace, Path: "/w/Nter.xcworkspace"}, "NterDev", "Debug")

	line := strings.Join(args, " ")
	if !strings.Contains(line, "-destination "+GenericSimulatorDestination) {
		t.Fatalf("build must target the generic simulator: %q", line)
	}
	if strings.Contains(line, "id=") {
		t.Fatalf("the build named a device, which is the whole thing this avoids: %q", line)
	}
	for _, want := range []string{"-workspace /w/Nter.xcworkspace", "-scheme NterDev", "-configuration Debug", "build"} {
		if !strings.Contains(line, want) {
			t.Fatalf("build args missing %q: %q", want, line)
		}
	}
}

func TestProductPath_AsksTheBuildSystemWhereTheAppIs(t *testing.T) {
	run, _ := runner(`[{"action":"build","target":"NterDev","buildSettings":{`+
		`"TARGET_BUILD_DIR":"/dd/Build/Products/Debug-iphonesimulator",`+
		`"FULL_PRODUCT_NAME":"Nter.app"}}]`, nil)

	app, err := ProductPath(context.Background(), run, "/w", Project{Kind: KindProject}, "NterDev", "Debug")
	if err != nil {
		t.Fatalf("product path: %v", err)
	}
	if app != "/dd/Build/Products/Debug-iphonesimulator/Nter.app" {
		t.Fatalf("app %q", app)
	}
}

// A scheme whose only target is a framework or a test bundle builds fine and
// produces nothing installable. Saying so beats installing whatever .app was
// left in the products directory by a previous build.
func TestProductPath_SaysSoWhenNothingInstallableWasBuilt(t *testing.T) {
	run, _ := runner(`[{"buildSettings":{"TARGET_BUILD_DIR":"/dd","FULL_PRODUCT_NAME":"NterKit.framework"}}]`, nil)

	if _, err := ProductPath(context.Background(), run, "/w", Project{}, "NterKit", "Debug"); !errors.Is(err, ErrNoProduct) {
		t.Fatalf("product path: %v, want ErrNoProduct", err)
	}
}

// The failure this rule exists for, in the exact shape it arrived: the tools
// xcodebuild spawns log through os_log, and that prefix carries a bracket. A
// scan for the first `[` anywhere starts the document inside a pid and fails
// with "invalid character ':' after array element".
func TestProductPath_SurvivesAnOsLogPrefixBeforeTheJSON(t *testing.T) {
	run, _ := runner("2026-09-18 13:03:02.285 appintentsnltrainingprocessor[80601:415773] Parsing options\n"+
		"2026-09-18 13:03:02.285 appintentsnltrainingprocessor[80601:415773] No AppShortcuts found - Skipping.\n"+
		`[{"buildSettings":{"TARGET_BUILD_DIR":"/dd","FULL_PRODUCT_NAME":"Probe.app"}}]`, nil)

	app, err := ProductPath(context.Background(), run, "/w", Project{}, "Probe", "Debug")
	if err != nil {
		t.Fatalf("product path: %v", err)
	}
	if app != "/dd/Probe.app" {
		t.Fatalf("app %q", app)
	}
}

// A CocoaPods workspace reports every pod as a scheme - 108 of them on the real
// project this was measured against. An "app environment" picker offering
// AFNetworking is not a picker.
func TestSchemes_LeavesOutSchemesThatBelongToDependencies(t *testing.T) {
	dir := t.TempDir()
	scheme := func(project, name string) {
		t.Helper()
		at := filepath.Join(dir, project, "xcshareddata", "xcschemes")
		if err := os.MkdirAll(at, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(at, name+".xcscheme"), []byte("<Scheme/>"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	scheme("Nter/NterApp.xcodeproj", "NterApp")
	scheme("Pods/Pods.xcodeproj", "Alamofire")
	scheme("Pods/Pods.xcodeproj", "AFNetworking")
	run, _ := runner(`{"workspace":{"schemes":["AFNetworking","Alamofire","NterApp"]}}`, nil)

	schemes, err := Schemes(context.Background(), run, dir, Project{Kind: KindWorkspace})
	if err != nil {
		t.Fatalf("schemes: %v", err)
	}
	if strings.Join(schemes, ",") != "NterApp" {
		t.Fatalf("schemes %v, want the app's own only", schemes)
	}
}

// xcodebuild stays the source of truth: the filter only ever REMOVES from what
// it said. A scheme file on disk that xcodebuild did not list is not buildable.
func TestSchemes_NeverAddsASchemeXcodebuildDidNotList(t *testing.T) {
	dir := t.TempDir()
	at := filepath.Join(dir, "App.xcodeproj", "xcshareddata", "xcschemes")
	if err := os.MkdirAll(at, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"App", "Retired"} {
		if err := os.WriteFile(filepath.Join(at, name+".xcscheme"), []byte("<Scheme/>"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run, _ := runner(`{"project":{"schemes":["App"]}}`, nil)

	schemes, err := Schemes(context.Background(), run, dir, Project{Kind: KindProject})
	if err != nil {
		t.Fatalf("schemes: %v", err)
	}
	if strings.Join(schemes, ",") != "App" {
		t.Fatalf("schemes %v", schemes)
	}
}

// A project laid out in a way the filter does not anticipate must behave
// exactly as it did before it existed. Too many schemes is an annoyance; none
// at all is a feature that does not work.
func TestSchemes_FallsBackToTheWholeListWhenTheFilterFindsNothing(t *testing.T) {
	run, _ := runner(`{"project":{"schemes":["App","AppTests"]}}`, nil)

	schemes, err := Schemes(context.Background(), run, t.TempDir(), Project{Kind: KindProject})
	if err != nil {
		t.Fatalf("schemes: %v", err)
	}
	if strings.Join(schemes, ",") != "App,AppTests" {
		t.Fatalf("schemes %v", schemes)
	}
}

// workspaceAt writes a .xcworkspace whose contents.xcworkspacedata references
// the given locations, in order - the same file Xcode writes.
func workspaceAt(t *testing.T, dir, name string, locations ...string) Project {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(path, 0o750); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<Workspace version = \"1.0\">\n")
	for _, location := range locations {
		fmt.Fprintf(&b, "   <FileRef location = %q>\n   </FileRef>\n", location)
	}
	b.WriteString("</Workspace>\n")
	if err := os.WriteFile(filepath.Join(path, "contents.xcworkspacedata"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return Project{Kind: KindWorkspace, Name: name, Path: path}
}

// byProject answers each `-list` per project path, so a test can give the app
// and a pod different configuration lists and see which one is read.
func byProject(t *testing.T, answers map[string]string) (Runner, *[]string) {
	t.Helper()
	var asked []string
	return func(_ context.Context, _, _ string, args ...string) ([]byte, error) {
		path := args[len(args)-1]
		asked = append(asked, filepath.Base(path))
		out, ok := answers[filepath.Base(path)]
		if !ok {
			return nil, errors.New("exit status 66")
		}
		return []byte(out), nil
	}, &asked
}

// 🗝 The case this whole change exists for: a real project whose configurations
// do not include Debug. nter-ios-app lists exactly these.
func TestConfigurations_ReadsThemFromTheProjectInsideAWorkspace(t *testing.T) {
	dir := t.TempDir()
	mkdirs(t, dir, filepath.Join("NterApp", "NterApp.xcodeproj"), filepath.Join("Pods", "Pods.xcodeproj"))
	workspace := workspaceAt(t, dir, "NterWorkspace.xcworkspace",
		"group:NterApp/NterApp.xcodeproj", "group:Pods/Pods.xcodeproj")
	run, asked := byProject(t, map[string]string{
		"NterApp.xcodeproj": `{"project":{"name":"NterApp","configurations":["Dev","Mock-api","Production","Release","UAT"],"schemes":["NterApp"]}}`,
		"Pods.xcodeproj":    `{"project":{"name":"Pods","configurations":["Debug","Release"],"schemes":[]}}`,
	})

	configurations, err := Configurations(context.Background(), run, dir, workspace)
	if err != nil {
		t.Fatalf("configurations: %v", err)
	}
	if strings.Join(configurations, ",") != "Dev,Mock-api,Production,Release,UAT" {
		t.Fatalf("configurations %v, want the app's own", configurations)
	}
	// The Pods project mirrors the app's configurations and adds CocoaPods' own.
	// Reading it - or merging it in - would offer a configuration the app cannot
	// be built with, which is the doomed build this feature exists to prevent.
	if strings.Join(*asked, ",") != "NterApp.xcodeproj" {
		t.Fatalf("asked %v; the first project that answers is the answer, and Pods is never asked", *asked)
	}
}

// A workspace lists its schemes and nothing else, so asking the WORKSPACE for
// configurations returns an empty list however the project is laid out.
func TestConfigurations_NeverAsksTheWorkspaceItself(t *testing.T) {
	dir := t.TempDir()
	mkdirs(t, dir, filepath.Join("App", "App.xcodeproj"))
	workspace := workspaceAt(t, dir, "App.xcworkspace", "group:App/App.xcodeproj")
	run, asked := byProject(t, map[string]string{
		"App.xcodeproj": `{"project":{"configurations":["Debug","Release"]}}`,
	})

	if _, err := Configurations(context.Background(), run, dir, workspace); err != nil {
		t.Fatalf("configurations: %v", err)
	}
	for _, target := range *asked {
		if strings.HasSuffix(target, ".xcworkspace") {
			t.Fatalf("asked %v; a workspace listing has no configurations in it", *asked)
		}
	}
}

func TestConfigurations_ReadsAPlainProjectDirectly(t *testing.T) {
	run, _ := runner(`{"project":{"name":"Weather","configurations":["Debug","Release"],"schemes":["Weather"]}}`, nil)

	configurations, err := Configurations(context.Background(), run, "/w", Project{Kind: KindProject, Path: "/w/Weather.xcodeproj"})
	if err != nil {
		t.Fatalf("configurations: %v", err)
	}
	if strings.Join(configurations, ",") != "Debug,Release" {
		t.Fatalf("configurations %v", configurations)
	}
}

// A ref inside Pods/ is CocoaPods', and a workspace that holds nothing else has
// no configuration list to give. Answering "Debug, Release" from Pods.xcodeproj
// would be a confident wrong answer.
func TestConfigurations_IgnoresProjectsADependencyManagerOwns(t *testing.T) {
	dir := t.TempDir()
	mkdirs(t, dir, filepath.Join("Pods", "Pods.xcodeproj"))
	workspace := workspaceAt(t, dir, "App.xcworkspace", "group:Pods/Pods.xcodeproj")
	run, asked := byProject(t, map[string]string{
		"Pods.xcodeproj": `{"project":{"configurations":["Debug","Release"]}}`,
	})

	if _, err := Configurations(context.Background(), run, dir, workspace); !errors.Is(err, ErrNoConfigurations) {
		t.Fatalf("configurations: %v, want ErrNoConfigurations", err)
	}
	if len(*asked) != 0 {
		t.Fatalf("asked %v, want nothing", *asked)
	}
}

// group: is relative to the enclosing Group, and a Group can nest. Getting this
// wrong resolves a path that does not exist, which looks exactly like a project
// with no configurations.
func TestConfigurations_ResolvesARefNestedInAGroup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "App.xcworkspace")
	if err := os.MkdirAll(path, 0o750); err != nil {
		t.Fatal(err)
	}
	contents := `<?xml version="1.0" encoding="UTF-8"?>
<Workspace version = "1.0">
   <Group location = "group:apps" name = "apps">
      <FileRef location = "group:Weather/Weather.xcodeproj"></FileRef>
   </Group>
</Workspace>`
	if err := os.WriteFile(filepath.Join(path, "contents.xcworkspacedata"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	var got string
	run := Runner(func(_ context.Context, _, _ string, args ...string) ([]byte, error) {
		got = args[len(args)-1]
		return []byte(`{"project":{"configurations":["Debug"]}}`), nil
	})

	if _, err := Configurations(context.Background(), run, dir, Project{Kind: KindWorkspace, Name: "App.xcworkspace", Path: path}); err != nil {
		t.Fatalf("configurations: %v", err)
	}
	if want := filepath.Join(dir, "apps", "Weather", "Weather.xcodeproj"); got != want {
		t.Fatalf("listed %q, want %q", got, want)
	}
}

// "No answer" and "no configurations" are the same result here, and neither is
// permission to fall back to Debug: every Xcode project defines configurations,
// so an empty list means the question failed.
func TestConfigurations_EmptyIsItsOwnAnswer(t *testing.T) {
	run, _ := runner(`{"project":{"configurations":[]}}`, nil)

	if _, err := Configurations(context.Background(), run, "/w", Project{Kind: KindProject}); !errors.Is(err, ErrNoConfigurations) {
		t.Fatalf("configurations: %v, want ErrNoConfigurations", err)
	}
}

// A generic simulator destination means EVERY architecture the SDK supports, and
// on an Apple Silicon Mac that includes an x86_64 slice no simulator here runs.
// Measured on nter-ios-app: the whole project compiles and then the x86_64 link
// fails against an arm64-only xcframework, which is a doomed build dressed up as
// a compiler error.
func TestBuildArgs_BuildsOnlyThisMachinesArchitecture(t *testing.T) {
	line := strings.Join(BuildArgs(Project{Kind: KindWorkspace, Path: "/w/Nter.xcworkspace"}, "NterApp", "Dev"), " ")

	// ARCHS, not ONLY_ACTIVE_ARCH: a target that sets ONLY_ACTIVE_ARCH=NO of its
	// own - which CocoaPods writes into the Pods project - beats the flag, and
	// the x86_64 slice comes back.
	if !strings.Contains(line, "ARCHS=$(NATIVE_ARCH_ACTUAL)") {
		t.Fatalf("the build must not ask for architectures this machine cannot run: %q", line)
	}
}

// The state every freshly spawned worker starts in, and the two states it must
// not be confused with: a project that uses no CocoaPods at all, and one whose
// pods are already installed.
func TestPodInstallPending(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []string
		files   []string
		want    bool
	}{
		{name: "a fresh worktree: a Podfile and no Pods", files: []string{"Podfile"}, want: true},
		{name: "pods installed", entries: []string{"Pods"}, files: []string{"Podfile"}, want: false},
		{name: "no CocoaPods in this project at all", entries: []string{"Packages"}, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, entry := range tc.entries {
				if err := os.MkdirAll(filepath.Join(dir, entry), 0o750); err != nil {
					t.Fatal(err)
				}
			}
			for _, file := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, file), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if got := PodInstallPending(dir); got != tc.want {
				t.Fatalf("PodInstallPending = %v, want %v", got, tc.want)
			}
		})
	}
}
