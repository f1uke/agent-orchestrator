// Package simchrome reads Apple's own description of what a device looks like,
// so the desktop pane can draw a frame around a screen instead of guessing one.
//
// The guess was visibly wrong, twice. A phone's body is not a uniform hairline
// with an eyeballed corner radius: for an iPhone 17 Pro the frame is 4.5% of
// the screen's width and the display's corners are 15.5% of it, and both differ
// per model. Those numbers are on the machine already - Xcode ships the artwork
// the Simulator app itself draws - so they are read rather than invented. It is
// the same source serve-sim uses, which is why its frame matches the device and
// ours did not.
//
// The chain, all of it plain files:
//
//	.simdevicetype/Contents/Resources/profile.plist  -> chromeIdentifier
//	DeviceKit/Chrome/<name>.devicechrome/…/chrome.json -> corner radius, insets
//	…/PhoneComposite.pdf                             -> the body's own size
//
// Nothing here is required for the pane to work. A device with no chrome on
// this machine - a new model, a trimmed Xcode, a future layout - simply gets no
// frame, which is why every failure returns "not found" rather than an error
// somebody has to read.
package simchrome

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Frame is a device's body, in multiples of its screen's width, so the pane can
// draw it at whatever size the screen is being shown.
type Frame struct {
	// Thickness is the body around the screen, as a fraction of screen width.
	Thickness float64 `json:"thickness"`
	// Radius is the display's own corner radius, as a fraction of screen width.
	Radius float64 `json:"radius"`
}

// ErrNoChrome is a device this machine has no artwork for. Not a failure: the
// pane draws no frame and shows the screen.
var ErrNoChrome = errors.New("simchrome: no device chrome for this device type")

// Roots are where the two halves live. Fields rather than constants so a test
// can point them at a fixture.
type Roots struct {
	DeviceTypes string
	Chrome      string
}

// DefaultRoots is where Xcode puts them.
func DefaultRoots() Roots {
	return Roots{
		DeviceTypes: "/Library/Developer/CoreSimulator/Profiles/DeviceTypes",
		Chrome:      "/Library/Developer/DeviceKit/Chrome",
	}
}

// chromeIdentifierPattern finds the one value needed out of profile.plist.
//
// The file is a binary property list and the value is a plain string inside it,
// so it is read rather than parsed: pulling in a plist decoder to fetch one
// distinctive literal would be a dependency for a single line.
//
// A binary plist stores strings without terminators, so a greedy match runs
// straight into whatever key follows - "phone11" reads as "phone11ZiPhone18".
// The match is therefore only a candidate, and the bundle that exists on disk
// decides where it really ends.
var chromeIdentifierPattern = regexp.MustCompile(`com\.apple\.dt\.devicekit\.chrome\.[A-Za-z0-9_]+`)

// chromeDirFor takes the longest prefix of a candidate name that is a bundle
// this machine actually has.
func chromeDirFor(root, candidate string) (string, bool) {
	for end := len(candidate); end > 0; end-- {
		dir := filepath.Join(root, candidate[:end]+".devicechrome", "Contents", "Resources")
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir, true
		}
	}
	return "", false
}

// chromeFile is the part of chrome.json this needs.
type chromeFile struct {
	Paths struct {
		SimpleOutsideBorder struct {
			CornerRadiusX float64 `json:"cornerRadiusX"`
		} `json:"simpleOutsideBorder"`
	} `json:"paths"`
	Images struct {
		Sizing struct {
			LeftWidth    float64 `json:"leftWidth"`
			RightWidth   float64 `json:"rightWidth"`
			TopHeight    float64 `json:"topHeight"`
			BottomHeight float64 `json:"bottomHeight"`
		} `json:"sizing"`
		// Composite is one image of the whole body. Only some models have one,
		// and only those can be measured - see bodyWidthOf.
		Composite string `json:"composite"`
	} `json:"images"`
}

