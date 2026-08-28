// Package simbuild identifies the application installed on an iOS Simulator, so
// a capture can say WHICH BUILD it saw.
//
// It exists because `xcodebuild test -destination <udid>` builds and installs
// the app target as part of running tests: the binary changes underneath an
// agent that never asked for it, and a screenshot taken before that install
// looks exactly like one taken after. Three separate workers have independently
// rediscovered "check the md5 before you trust a screenshot" and written it in
// their own notes - a lesson everybody has to learn for themselves is a job the
// system should be doing.
//
// AO cannot stop xcodebuild; it never passes through us. So the result is made
// honest instead: the identity of the on-device binary is captured at the
// moment of the screenshot and stored with the evidence, and captures from
// either side of a reinstall are then visibly from different builds rather than
// silently mixed.
//
// Nothing here can change a device. It reads the app bundle simctl already told
// us where to find, and asks codesign what it is.
package simbuild

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Sentinels. Every one of them means "no fingerprint", never "the wrong
// fingerprint": a build id that might be about a different app is worse than
// none, because the whole point is to be trusted.
var (
	// ErrNoApp: nothing user-installed is on this device.
	ErrNoApp = errors.New("simbuild: no app installed")
	// ErrUnknownApp: the caller named a bundle id this device does not have.
	ErrUnknownApp = errors.New("simbuild: no such app on this device")
	// ErrNoDataPath: simctl did not say where this device keeps its data, so
	// its applications cannot be found.
	ErrNoDataPath = errors.New("simbuild: device has no data path")
)

// Runner executes a command and returns its combined output, matching
// simctl.Runner. Injected so this package is testable without a mac.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Digest methods. The method is part of the digest string because two digests
// are only comparable when they were produced the same way, and a reader who
// cannot tell would compare them anyway.
const (
	// MethodCDHash is Apple's own identity for the code: the hash of the code
	// directory, which seals the executable's pages AND, through
	// _CodeSignature/CodeResources, every resource in the bundle. It costs
	// about 15 ms regardless of how big the app is.
	MethodCDHash = "cdhash"
	// MethodSHA256 is a content hash over the whole bundle, used when the app
	// is not signed - which simulator builds frequently are not, since
	// CODE_SIGNING_ALLOWED=NO is a normal thing to build with. It is exact and
	// always available, and costs about a second on a 500 MB app.
	MethodSHA256 = "sha256"
)

// digestPrefix is how many hex characters of a digest are kept. Twenty is far
// past the point where two different builds could collide, and short enough to
// read in a terminal and sit in a PNG text chunk.
const digestPrefix = 20

// xctrunnerSuffix marks the XCUITest host `xcodebuild test` installs alongside
// the app. It is an app, it is user-installed, and it is never the thing a
// screenshot is about - so discovery drops it rather than calling a device with
// one app on it ambiguous.
const xctrunnerSuffix = ".xctrunner"

// App is one application installed on a device.
type App struct {
	BundleID string `json:"bundleId"`
	// Name is CFBundleName, or the .app directory's own name when the bundle
	// does not carry one.
	Name string `json:"name"`
	// Path is the .app on the host filesystem.
	Path string `json:"path"`
	// Version is CFBundleShortVersionString ("6.5.18") and Number is
	// CFBundleVersion ("708"). Both are what the build SAYS it is, which is
	// exactly why they are not the identity: a stale install is right version,
	// wrong bytes.
	Version string `json:"version,omitempty"`
	Number  string `json:"number,omitempty"`
	// InstalledAt is when this install landed, from the container CoreSimulator
	// creates for it. It is what orders the apps on a device, and the order is
	// what picks one: see UnderTest.
	InstalledAt time.Time `json:"installedAt"`
}

// Build is the identity of one installed application at one moment.
type Build struct {
	App
	// Digest is "<method>:<hex>" - see MethodCDHash and MethodSHA256. It is the
	// only field that answers "are these two captures the same bytes".
	Digest string `json:"digest"`
	// Inferred marks a build AO chose rather than was told: the device carries
	// several apps and this is the one most recently installed. It is reported
	// rather than hidden, and never suppresses the answer - a capture that named
	// no app at all would be useless on a real developer's device, where nine
	// apps is ordinary. Of is how many it chose from.
	Inferred bool `json:"inferred,omitempty"`
	Of       int  `json:"of,omitempty"`
}

// ID is the one-line identity written next to a screenshot: enough for a person
// to see at a glance that two captures are of different builds, and stable
// enough to compare by string equality.
func (b Build) ID() string {
	parts := []string{b.BundleID}
	switch {
	case b.Version != "" && b.Number != "":
		parts = append(parts, b.Version+" ("+b.Number+")")
	case b.Version != "":
		parts = append(parts, b.Version)
	case b.Number != "":
		parts = append(parts, "("+b.Number+")")
	}
	if b.Digest != "" {
		parts = append(parts, b.Digest)
	}
	return strings.Join(parts, " ")
}

