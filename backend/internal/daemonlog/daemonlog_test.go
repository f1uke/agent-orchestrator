package daemonlog

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenPutsTheLogUnderTheDataDir(t *testing.T) {
	dataDir := t.TempDir()
	r, err := Open(dataDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	if want := filepath.Join(dataDir, "logs", "daemon.log"); r.Path() != want {
		t.Fatalf("Path = %q, want %q", r.Path(), want)
	}
	if _, err := r.Write([]byte("hello\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := os.ReadFile(Path(dataDir))
	if err != nil || string(got) != "hello\n" {
		t.Fatalf("log file = %q, %v; want the line written", got, err)
	}
}

func TestOpenAppendsToAnEarlierRunsLog(t *testing.T) {
	dataDir := t.TempDir()
	for _, line := range []string{"first run\n", "second run\n"} {
		r, err := Open(dataDir)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if _, err := r.Write([]byte(line)); err != nil {
			t.Fatalf("Write: %v", err)
		}
		_ = r.Close()
	}
	got, _ := os.ReadFile(Path(dataDir))
	if string(got) != "first run\nsecond run\n" {
		t.Fatalf("log = %q, want both runs kept", got)
	}
}

func TestRotationKeepsWholeLinesAndABoundedNumberOfGenerations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "daemon.log")
	r, err := open(path, 20, 2)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	for _, line := range []string{"aaaaaaaaaaaaaaa\n", "bbbbbbbbbbbbbbb\n", "ccccccccccccccc\n", "ddddddddddddddd\n"} {
		if _, err := r.Write([]byte(line)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	want := map[string]string{
		path:        "ddddddddddddddd\n",
		path + ".1": "ccccccccccccccc\n",
		path + ".2": "bbbbbbbbbbbbbbb\n",
	}
	for p, w := range want {
		got, err := os.ReadFile(p)
		if err != nil || string(got) != w {
			t.Fatalf("%s = %q, %v; want %q", filepath.Base(p), got, err, w)
		}
	}
	if _, err := os.Stat(path + ".3"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a third generation exists (%v), want only 2 kept", err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

// The app's stderr pipe breaking must not stop the file from being written.
func TestTeeKeepsWritingPastABrokenDestination(t *testing.T) {
	var b strings.Builder
	w := Tee(failingWriter{}, &b)
	if n, err := w.Write([]byte("line\n")); err != nil || n != 5 {
		t.Fatalf("Write = %d, %v; want the write accepted", n, err)
	}
	if b.String() != "line\n" {
		t.Fatalf("second destination got %q, want the line", b.String())
	}
}