// Lookup finds the frame for a device type, e.g.
// "com.apple.CoreSimulator.SimDeviceType.iPhone-17-Pro".
func Lookup(roots Roots, deviceTypeIdentifier string) (Frame, error) {
	bundle, ok := deviceTypeBundle(roots.DeviceTypes, deviceTypeIdentifier)
	if !ok {
		return Frame{}, ErrNoChrome
	}
	profile, err := os.ReadFile(filepath.Join(bundle, "Contents", "Resources", "profile.plist"))
	if err != nil {
		return Frame{}, ErrNoChrome
	}
	identifier := chromeIdentifierPattern.Find(profile)
	if identifier == nil {
		return Frame{}, ErrNoChrome
	}

	// "com.apple.dt.devicekit.chrome.phone11…" -> "phone11.devicechrome"
	candidate := string(identifier[strings.LastIndex(string(identifier), ".")+1:])
	dir, ok := chromeDirFor(roots.Chrome, candidate)
	if !ok {
		return Frame{}, ErrNoChrome
	}
	raw, err := os.ReadFile(filepath.Join(dir, "chrome.json"))
	if err != nil {
		return Frame{}, ErrNoChrome
	}
	var chrome chromeFile
	if err := json.Unmarshal(raw, &chrome); err != nil {
		return Frame{}, ErrNoChrome
	}

	// The insets are in the artwork's own units, so the body's size is needed to
	// turn them into fractions of the screen.
	bodyWidth, err := bodyWidthOf(dir, chrome)
	if err != nil {
		return Frame{}, ErrNoChrome
	}
	sizing := chrome.Images.Sizing
	screenWidth := bodyWidth - sizing.LeftWidth - sizing.RightWidth
	if screenWidth <= 0 || sizing.LeftWidth <= 0 {
		return Frame{}, ErrNoChrome
	}

	// The display's corners are the body's, less the body around them.
	outer := chrome.Paths.SimpleOutsideBorder.CornerRadiusX
	inner := outer - sizing.LeftWidth
	if inner < 0 {
		inner = 0
	}
	frame := Frame{
		Thickness: sizing.LeftWidth / screenWidth,
		Radius:    inner / screenWidth,
	}
	if !frame.plausible() {
		// A number this far out means the artwork is not laid out the way this
		// reads it. Drawing it anyway is how a frame ends up 45% of the screen
		// wide; no frame is always better than a wrong one.
		return Frame{}, ErrNoChrome
	}
	return frame, nil
}

// plausible bounds a frame to what a device could actually be. A body is a few
// percent of the screen it surrounds, and a display's corners a fraction of its
// width - measured across every model on this machine, 4-5% and 11-16%.
func (f Frame) plausible() bool {
	return f.Thickness > 0 && f.Thickness <= 0.12 && f.Radius >= 0 && f.Radius <= 0.35
}

// deviceTypeBundle finds the .simdevicetype for an identifier.
//
// Matched on letters and digits alone rather than by rebuilding the name:
// the identifier spells "iPhone-SE-3rd-generation" and the bundle is
// "iPhone SE (3rd generation).simdevicetype", so dashes-to-spaces gets it
// wrong for every model whose name has punctuation in it - which is most of
// the iPads and all of the watches.
func deviceTypeBundle(root, deviceTypeIdentifier string) (string, bool) {
	const prefix = "com.apple.CoreSimulator.SimDeviceType."
	if !strings.HasPrefix(deviceTypeIdentifier, prefix) {
		return "", false
	}
	want := alphanumeric(strings.TrimPrefix(deviceTypeIdentifier, prefix))
	if want == "" {
		return "", false
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", false
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".simdevicetype") {
			continue
		}
		if alphanumeric(strings.TrimSuffix(name, ".simdevicetype")) == want {
			return filepath.Join(root, name), true
		}
	}
	return "", false
}

// alphanumeric keeps only letters and digits, lowercased, so two spellings of
// the same model compare equal.
func alphanumeric(s string) string {
	var out strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			out.WriteRune(r)
		}
	}
	return out.String()
}

// bodyWidthOf measures the device body in the artwork's own units.
//
// Only a model drawn as one composite image can be measured this way. The rest
// are nine slices whose middle pieces are tileable strips, so their widths say
// nothing about the body - deriving a frame from them produced a body 14% of
// the screen's width for a phone and 45% for an iPad, which is worse than
// drawing no frame at all. Those models get none.
func bodyWidthOf(dir string, chrome chromeFile) (float64, error) {
	if chrome.Images.Composite == "" {
		return 0, ErrNoChrome
	}
	return artworkWidth(filepath.Join(dir, chrome.Images.Composite+".pdf"))
}

// mediaBox is the page size every PDF declares near its front.
var mediaBox = regexp.MustCompile(`/MediaBox\s*\[\s*([\d.-]+)\s+([\d.-]+)\s+([\d.-]+)\s+([\d.-]+)`)

// artworkHeadBytes bounds how much of the artwork is read looking for its size.
// The declaration is in the first page object; reading the whole vector body
// would be megabytes for one number.
const artworkHeadBytes = 128 << 10

func artworkWidth(path string) (float64, error) {
	file, err := os.Open(path) //nolint:gosec // a path built from Xcode's own bundle layout
	if err != nil {
		return 0, err
	}
	defer func() { _ = file.Close() }()
	head := make([]byte, artworkHeadBytes)
	n, err := io.ReadFull(file, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return 0, err
	}
	match := mediaBox.FindSubmatch(head[:n])
	if match == nil {
		return 0, ErrNoChrome
	}
	left, err1 := strconv.ParseFloat(string(match[1]), 64)
	right, err2 := strconv.ParseFloat(string(match[3]), 64)
	if err1 != nil || err2 != nil || right-left <= 0 {
		return 0, ErrNoChrome
	}
	return right - left, nil
}