// Empty reports a build nothing could be learned about.
func (b Build) Empty() bool { return b.BundleID == "" && b.Digest == "" }

// ListApps returns the applications installed on a device, newest install
// first.
//
// It reads the device's own data directory rather than running `simctl
// listapps`, for two reasons that both matter: that directory holds ONLY
// user-installed apps (the hundred system apps live in the runtime root, and
// none of them is ever the thing under test), and it can be read on a device
// that is shut down, which listapps refuses to do.
func ListApps(ctx context.Context, run Runner, dataPath string) ([]App, error) {
	if strings.TrimSpace(dataPath) == "" {
		return nil, ErrNoDataPath
	}
	root := filepath.Join(dataPath, "Containers", "Bundle", "Application")
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoApp
	}
	if err != nil {
		return nil, fmt.Errorf("read installed applications: %w", err)
	}
	apps := []App{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		container := filepath.Join(root, entry.Name())
		bundle, ok := appBundleIn(container)
		if !ok {
			continue
		}
		app, err := readApp(ctx, run, bundle)
		if err != nil {
			// One unreadable bundle must not hide the others: a device with a
			// half-written install still has an app under test on it.
			continue
		}
		app.InstalledAt = installedAt(container, bundle)
		apps = append(apps, app)
	}
	if len(apps) == 0 {
		return nil, ErrNoApp
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].BundleID < apps[j].BundleID })
	return apps, nil
}

// UnderTest picks the app a capture is about, and says whether it had to
// choose.
//
// A named bundle id is taken as given. Otherwise it is the MOST RECENTLY
// INSTALLED app, with the XCUITest host `xcodebuild test` puts on the device
// discounted - because the app that just landed is the one the question is
// about, and because the alternative was measured and does not survive contact:
// a real developer's simulator in this repo's own house carried NINE user apps,
// so "the single installed app" would have answered "unknown" every time.
//
// Choosing is safe here in a way it is not elsewhere in `ao sim`, and the
// difference is worth stating: a chosen build is never silently wrong, because
// the answer names the bundle id it is about. A reader who expected a different
// app can see that at a glance, and two captures of different apps are visibly
// not comparable. What a wrong pick costs is a pin (--app, or $AO_SIM_APP); what
// refusing costs is the whole feature on the machines it was built for.
func UnderTest(apps []App, bundleID string) (App, bool, error) {
	if want := strings.TrimSpace(bundleID); want != "" {
		for _, app := range apps {
			if strings.EqualFold(app.BundleID, want) {
				return app, false, nil
			}
		}
		return App{}, false, fmt.Errorf("%w: %q", ErrUnknownApp, want)
	}
	candidates := []App{}
	for _, app := range apps {
		if strings.HasSuffix(strings.ToLower(app.BundleID), xctrunnerSuffix) {
			continue
		}
		candidates = append(candidates, app)
	}
	if len(candidates) == 0 {
		return App{}, false, ErrNoApp
	}
	newest := candidates[0]
	for _, app := range candidates[1:] {
		if app.InstalledAt.After(newest.InstalledAt) {
			newest = app
		}
	}
	return newest, len(candidates) > 1, nil
}

// Fingerprint reads the identity of an installed application.
func Fingerprint(ctx context.Context, run Runner, app App) (Build, error) {
	build := Build{App: app}
	if digest, ok := codeDirectoryHash(ctx, run, app.Path); ok {
		build.Digest = MethodCDHash + ":" + digest
		return build, nil
	}
	digest, err := bundleContentHash(ctx, app.Path)
	if err != nil {
		return Build{}, err
	}
	build.Digest = MethodSHA256 + ":" + digest
	return build, nil
}

// Read is the whole sequence: find the apps, pick the one under test, and
// fingerprint it.
func Read(ctx context.Context, run Runner, dataPath, bundleID string) (Build, error) {
	apps, err := ListApps(ctx, run, dataPath)
	if err != nil {
		return Build{}, err
	}
	app, inferred, err := UnderTest(apps, bundleID)
	if err != nil {
		return Build{}, err
	}
	build, err := Fingerprint(ctx, run, app)
	if err != nil {
		return Build{}, err
	}
	build.Inferred = inferred
	if inferred {
		build.Of = len(apps)
	}
	return build, nil
}

// ReadBundle reads the identity of a .app bundle anywhere on disk - a build
// about to be installed, rather than one already on a device. Exported so an
// install can say WHICH app it just sent, instead of falling back to "the only
// app on the device" and reporting the wrong one when there are two.
func ReadBundle(ctx context.Context, run Runner, bundle string) (App, error) {
	return readApp(ctx, run, bundle)
}

// appBundleIn finds the single .app inside one installed-application directory.
func appBundleIn(dir string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasSuffix(entry.Name(), ".app") {
			return filepath.Join(dir, entry.Name()), true
		}
	}
	return "", false
}

