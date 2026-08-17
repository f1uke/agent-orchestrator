package simlog

import (
	"os"
	"testing"
)

func TestRealCapture(t *testing.T) {
	path := os.Getenv("SIMLOG_CAPTURE")
	if path == "" {
		t.Skip("no capture")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var withProc, blankProc int
	scan, err := Read(f, Filter{}, func(e Entry) error {
		if e.Process == "" {
			blankProc++
		} else {
			withProc++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("entries=%d withProc=%d blank=%d topProcs=%v", scan.Entries, withProc, blankProc, scan.Processes[:5])
}
