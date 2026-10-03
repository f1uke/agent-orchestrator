package simtrust

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

const testUDID = "4754DB41-86C8-4326-81A7-172DDD41D5DA"

// recorder is a simctl.Runner that remembers what it was asked and answers with
// whatever the test decided, so nothing here needs Xcode or a device.
type recorder struct {
	mu    sync.Mutex
	calls [][]string
	reply func(args []string) ([]byte, error)
}

func (r *recorder) run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	r.calls = append(r.calls, append([]string{name}, args...))
	reply := r.reply
	r.mu.Unlock()
	if reply == nil {
		return nil, nil
	}
	return reply(args)
}

func writeCA(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("-----BEGIN CERTIFICATE-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The command is the one proven to work against a live simulator: the device
// is named by udid and the file by absolute path.
func TestInstall_AddsEachExistingFileAsARootCert(t *testing.T) {
	dir := t.TempDir()
	ca := writeCA(t, dir, "proxy-ca.pem")
	rec := &recorder{}

	got := Install(context.Background(), rec.run, testUDID, []string{ca})

	want := [][]string{{"xcrun", "simctl", "keychain", testUDID, "add-root-cert", ca}}
	if !reflect.DeepEqual(rec.calls, want) {
		t.Fatalf("ran %v, want %v", rec.calls, want)
	}
	if !reflect.DeepEqual(got.Trusted, []string{ca}) || len(got.Failed) != 0 {
		t.Fatalf("result = %+v, want %s trusted", got, ca)
	}
}

// The default names Proxyman's CA, and a Mac without Proxyman must hear
// nothing about it: no command, no warning, an empty result.
func TestInstall_MissingFileIsSkippedSilently(t *testing.T) {
	rec := &recorder{}
	got := Install(context.Background(), rec.run, testUDID, []string{filepath.Join(t.TempDir(), "absent.pem")})

	if len(rec.calls) != 0 {
		t.Fatalf("ran %v for a file that does not exist", rec.calls)
	}
	if !got.Empty() {
		t.Fatalf("result = %+v, want empty", got)
	}
}

// One CA that will not install must not stop the others, and must say why in
// simctl's own words.
func TestInstall_AFailureIsReportedAndTheRestStillRun(t *testing.T) {
	dir := t.TempDir()
	bad, good := writeCA(t, dir, "bad.pem"), writeCA(t, dir, "good.pem")
	rec := &recorder{reply: func(args []string) ([]byte, error) {
		if args[len(args)-1] == bad {
			return []byte("Unable to add root certificate: invalid format"), errors.New("exit status 1")
		}
		return nil, nil
	}}

	got := Install(context.Background(), rec.run, testUDID, []string{bad, good})

	if !reflect.DeepEqual(got.Trusted, []string{good}) {
		t.Fatalf("trusted = %v, want only %s", got.Trusted, good)
	}
	if len(got.Failed) != 1 || got.Failed[0].File != bad ||
		!strings.Contains(got.Failed[0].Reason, "invalid format") {
		t.Fatalf("failed = %+v, want %s with simctl's reason", got.Failed, bad)
	}
}

func TestExpand_ResolvesHomeDropsBlanksAndDuplicates(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	got := Expand([]string{"~/ca.pem", " ", "", home + "/ca.pem", "/etc/../etc/x.pem"})
	want := []string{filepath.Join(home, "ca.pem"), "/etc/x.pem"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Expand = %v, want %v", got, want)
	}
}

func TestPresent_SaysPerEntryWhetherTheFileExists(t *testing.T) {
	dir := t.TempDir()
	ca := writeCA(t, dir, "ca.pem")
	got := Present([]string{ca, filepath.Join(dir, "absent.pem"), ""})
	if want := []bool{true, false, false}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Present = %v, want %v", got, want)
	}
}

// The Truster's memory is the only place a boot's trust pass can be read back
// from, so it has to be keyed the way every other device map is - by the
// normalized udid - and it has to hold the latest pass, not the first.
func TestTruster_RemembersEachDevicesLastPass(t *testing.T) {
	dir := t.TempDir()
	ca := writeCA(t, dir, "ca.pem")
	tr := NewTruster((&recorder{}).run)
	at := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	tr.now = func() time.Time { return at }

	tr.Apply(context.Background(), strings.ToLower(testUDID), "", Request{Files: []string{ca}})
	got, ok := tr.Last(testUDID)
	if !ok || !reflect.DeepEqual(got.Trusted, []string{ca}) || !got.At.Equal(at) {
		t.Fatalf("Last = %+v, %v; want %s trusted at %s", got, ok, ca, at)
	}

	tr.Apply(context.Background(), testUDID, "", Request{})
	if got, _ := tr.Last(testUDID); !got.Empty() {
		t.Fatalf("Last after an empty pass = %+v, want it to replace the earlier one", got)
	}
}

// "AO could not tell what to trust" must not read as "nothing to trust".
func TestTruster_ARequestErrorIsAFailureNotSilence(t *testing.T) {
	rec := &recorder{}
	tr := NewTruster(rec.run)

	got := tr.Apply(context.Background(), testUDID, "", Request{Err: errors.New("project 7 is degraded")})

	if len(rec.calls) != 0 {
		t.Fatalf("ran %v without knowing what to trust", rec.calls)
	}
	if len(got.Failed) != 1 || got.Failed[0].File != "" || !strings.Contains(got.Failed[0].Reason, "project 7 is degraded") {
		t.Fatalf("result = %+v, want one failure carrying the resolver's error", got)
	}
}

func countInstalls(rec *recorder) int {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return len(rec.calls)
}

// A claim is renewed every few minutes, and each install costs about a second.
// Within one boot of a device, a CA whose content is already trusted is
// reported as trusted without asking simctl again.
func TestTruster_SkipsACAThisBootAlreadyTrusts(t *testing.T) {
	ca := writeCA(t, t.TempDir(), "ca.pem")
	rec := &recorder{}
	tr := NewTruster(rec.run)

	first := tr.Apply(context.Background(), testUDID, "boot-1", Request{Files: []string{ca}})
	second := tr.Apply(context.Background(), testUDID, "boot-1", Request{Files: []string{ca}})

	if n := countInstalls(rec); n != 1 {
		t.Fatalf("ran simctl %d times in one boot, want 1", n)
	}
	if !reflect.DeepEqual(first.Trusted, []string{ca}) || !reflect.DeepEqual(second.Trusted, []string{ca}) {
		t.Fatalf("trusted = %v then %v, want %s both times - a skipped install is still a trusted CA", first.Trusted, second.Trusted, ca)
	}
}

// What would take a root away again: a new boot (an erase needs one), or a
// proxy regenerating its CA under the same path. Either installs again.
func TestTruster_InstallsAgainOnANewBootOrANewCA(t *testing.T) {
	dir := t.TempDir()
	ca := writeCA(t, dir, "ca.pem")
	rec := &recorder{}
	tr := NewTruster(rec.run)

	tr.Apply(context.Background(), testUDID, "boot-1", Request{Files: []string{ca}})
	tr.Apply(context.Background(), testUDID, "boot-2", Request{Files: []string{ca}})
	if n := countInstalls(rec); n != 2 {
		t.Fatalf("ran simctl %d times across two boots, want 2", n)
	}

	if err := os.WriteFile(ca, []byte("-----BEGIN CERTIFICATE-----\nregenerated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tr.Apply(context.Background(), testUDID, "boot-2", Request{Files: []string{ca}})
	if n := countInstalls(rec); n != 3 {
		t.Fatalf("ran simctl %d times, want a third install for the regenerated CA", n)
	}
}

// A caller that cannot name the boot gets no shortcut: it installs every time.
func TestTruster_WithoutABootNameAlwaysInstalls(t *testing.T) {
	ca := writeCA(t, t.TempDir(), "ca.pem")
	rec := &recorder{}
	tr := NewTruster(rec.run)

	tr.Apply(context.Background(), testUDID, "", Request{Files: []string{ca}})
	tr.Apply(context.Background(), testUDID, "", Request{Files: []string{ca}})
	if n := countInstalls(rec); n != 2 {
		t.Fatalf("ran simctl %d times, want 2", n)
	}
}

// A failed install is not remembered as done, so the next claim tries again.
func TestTruster_AFailedInstallIsRetriedOnTheNextPass(t *testing.T) {
	ca := writeCA(t, t.TempDir(), "ca.pem")
	fail := true
	rec := &recorder{reply: func([]string) ([]byte, error) {
		if fail {
			return []byte("device busy"), errors.New("exit status 1")
		}
		return nil, nil
	}}
	tr := NewTruster(rec.run)

	tr.Apply(context.Background(), testUDID, "boot-1", Request{Files: []string{ca}})
	fail = false
	got := tr.Apply(context.Background(), testUDID, "boot-1", Request{Files: []string{ca}})
	if n := countInstalls(rec); n != 2 || !reflect.DeepEqual(got.Trusted, []string{ca}) {
		t.Fatalf("ran %d times, trusted %v; want the retry to install %s", n, got.Trusted, ca)
	}
}
