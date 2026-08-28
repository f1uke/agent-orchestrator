package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/pngmeta"
)

// realPNG is a decodable frame, because the build id is written INTO the
// capture: a stub that is not a PNG would pass a test that the real path fails.
func realPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 6))
	img.Set(2, 2, color.RGBA{R: 10, G: 200, B: 40, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

// shotBuildDeps is a booted device whose data directory a test can install into.
func shotBuildDeps(t *testing.T) (Deps, string) {
	t.Helper()
	deps, _, dataPath, _ := appDeps(t)
	deps, _ = withScreenshotBytes(t, deps, dataPath, realPNG(t))
	return deps, dataPath
}

// withScreenshotBytes replaces the faked `simctl io screenshot` so it writes a
// real PNG, keeping every other command answered as appDeps set it up.
func withScreenshotBytes(t *testing.T, deps Deps, _ string, payload []byte) (Deps, []byte) {
	t.Helper()
	inner := deps.CommandOutput
	deps.CommandOutput = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if len(args) >= 4 && args[1] == "io" && args[3] == "screenshot" {
			path := args[len(args)-1]
			if err := os.WriteFile(path, payload, 0o600); err != nil {
				return nil, err
			}
			return nil, nil
		}
		return inner(ctx, name, args...)
	}
	return deps, payload
}

func shotJSON(t *testing.T, deps Deps, args ...string) simShotResult {
	t.Helper()
	out, errOut, err := executeCLI(t, deps, append([]string{"sim", "shot", "--json"}, args...)...)
	if err != nil {
		t.Fatalf("shot failed: %v\nstderr=%s", err, errOut)
	}
	var res simShotResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	return res
}

// The case the whole fingerprint exists for: install build A, capture, install
// build B, capture, and the two captures are visibly of different software.
func TestSimShot_CapturesEitherSideOfAReinstallAreDistinguishable(t *testing.T) {
	deps, dataPath := shotBuildDeps(t)
	installFixture(t, dataPath, "MyApp", "com.example.MyApp", "1.0", "1", "build A")

	first := shotJSON(t, deps)
	if first.Build == nil {
		t.Fatalf("no build recorded: %s", first.BuildUnknown)
	}

	// What `xcodebuild test` does behind an agent's back.
	installFixture(t, dataPath, "MyApp", "com.example.MyApp", "1.0", "1", "build B")

	second := shotJSON(t, deps)
	if second.Build == nil {
		t.Fatalf("no build recorded: %s", second.BuildUnknown)
	}
	if first.Build.ID == second.Build.ID {
		t.Fatalf("both captures report %q; a reinstall between them was invisible", first.Build.ID)
	}
	// The version did not move, which is exactly why a commit sha cannot answer
	// this question and the digest has to.
	if first.Build.Version != second.Build.Version {
		t.Fatalf("the fixture changed more than the bytes: %q vs %q", first.Build.Version, second.Build.Version)
	}
	if first.Build.Digest == second.Build.Digest {
		t.Fatalf("the digest did not move: %q", first.Build.Digest)
	}
}

// The build travels inside the picture, because evidence gets downloaded,
// moved and dragged in by somebody who was never told there was a sidecar.
func TestSimShot_WritesTheBuildIntoThePNG(t *testing.T) {
	deps, dataPath := shotBuildDeps(t)
	installFixture(t, dataPath, "MyApp", "com.example.MyApp", "6.5.18", "708", "build A")

	res := shotJSON(t, deps)
	if res.Build == nil {
		t.Fatalf("no build recorded: %s", res.BuildUnknown)
	}
	embedded, ok := pngmeta.Get(res.Path, "ao-build")
	if !ok {
		t.Fatalf("the capture at %s carries no build", res.Path)
	}
	if embedded != res.Build.ID {
		t.Fatalf("embedded %q, reported %q", embedded, res.Build.ID)
	}
	if !strings.HasPrefix(embedded, "com.example.MyApp 6.5.18 (708) sha256:") {
		t.Fatalf("build id = %q", embedded)
	}
	// And it is still a screenshot.
	raw, err := os.ReadFile(res.Path) //nolint:gosec // test fixture
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(raw)); err != nil {
		t.Fatalf("the capture stopped being a readable PNG: %v", err)
	}
	if res.Bytes != int64(len(raw)) {
		t.Fatalf("reported %d bytes, file is %d", res.Bytes, len(raw))
	}
}

