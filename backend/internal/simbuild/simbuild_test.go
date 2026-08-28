package simbuild

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// installApp lays out a device data directory the way CoreSimulator does:
// <data>/Containers/Bundle/Application/<uuid>/<Name>.app. Everything this
// package reads is on disk, so a fixture is a directory tree rather than a
// mocked subprocess - the one exception being the two tools it shells out to.
func installApp(t *testing.T, dataPath, container, name, bundleID, version, number string, files map[string]string) string {
	t.Helper()
	app := filepath.Join(dataPath, "Containers", "Bundle", "Application", container, name+".app")
	if err := os.MkdirAll(app, 0o750); err != nil {
		t.Fatal(err)
	}
	info := map[string]string{
		"CFBundleIdentifier":         bundleID,
		"CFBundleName":               name,
		"CFBundleShortVersionString": version,
		"CFBundleVersion":            number,
	}
	raw, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "Info.plist"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	for rel, content := range files {
		path := filepath.Join(app, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return app
}

// fakeRunner answers the two commands this package shells out to. plutil is
// answered by reading the fixture's Info.plist, which the fixtures write as
// JSON already - plutil's job in production is exactly that conversion.
// codesign is answered by the test, so both digest methods are exercisable
// without a signed bundle.
func fakeRunner(cdhash string) Runner {
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		switch name {
		case "plutil":
			return os.ReadFile(args[len(args)-1]) //nolint:gosec // test fixture
		case "codesign":
			if cdhash == "" {
				return []byte("code object is not signed at all"), errors.New("exit 1")
			}
			return []byte("Identifier=x\nCandidateCDHashFull sha256=" + cdhash + "\nCDHash=" + cdhash + "\n"), nil
		}
		return nil, errors.New("unexpected command " + name)
	}
}

func TestRead_FingerprintsTheSingleInstalledApp(t *testing.T) {
	data := t.TempDir()
	installApp(t, data, "c1", "MyApp", "com.example.MyApp", "6.5.18", "708",
		map[string]string{"MyApp": "the binary"})

	build, err := Read(t.Context(), fakeRunner("d114bed635ed2eb91f5cc60fcf81cf77c59c4e12"), data, "")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if build.BundleID != "com.example.MyApp" || build.Version != "6.5.18" || build.Number != "708" {
		t.Fatalf("build = %+v, want the fixture's identity", build)
	}
	if want := "cdhash:d114bed635ed2eb91f5c"; build.Digest != want {
		t.Fatalf("digest = %q, want %q", build.Digest, want)
	}
	if got := build.ID(); got != "com.example.MyApp 6.5.18 (708) cdhash:d114bed635ed2eb91f5c" {
		t.Fatalf("ID() = %q", got)
	}
	if build.InstalledAt.IsZero() {
		t.Fatal("a build with no install time cannot say when it landed")
	}
}

// The whole reason this exists: two builds must not read as the same one. The
// change is in a FRAMEWORK rather than the main executable, which is the case
// that would have slipped past a fingerprint of the executable alone - measured
// on a real app whose executable was 90 KB of a 505 MB bundle.
func TestRead_UnsignedBundlesAreToldApartByTheirContents(t *testing.T) {
	dataA, dataB := t.TempDir(), t.TempDir()
	installApp(t, dataA, "c1", "MyApp", "com.example.MyApp", "1.0", "1",
		map[string]string{"MyApp": "same executable", "Frameworks/App.framework/App": "build A"})
	installApp(t, dataB, "c1", "MyApp", "com.example.MyApp", "1.0", "1",
		map[string]string{"MyApp": "same executable", "Frameworks/App.framework/App": "build B"})

	run := fakeRunner("")
	a, err := Read(t.Context(), run, dataA, "")
	if err != nil {
		t.Fatalf("read A: %v", err)
	}
	b, err := Read(t.Context(), run, dataB, "")
	if err != nil {
		t.Fatalf("read B: %v", err)
	}
	if !strings.HasPrefix(a.Digest, MethodSHA256+":") {
		t.Fatalf("an unsigned bundle must fall back to a content hash, got %q", a.Digest)
	}
	if a.Digest == b.Digest {
		t.Fatalf("two builds differing only inside Frameworks/ produced one digest %q", a.Digest)
	}
	// Same bytes, same answer: a digest that moved on its own would make every
	// pair of captures look like different builds and teach agents to ignore it.
	again, err := Read(t.Context(), run, dataA, "")
	if err != nil {
		t.Fatal(err)
	}
	if again.Digest != a.Digest {
		t.Fatalf("re-reading the same bundle gave %q then %q", a.Digest, again.Digest)
	}
}

// `xcodebuild test` installs the XCUITest host alongside the app. Counting it
// as a candidate would make every UI-tested project ambiguous, which is exactly
// the project this is for.
func TestUnderTest_DiscountsTheXCTestRunner(t *testing.T) {
	apps := []App{
		{BundleID: "com.example.MyApp"},
		{BundleID: "com.example.MyAppUITests.xctrunner"},
	}
	app, _, err := UnderTest(apps, "")
	if err != nil {
		t.Fatalf("under test: %v", err)
	}
	if app.BundleID != "com.example.MyApp" {
		t.Fatalf("picked %q", app.BundleID)
	}
}

// A device that has accumulated apps is the ORDINARY case, not the exception:
// the simulator this was measured on carried nine. So several candidates
// resolve to the one most recently installed - the app that just changed, which
// is what the question is about - and the pick is reported as a pick.
func TestUnderTest_PicksTheMostRecentlyInstalledAndSaysSo(t *testing.T) {
	old := time.Date(2026, 7, 22, 18, 32, 0, 0, time.UTC)
	apps := []App{
		{BundleID: "com.example.One", InstalledAt: old},
		{BundleID: "com.example.Two", InstalledAt: old.Add(72 * time.Hour)},
	}
	app, chosen, err := UnderTest(apps, "")
	if err != nil {
		t.Fatalf("under test: %v", err)
	}
	if app.BundleID != "com.example.Two" {
		t.Fatalf("picked %q, want the newest install", app.BundleID)
	}
	if !chosen {
		t.Fatal("a pick between several apps must report that it chose")
	}

	// Named explicitly, nothing was chosen.
	named, chosen, err := UnderTest(apps, "com.example.One")
	if err != nil || named.BundleID != "com.example.One" {
		t.Fatalf("naming a bundle id must win: %+v %v", named, err)
	}
	if chosen {
		t.Fatal("a named app was reported as a pick")
	}
	if _, _, err := UnderTest(apps, "com.example.Three"); !errors.Is(err, ErrUnknownApp) {
		t.Fatalf("naming an app that is not there = %v, want ErrUnknownApp", err)
	}
}

// One app on the device is not a pick, however many times it is read.
func TestUnderTest_ASingleAppIsNotAGuess(t *testing.T) {
	app, chosen, err := UnderTest([]App{{BundleID: "com.example.Only"}}, "")
	if err != nil || app.BundleID != "com.example.Only" {
		t.Fatalf("%+v %v", app, err)
	}
	if chosen {
		t.Fatal("the only app on a device was reported as a pick")
	}
}

func TestListApps_SaysWhenThereIsNothingToFingerprint(t *testing.T) {
	if _, err := ListApps(t.Context(), fakeRunner(""), ""); !errors.Is(err, ErrNoDataPath) {
		t.Fatalf("no data path = %v, want ErrNoDataPath", err)
	}
	if _, err := ListApps(t.Context(), fakeRunner(""), t.TempDir()); !errors.Is(err, ErrNoApp) {
		t.Fatalf("empty device = %v, want ErrNoApp", err)
	}
}

// A version string is what a build SAYS it is. Two installs claiming the same
// version and carrying different bytes is precisely the stale-install case the
// commit sha could never express, so ID() has to differ.
func TestID_SeparatesTwoInstallsThatClaimTheSameVersion(t *testing.T) {
	a := Build{App: App{BundleID: "com.example.MyApp", Version: "1.0", Number: "1"}, Digest: "sha256:aaaa"}
	b := Build{App: App{BundleID: "com.example.MyApp", Version: "1.0", Number: "1"}, Digest: "sha256:bbbb"}
	if a.ID() == b.ID() {
		t.Fatalf("two different builds share one id %q", a.ID())
	}
}
