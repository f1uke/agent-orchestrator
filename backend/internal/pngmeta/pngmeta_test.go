package pngmeta

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// writePNG puts a real, decodable PNG on disk. Real rather than a stub because
// the whole point of this package is that what it writes is still a PNG
// afterwards - a hand-rolled fixture could hide a chunk stream that only this
// package can read.
func writePNG(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	img.Set(1, 1, color.RGBA{R: 200, G: 30, B: 90, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	path := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func decodable(t *testing.T, path string) image.Image {
	t.Helper()
	file, err := os.Open(path) //nolint:gosec // test fixture
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer file.Close()
	img, err := png.Decode(file)
	if err != nil {
		t.Fatalf("the file stopped being a readable PNG: %v", err)
	}
	return img
}

func TestSet_RoundTripsAndLeavesTheImageReadable(t *testing.T) {
	path := writePNG(t)
	before := decodable(t, path)

	build := "com.example.App 1.2.3 (456) cdhash:d114bed635ed2eb91f5c"
	if err := Set(path, "ao-build", build); err != nil {
		t.Fatalf("set: %v", err)
	}

	got, ok := Get(path, "ao-build")
	if !ok || got != build {
		t.Fatalf("Get = %q, %v; want the build back verbatim", got, ok)
	}
	// The picture is the evidence. A fingerprint that costs the image would be
	// a straight downgrade, so this is not a nice-to-have assertion.
	after := decodable(t, path)
	if after.Bounds() != before.Bounds() {
		t.Fatalf("bounds changed: %v -> %v", before.Bounds(), after.Bounds())
	}
	if after.At(1, 1) != before.At(1, 1) {
		t.Fatalf("pixel changed: %v -> %v", before.At(1, 1), after.At(1, 1))
	}
}

// A second capture into the same file must not leave two answers to "which
// build is this", because a reader would then have to pick one.
func TestSet_ReplacesRatherThanAppends(t *testing.T) {
	path := writePNG(t)
	if err := Set(path, "ao-build", "first"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := Set(path, "ao-build", "second"); err != nil {
		t.Fatalf("re-set: %v", err)
	}
	if got, _ := Get(path, "ao-build"); got != "second" {
		t.Fatalf("Get = %q, want the newest value", got)
	}
	raw, err := os.ReadFile(path) //nolint:gosec // test fixture
	if err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(raw, []byte("ao-build")); n != 1 {
		t.Fatalf("the file carries %d ao-build chunks, want exactly 1", n)
	}
}

// Other keywords are somebody else's data and survive untouched.
func TestSet_KeepsOtherKeywords(t *testing.T) {
	path := writePNG(t)
	if err := Set(path, "Author", "a person"); err != nil {
		t.Fatalf("set author: %v", err)
	}
	if err := Set(path, "ao-build", "com.example.App"); err != nil {
		t.Fatalf("set build: %v", err)
	}
	if got, ok := Get(path, "Author"); !ok || got != "a person" {
		t.Fatalf("Author = %q, %v; want it left alone", got, ok)
	}
}

// Most evidence comes from somewhere else. Reading a build out of a file that
// never had one is an ordinary "no", never an error.
func TestGet_MissingKeywordIsNotAnError(t *testing.T) {
	if got, ok := Get(writePNG(t), "ao-build"); ok || got != "" {
		t.Fatalf("Get on a plain PNG = %q, %v; want empty and not ok", got, ok)
	}
	if got, ok := Get(filepath.Join(t.TempDir(), "nope.png"), "ao-build"); ok || got != "" {
		t.Fatalf("Get on a missing file = %q, %v; want empty and not ok", got, ok)
	}
}

// A screenshot that could not be annotated must still be a screenshot: the
// original bytes stay exactly as they were rather than being half-rewritten.
func TestSet_LeavesANonPNGUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not.png")
	original := []byte("this is a jpeg, honestly")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Set(path, "ao-build", "x"); err == nil {
		t.Fatal("Set on a non-PNG must fail rather than write something")
	}
	raw, err := os.ReadFile(path) //nolint:gosec // test fixture
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, original) {
		t.Fatalf("the file was modified: %q", raw)
	}
}

func TestSet_RejectsAKeywordTheSpecForbids(t *testing.T) {
	path := writePNG(t)
	for name, key := range map[string]string{
		"empty":         "",
		"leading space": " ao-build",
		"too long":      string(make([]byte, 80)),
	} {
		if err := Set(path, key, "x"); err == nil {
			t.Fatalf("%s keyword was accepted", name)
		}
	}
}