// A capture that cannot say which build it saw must say THAT, out loud. Silence
// would let a reader assume the question was not worth asking.
func TestSimShot_SaysWhyThereIsNoBuild(t *testing.T) {
	deps, _ := shotBuildDeps(t)

	res := shotJSON(t, deps)
	if res.Build != nil {
		t.Fatalf("a device with nothing installed reported a build: %+v", res.Build)
	}
	if !strings.Contains(res.BuildUnknown, "no app is installed") {
		t.Fatalf("buildUnknown = %q", res.BuildUnknown)
	}
	if !strings.Contains(res.BuildUnknown, "ao sim install") {
		t.Fatalf("the reason must name the way out: %q", res.BuildUnknown)
	}
}

// On a device carrying several apps the capture reports the newest install and
// marks the answer as a pick. It is never silently wrong - the build id names
// the app it is about - and --app settles it for good.
func TestSimShot_PicksTheNewestInstallAndMarksIt(t *testing.T) {
	deps, dataPath := shotBuildDeps(t)
	installFixture(t, dataPath, "One", "com.example.One", "1.0", "1", "a")
	newest := installFixture(t, dataPath, "Two", "com.example.Two", "2.0", "2", "b")
	touchLater(t, newest)

	res := shotJSON(t, deps)
	if res.Build == nil {
		t.Fatalf("a device with two apps recorded no build: %s", res.BuildUnknown)
	}
	if res.Build.BundleID != "com.example.Two" {
		t.Fatalf("fingerprinted %q, want the most recent install", res.Build.BundleID)
	}
	if !res.Build.Inferred || res.Build.Of != 2 {
		t.Fatalf("a pick must be reported as one: %+v", res.Build)
	}

	named := shotJSON(t, deps, "--app", "com.example.One")
	if named.Build == nil || named.Build.BundleID != "com.example.One" {
		t.Fatalf("--app must settle it: %+v (%s)", named.Build, named.BuildUnknown)
	}
	if named.Build.Inferred {
		t.Fatalf("a named app was reported as a pick: %+v", named.Build)
	}
}

// The Build: line a person reads has to carry the caveat too, not only the JSON.
func TestSimShot_TextSaysWhenTheAppWasAPick(t *testing.T) {
	deps, dataPath := shotBuildDeps(t)
	installFixture(t, dataPath, "One", "com.example.One", "1.0", "1", "a")
	newest := installFixture(t, dataPath, "Two", "com.example.Two", "2.0", "2", "b")
	touchLater(t, newest)

	out, errOut, err := executeCLI(t, deps, "sim", "shot")
	if err != nil {
		t.Fatalf("shot failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "newest of 2 apps on this device") ||
		!strings.Contains(out, "AO_SIM_APP") {
		t.Fatalf("the Build line must say it chose, and how to pin it:\n%s", out)
	}
}

// A capture is worth more than its annotation: the screenshot still lands and
// the failure is reported rather than the command failing.
func TestSimShot_AnUnwritablePNGStillProducesACapture(t *testing.T) {
	deps, _, dataPath, _ := appDeps(t)
	deps, _ = withScreenshotBytes(t, deps, dataPath, []byte("not a png at all"))
	installFixture(t, dataPath, "MyApp", "com.example.MyApp", "1.0", "1", "build A")

	res := shotJSON(t, deps)
	if res.Path == "" {
		t.Fatal("the capture was lost because its build could not be written")
	}
	if !strings.Contains(res.BuildUnknown, "could not be written into the PNG") {
		t.Fatalf("buildUnknown = %q", res.BuildUnknown)
	}
}
