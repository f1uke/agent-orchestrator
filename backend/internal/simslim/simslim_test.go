package simslim

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// recorder is a simctl.Runner that remembers what it was asked to run and
// answers with whatever the test decided, so every path here is exercised
// without Xcode, a mac or a device.
type recorder struct {
	mu    sync.Mutex
	args  [][]string
	reply func(args []string) ([]byte, error)
}

func (r *recorder) run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	r.args = append(r.args, append([]string{name}, args...))
	reply := r.reply
	r.mu.Unlock()
	if reply == nil {
		return nil, nil
	}
	return reply(args)
}

func (r *recorder) calls() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]string, len(r.args))
	copy(out, r.args)
	return out
}

func found(string) (string, error) { return "/usr/local/bin/simslim", nil }

func missing(string) (string, error) { return "", errors.New("executable file not found in $PATH") }

const testUDID = "4754DB41-86C8-4326-81A7-172DDD41D5DA"

var testProfile = Profile{Keep: []string{"com.apple.apsd", "com.apple.swcd"}}

// A device already matching the profile must NOT be rebooted: `simslim on`
// reboots every time it runs, even when nothing changes, so calling it
// unconditionally would add a second reboot to every boot forever.
func TestApply_VerifyPassesLeavesTheDeviceAlone(t *testing.T) {
	rec := &recorder{}
	got := Apply(context.Background(), found, rec.run, testUDID, testProfile)

	if got.Outcome != Already {
		t.Fatalf("outcome = %q, want %q", got.Outcome, Already)
	}
	calls := rec.calls()
	if len(calls) != 1 {
		t.Fatalf("ran %d commands, want exactly 1 (verify): %v", len(calls), calls)
	}
	if calls[0][1] != "verify" {
		t.Fatalf("first command was %q, want verify", calls[0][1])
	}
	for _, c := range calls {
		if c[1] == "on" {
			t.Fatalf("`simslim on` ran on a device that already matched: %v", calls)
		}
	}
}

func TestApply_VerifyReportsDriftSoItSlims(t *testing.T) {
	rec := &recorder{reply: func(args []string) ([]byte, error) {
		if args[0] == "verify" {
			return []byte("drift: 3 daemons enabled that should not be"), errors.New("exit status 1")
		}
		return nil, nil
	}}

	got := Apply(context.Background(), found, rec.run, testUDID, testProfile)

	if got.Outcome != Applied {
		t.Fatalf("outcome = %q, want %q (reason %q)", got.Outcome, Applied, got.Reason)
	}
	calls := rec.calls()
	if len(calls) != 2 || calls[1][1] != "on" {
		t.Fatalf("want verify then on, got %v", calls)
	}
}

func TestApply_PassesTheKeepListToBothCommands(t *testing.T) {
	rec := &recorder{reply: func(args []string) ([]byte, error) {
		if args[0] == "verify" {
			return nil, errors.New("exit status 1")
		}
		return nil, nil
	}}

	Apply(context.Background(), found, rec.run, testUDID, testProfile)

	for _, c := range rec.calls() {
		joined := strings.Join(c, " ")
		if !strings.Contains(joined, "--keep com.apple.apsd,com.apple.swcd") {
			t.Fatalf("command missing the keep list: %q", joined)
		}
		if !strings.Contains(joined, testUDID) {
			t.Fatalf("command missing the udid: %q", joined)
		}
	}
}

// An empty (but present) Keep means a fully slim device, which simslim spells
// as `on` with no --keep at all.
func TestApply_EmptyKeepSendsNoKeepFlag(t *testing.T) {
	rec := &recorder{reply: func(args []string) ([]byte, error) {
		if args[0] == "verify" {
			return nil, errors.New("exit status 1")
		}
		return nil, nil
	}}

	Apply(context.Background(), found, rec.run, testUDID, Profile{})

	for _, c := range rec.calls() {
		if strings.Contains(strings.Join(c, " "), "--keep") {
			t.Fatalf("empty Keep still sent --keep: %v", c)
		}
	}
}

func TestApply_WithoutTheBinaryRunsNothingAndSaysSkipped(t *testing.T) {
	rec := &recorder{}
	got := Apply(context.Background(), missing, rec.run, testUDID, testProfile)

	if got.Outcome != Skipped {
		t.Fatalf("outcome = %q, want %q", got.Outcome, Skipped)
	}
	if got.Reason == "" {
		t.Fatal("Skipped carried no reason; a stock device must say why")
	}
	// The reason is the bare fact and nothing more. Every surface that prints
	// it already says the device is stock in its own words, so a reason that
	// says it too renders as "is stock ... so this device is stock".
	if strings.Contains(got.Reason, "stock") {
		t.Fatalf("reason = %q; it repeats the word every caller already prints around it", got.Reason)
	}
	if n := len(rec.calls()); n != 0 {
		t.Fatalf("ran %d commands without the binary, want 0", n)
	}
}

func TestApply_FailureCarriesTheToolsOwnWords(t *testing.T) {
	rec := &recorder{reply: func(args []string) ([]byte, error) {
		if args[0] == "verify" {
			return nil, errors.New("exit status 1")
		}
		return []byte("context deadline exceeded while disabling 170 services"), errors.New("exit status 1")
	}}

	got := Apply(context.Background(), found, rec.run, testUDID, testProfile)

	if got.Outcome != Failed {
		t.Fatalf("outcome = %q, want %q", got.Outcome, Failed)
	}
	if !strings.Contains(got.Reason, "context deadline exceeded") {
		t.Fatalf("reason = %q, want the tool's own output", got.Reason)
	}
}

func TestApply_FailureWithNoOutputStillExplainsItself(t *testing.T) {
	rec := &recorder{reply: func(args []string) ([]byte, error) {
		return nil, errors.New("exit status 2")
	}}

	got := Apply(context.Background(), found, rec.run, testUDID, testProfile)

	if got.Outcome != Failed || got.Reason == "" {
		t.Fatalf("got %+v, want Failed with a reason", got)
	}
}