// infoPlist is the part of Info.plist this package reads.
type infoPlist struct {
	BundleID string `json:"CFBundleIdentifier"`
	Name     string `json:"CFBundleName"`
	Version  string `json:"CFBundleShortVersionString"`
	Number   string `json:"CFBundleVersion"`
}

// readApp reads a bundle's identity out of its Info.plist.
//
// The plist is binary, so it is converted by plutil - which every mac has in
// /usr/bin - rather than decoded here. This package's other reader (codesign)
// is a subprocess too, so the shape is consistent, and a targeted regexp over a
// binary plist (the trick internal/simchrome uses for one distinctive literal)
// would not survive four keys whose values are arbitrary strings.
func readApp(ctx context.Context, run Runner, bundle string) (App, error) {
	app := App{Path: bundle, Name: strings.TrimSuffix(filepath.Base(bundle), ".app")}
	if run == nil {
		return App{}, errors.New("simbuild: no command runner")
	}
	out, err := run(ctx, "plutil", "-convert", "json", "-o", "-", filepath.Join(bundle, "Info.plist"))
	if err != nil {
		return App{}, fmt.Errorf("read %s Info.plist: %w", filepath.Base(bundle), err)
	}
	var info infoPlist
	if err := json.Unmarshal(out, &info); err != nil {
		return App{}, fmt.Errorf("parse %s Info.plist: %w", filepath.Base(bundle), err)
	}
	if strings.TrimSpace(info.BundleID) == "" {
		return App{}, fmt.Errorf("%s has no CFBundleIdentifier", filepath.Base(bundle))
	}
	app.BundleID = info.BundleID
	if info.Name != "" {
		app.Name = info.Name
	}
	app.Version, app.Number = info.Version, info.Number
	return app, nil
}

// cdHashPattern pulls the code directory hash out of what codesign prints.
// CandidateCDHashFull is preferred where present because it is the whole hash
// rather than the truncated one.
var cdHashPattern = regexp.MustCompile(`(?m)^(?:CandidateCDHashFull|CDHash)[ =]\S*?([0-9a-f]{20,})\s*$`)

// codeDirectoryHash asks codesign what this code is. ok=false means the bundle
// is not signed, which is an ordinary state for a simulator build and never an
// error: the caller falls back to hashing the bytes itself.
func codeDirectoryHash(ctx context.Context, run Runner, bundle string) (string, bool) {
	if run == nil {
		return "", false
	}
	out, err := run(ctx, "codesign", "-d", "--verbose=4", bundle)
	if err != nil && len(out) == 0 {
		return "", false
	}
	match := cdHashPattern.FindSubmatch(out)
	if match == nil {
		return "", false
	}
	hash := string(match[1])
	if len(hash) > digestPrefix {
		hash = hash[:digestPrefix]
	}
	return hash, true
}

// bundleContentHash hashes every regular file in the bundle, in sorted path
// order, with each file's path and length framed into the stream so that
// renaming or splitting files cannot produce the same digest as leaving them
// alone.
//
// The whole bundle, and not just the main executable: measured on a real app,
// the executable was 90 KB of a 505 MB bundle, with everything that actually
// changes between builds sitting in Frameworks/. Hashing the executable alone
// would have reported two very different builds as the same one.
func bundleContentHash(ctx context.Context, bundle string) (string, error) {
	paths := []string{}
	err := filepath.WalkDir(bundle, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walk %s: %w", filepath.Base(bundle), err)
	}
	sort.Strings(paths)
	sum := sha256.New()
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		rel, relErr := filepath.Rel(bundle, path)
		if relErr != nil {
			rel = path
		}
		file, err := os.Open(path) //nolint:gosec // paths come from walking the bundle itself
		if err != nil {
			return "", fmt.Errorf("read %s: %w", rel, err)
		}
		// A hash.Hash never fails a write, which is why these are not checked.
		_, _ = fmt.Fprintf(sum, "%s\x00", rel)
		written, err := io.Copy(sum, file)
		_ = file.Close()
		if err != nil {
			return "", fmt.Errorf("read %s: %w", rel, err)
		}
		_, _ = fmt.Fprintf(sum, "\x00%d\x00", written)
	}
	return hex.EncodeToString(sum.Sum(nil))[:digestPrefix], nil
}

// installedAt is when this install landed.
//
// The container is the better of the two: CoreSimulator creates that directory
// for each install, so its timestamp moves even when the bundle copied into it
// keeps the source's own. The bundle is taken when it is newer, which is what a
// build written in place after the install looks like. A directory that cannot
// be stat'd contributes nothing rather than failing the read - what it costs is
// this app's place in the ordering, not the whole answer.
func installedAt(container, bundle string) time.Time {
	newest := time.Time{}
	for _, path := range []string{container, bundle} {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	return newest.UTC()
}
