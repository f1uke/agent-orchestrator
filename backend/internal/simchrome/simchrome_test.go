package simchrome_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/simchrome"
)

// A device type bundle and the chrome bundle it points at, laid out the way
// Xcode lays them out, so the reader is exercised on the shape it really meets
// rather than on a stub of its own design.
type fixture struct {
	roots simchrome.Roots
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	return &fixture{roots: simchrome.Roots{
		DeviceTypes: filepath.Join(root, "DeviceTypes"),
		Chrome:      filepath.Join(root, "Chrome"),
	}}
}

// deviceType writes a binary-plist-shaped profile. The value is a plain string
// inside it with no terminator, so the bytes after it are deliberately another
// key - which is exactly what made a greedy match read "phone11ZiPhone18".
func (f *fixture) deviceType(t *testing.T, bundleName, chromeName string) {
	t.Helper()
	dir := filepath.Join(f.roots.DeviceTypes, bundleName+".simdevicetype", "Contents", "Resources")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := append([]byte("bplist00\xde\x01\x02chromeIdentifier_\x10\x25com.apple.dt.devicekit.chrome."+chromeName),
		[]byte("ZiPhone18\\productClass")...)
	if err := os.WriteFile(filepath.Join(dir, "profile.plist"), body, 0o600); err != nil {
		t.Fatalf("write profile: %v", err)
	}
}

// chrome writes an artwork bundle: the JSON Xcode ships and a one-page PDF
// whose MediaBox is the body's size.
func (f *fixture) chrome(t *testing.T, name string, bodyWidth, inset, cornerRadius float64, composite bool) {
	t.Helper()
	dir := filepath.Join(f.roots.Chrome, name+".devicechrome", "Contents", "Resources")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	compositeField := `"composite": "Body",`
	if !composite {
		compositeField = ""
	}
	doc := `{"identifier":"com.apple.dt.devicekit.chrome.` + name + `",
	  "paths": {"simpleOutsideBorder": {"cornerRadiusX": ` + ftoa(cornerRadius) + `}},
	  "images": {` + compositeField + `
	    "sizing": {"leftWidth": ` + ftoa(inset) + `, "rightWidth": ` + ftoa(inset) +
		`, "topHeight": ` + ftoa(inset) + `, "bottomHeight": ` + ftoa(inset) + `}}}`
	if err := os.WriteFile(filepath.Join(dir, "chrome.json"), []byte(doc), 0o600); err != nil {
		t.Fatalf("write chrome.json: %v", err)
	}
	if composite {
		pdf := "%PDF-1.4\n1 0 obj<</Type/Page/MediaBox[0 0 " + ftoa(bodyWidth) + " 900]>>endobj\n"
		if err := os.WriteFile(filepath.Join(dir, "Body.pdf"), []byte(pdf), 0o600); err != nil {
			t.Fatalf("write pdf: %v", err)
		}
	}
}

func ftoa(f float64) string {
	if f == float64(int(f)) {
		return itoa(int(f))
	}
	return itoa(int(f))
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := ""
	for i > 0 {
		digits = string(rune('0'+i%10)) + digits
		i /= 10
	}
	return digits
}

const proID = "com.apple.CoreSimulator.SimDeviceType.iPhone-17-Pro"

// The numbers are the device's own, and they are the reason this reads files at
// all: a body of 18 around a 400-wide screen is 4.5% of it, and a display whose
// body corners are 80 has its own corners at 62 - 15.5%. Guessing them was
// visibly wrong.
func TestLookup_ReadsTheDevicesOwnProportions(t *testing.T) {
	f := newFixture(t)
	f.deviceType(t, "iPhone 17 Pro", "phone11")
	f.chrome(t, "phone11", 436, 18, 80, true)

	frame, err := simchrome.Lookup(f.roots, proID)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got := frame.Thickness; got < 0.0449 || got > 0.0451 {
		t.Fatalf("thickness = %g, want 18/400", got)
	}
	if got := frame.Radius; got < 0.1549 || got > 0.1551 {
		t.Fatalf("radius = %g, want (80-18)/400", got)
	}
}

// A binary plist stores strings without terminators, so a greedy match runs
// into the next key: "phone11" reads as "phone11ZiPhone18". What exists on disk
// is what decides where the name ends.
func TestLookup_FindsTheChromeDespiteTheKeyThatFollowsIt(t *testing.T) {
	f := newFixture(t)
	f.deviceType(t, "iPhone 17 Pro", "phone11")
	f.chrome(t, "phone11", 436, 18, 80, true)
	// A longer name that also starts with phone11 must not be preferred over the
	// real one just because it matches more of the run-on bytes.
	f.chrome(t, "phone1", 436, 18, 80, true)

	if _, err := simchrome.Lookup(f.roots, proID); err != nil {
		t.Fatalf("lookup: %v", err)
	}
}

// The identifier spells the model with dashes; the bundle spells it with
// spaces and brackets. Rebuilding the name from the identifier got every model
// with punctuation wrong.
func TestLookup_MatchesABundleNameWithPunctuationInIt(t *testing.T) {
	f := newFixture(t)
	f.deviceType(t, "iPhone SE (3rd generation)", "phone")
	f.chrome(t, "phone", 436, 18, 80, true)

	if _, err := simchrome.Lookup(f.roots, "com.apple.CoreSimulator.SimDeviceType.iPhone-SE-3rd-generation"); err != nil {
		t.Fatalf("lookup: %v", err)
	}
}

// A model drawn as nine slices has no body to measure, and the slice widths do
// not stand in for one - deriving from them produced a body 45% of an iPad's
// screen. No frame beats a wrong one.
func TestLookup_ADeviceWithNoMeasurableBodyGetsNoFrame(t *testing.T) {
	f := newFixture(t)
	f.deviceType(t, "iPad Pro", "tablet5")
	f.chrome(t, "tablet5", 0, 18, 80, false)

	if _, err := simchrome.Lookup(f.roots, "com.apple.CoreSimulator.SimDeviceType.iPad-Pro"); !errors.Is(err, simchrome.ErrNoChrome) {
		t.Fatalf("err = %v, want ErrNoChrome", err)
	}
}

// The last guard: artwork laid out in a way this does not understand yields a
// number, and a number this far out would be drawn. It is refused instead.
func TestLookup_AnImplausibleFrameIsRefusedRatherThanDrawn(t *testing.T) {
	f := newFixture(t)
	f.deviceType(t, "iPhone 17 Pro", "phone11")
	// A body of 180 around a 76-wide screen: over twice the screen's width.
	f.chrome(t, "phone11", 436, 180, 200, true)

	if _, err := simchrome.Lookup(f.roots, proID); !errors.Is(err, simchrome.ErrNoChrome) {
		t.Fatalf("err = %v, want an implausible frame refused", err)
	}
}

// Every missing piece is "no frame", never an error somebody has to read: the
// pane works without one.
func TestLookup_MissingPiecesAreNotFailures(t *testing.T) {
	f := newFixture(t)
	for name, prepare := range map[string]func(){
		"no device type at all": func() {},
		"no chrome bundle":      func() { f.deviceType(t, "iPhone 17 Pro", "nosuch") },
	} {
		t.Run(name, func(t *testing.T) {
			prepare()
			if _, err := simchrome.Lookup(f.roots, proID); !errors.Is(err, simchrome.ErrNoChrome) {
				t.Fatalf("err = %v, want ErrNoChrome", err)
			}
		})
	}
	if _, err := simchrome.Lookup(f.roots, "not-a-device-type"); !errors.Is(err, simchrome.ErrNoChrome) {
		t.Fatalf("err = %v, want ErrNoChrome", err)
	}
}
