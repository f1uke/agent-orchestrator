package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

// The failure this covers was found on a real device, not in a test: an
// interrupted `ao sim log --follow` did not return. Killing the child is not
// enough to end the read, because `simctl spawn` hands the same descriptor to
// the process it starts INSIDE the simulator - which is a child of the guest's
// launchd and survives. The write end stays open, no EOF ever arrives, and a
// reader waiting for one waits forever.
//
// Here the test plays the part of that third process: it holds the write end
// while the child is gone. Close must still end the read.
func TestProcessStream_CloseEndsAReadSomethingElseKeepsOpen(t *testing.T) {
	readFrom, writeTo, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writeTo.Close() }() // still open while Close runs: that is the point

	// A child that exits immediately: this is about the descriptor, not about
	// whether the process is alive. Re-running the test binary with a pattern
	// that matches nothing is the portable way to get one.
	cmd := exec.CommandContext(context.Background(), os.Args[0], "-test.run=^$")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	stream := &processStream{cmd: cmd, out: readFrom, stderr: &bytes.Buffer{}}

	reads := make(chan error, 1)
	go func() {
		buf := make([]byte, 16)
		_, readErr := stream.Read(buf)
		reads <- readErr
	}()
	// Nothing is coming, and nothing ever will.
	select {
	case err := <-reads:
		t.Fatalf("the read ended on its own with %v; this test proves nothing", err)
	case <-time.After(100 * time.Millisecond):
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	select {
	case <-reads:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not end the read: `ao sim log --follow` would hang instead of stopping")
	}
	// A child this process stopped on purpose is not a failure to report.
	if err := stream.Err(); err != nil {
		t.Fatalf("Err = %v, want nil for a stream that was stopped deliberately", err)
	}
}

func TestProcessStream_ReportsAChildThatFailedWithWhatItSaid(t *testing.T) {
	stream, err := startProcessStream(context.Background(), os.Args[0], "-test.run=^$", "-test.bogus-flag")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	buf := make([]byte, 4096)
	for {
		if _, readErr := stream.Read(buf); readErr != nil {
			break
		}
	}
	if stream.Err() == nil {
		t.Fatal("a child that exited non-zero must be reported, not read as a clean end")
	}
}
