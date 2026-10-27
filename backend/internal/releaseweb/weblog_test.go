package releaseweb

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openWebLog(t *testing.T, p Paths, content string) *os.File {
	t.Helper()
	if err := os.WriteFile(p.WebLog(), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(p.WebLog(), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func sizeOf(t *testing.T, path string) int64 {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.Size()
}

func TestWebLogOverTheCapMovesToDotOneAtStart(t *testing.T) {
	p := Paths{Dir: t.TempDir()}
	old := strings.Repeat("old line\n", 20)
	f := openWebLog(t, p, old)

	newWebLogWriter(p, f, 100)

	if b, _ := os.ReadFile(p.WebLog() + ".1"); string(b) != old {
		t.Errorf("web.log.1 holds %d bytes, want the %d bytes web.log had", len(b), len(old))
	}
	if n := sizeOf(t, p.WebLog()); n != 0 {
		t.Errorf("web.log holds %d bytes after start, want 0", n)
	}
}

func TestWebLogStaysUnderTheCapWhileRunning(t *testing.T) {
	p := Paths{Dir: t.TempDir()}
	f := openWebLog(t, p, "")
	const limit = 200
	w := newWebLogWriter(p, f, limit)
	var last string
	for i := range 100 {
		last = fmt.Sprintf("poll failed, attempt %03d\n", i)
		if _, err := w.Write([]byte(last)); err != nil {
			t.Fatal(err)
		}
		if n := sizeOf(t, p.WebLog()); n > limit {
			t.Fatalf("web.log grew to %d bytes after write %d, cap %d", n, i+1, limit)
		}
	}
	if n := sizeOf(t, p.WebLog()+".1"); n == 0 || n > limit {
		t.Errorf("web.log.1 holds %d bytes, want the previous part, at most %d", n, limit)
	}
	if b, _ := os.ReadFile(p.WebLog()); !bytes.HasSuffix(b, []byte(last)) {
		t.Errorf("web.log does not end with the newest line %q", last)
	}
}

func TestWebLogLeavesAnyOtherOutputAlone(t *testing.T) {
	p := Paths{Dir: t.TempDir()}
	full := strings.Repeat("x", 500)
	openWebLog(t, p, full)
	other, err := os.Create(filepath.Join(t.TempDir(), "terminal"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })

	if w := newWebLogWriter(p, other, 100); w != other {
		t.Errorf("an output that is not web.log came back wrapped: %T", w)
	}
	if n := sizeOf(t, p.WebLog()); n != int64(len(full)) {
		t.Errorf("web.log was touched: %d bytes, want %d", n, len(full))
	}
}
