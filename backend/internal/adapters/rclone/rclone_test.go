package rclone

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The outputs below are what rclone 1.75.1 printed, read live: the NOTICE a
// Google Drive remote on rclone's shared client_id prints on every call, the
// JSON log of a copy, and the refusals for a missing folder and remote.
const (
	notice     = `2026/10/07 16:21:39 NOTICE: finnomena: This remote uses rclone's shared Google Drive client_id, which is being retired and will stop working during 2026. Create your own client_id to avoid interruption: https://rclone.org/drive/#making-your-own-client-id`
	jsonNotice = `{"time":"2026-10-07T16:21:57.43+07:00","level":"notice","msg":"This remote uses rclone's shared Google Drive client_id, which is being retired and will stop working during 2026. Create your own client_id to avoid interruption: https://rclone.org/drive/#making-your-own-client-id","object":"finnomena:","source":"drive/drive.go:1"}`
	copyLog    = jsonNotice + `
{"time":"2026-10-07T16:21:57.437576+07:00","level":"info","msg":"Copied (new)","size":2,"object":"TC-1 pass.png","objectType":"*drive.Object","source":"operations/copy.go:380"}
{"time":"2026-10-07T16:21:57.437624+07:00","level":"info","msg":"Copied (replaced existing)","size":2,"object":"README.md","objectType":"*drive.Object","source":"operations/copy.go:380"}
{"time":"2026-10-07T16:21:57.438361+07:00","level":"info","msg":"\nTransferred:   \t          4 B / 4 B, 100%, 0 B/s, ETA -\nTransferred:            2 / 2, 100%\n","stats":{"bytes":4,"transfers":2},"source":"accounting/stats.go:549"}
`
	lsjsonOut = `[
{"Path":"README.md","Name":"README.md","Size":2,"MimeType":"text/markdown","ModTime":"2026-10-07T09:21:57.000Z","IsDir":false,"ID":"1readme"},
{"Path":"TC-1 pass.png","Name":"TC-1 pass.png","Size":2,"MimeType":"image/png","ModTime":"2026-10-07T09:21:57.000Z","IsDir":false,"ID":"1tc1"}
]`
	missingDir    = "2026/10/07 16:21:49 ERROR : error listing: directory not found\n2026/10/07 16:21:49 NOTICE: Failed to lsjson with 2 errors: last error was: error in ListJSON: directory not found"
	missingRemote = `2026/10/07 16:21:49 CRITICAL: Failed to create file system for "nosuchremote:QA": didn't find section in config file ("nosuchremote")`
	expiredToken  = `2026/10/07 16:30:00 CRITICAL: Failed to create file system for "finnomena:QA": couldn't find root directory ID: Get "https://www.googleapis.com/drive/v3/files/root?alt=json": couldn't fetch token: invalid_grant: maybe token expired? - try refreshing with "rclone config reconnect finnomena:"`
)

type call struct {
	name string
	args []string
}

type fakeRclone struct {
	calls  []call
	answer func(args []string) (Output, error)
	// files is the --files-from-raw list as it was when rclone ran.
	files string
}

func (f *fakeRclone) run(_ context.Context, name string, args ...string) (Output, error) {
	f.calls = append(f.calls, call{name, args})
	for i, a := range args {
		if a == "--files-from-raw" && i+1 < len(args) {
			b, _ := os.ReadFile(args[i+1])
			f.files = string(b)
		}
	}
	return f.answer(args)
}

func onPath(string) (string, error) { return "/opt/homebrew/bin/rclone", nil }

func client(f *fakeRclone) *Client {
	return New(Options{LookPath: onPath, Runner: f.run})
}

func TestHasRemote(t *testing.T) {
	f := &fakeRclone{answer: func([]string) (Output, error) {
		return Output{Stdout: []byte("finnomena:\nmy drive:\n")}, nil
	}}
	c := client(f)
	for name, want := range map[string]bool{"finnomena": true, "my drive": true, "finno": false, "": false} {
		got, err := c.HasRemote(context.Background(), name)
		if err != nil || got != want {
			t.Errorf("HasRemote(%q) = %v, %v; want %v", name, got, err, want)
		}
	}
	if got := f.calls[0].args; !reflect.DeepEqual(got, []string{"listremotes"}) {
		t.Errorf("argv = %q, want listremotes", got)
	}
}

func TestCopySendsExactlyTheNamedFilesAndReportsWhatWasCopied(t *testing.T) {
	f := &fakeRclone{answer: func([]string) (Output, error) { return Output{Stderr: []byte(copyLog)}, nil }}
	got, err := client(f).Copy(context.Background(), "/Users/me/Desktop/QA Evidence/M/TR-1 - r", "finnomena:QA/M/TR-1 - r", []string{"README.md", "TC-1 pass.png"})
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if want := []string{"README.md", "TC-1 pass.png"}; !reflect.DeepEqual(got, want) {
		t.Errorf("copied = %q, want %q", got, want)
	}
	args := f.calls[0].args
	if args[0] != "copy" || args[1] != "/Users/me/Desktop/QA Evidence/M/TR-1 - r" || args[2] != "finnomena:QA/M/TR-1 - r" {
		t.Errorf("argv = %q", args)
	}
	if !strings.Contains(strings.Join(args, " "), "--use-json-log -v") {
		t.Errorf("argv = %q, want the JSON log at -v", args)
	}
	if f.files != "README.md\nTC-1 pass.png\n" {
		t.Errorf("--files-from-raw held %q", f.files)
	}
	for _, a := range args {
		if a == "link" || strings.Contains(a, "config") {
			t.Errorf("argv %q runs something other than a copy", args)
		}
	}
	listFile := args[slicesIndex(args, "--files-from-raw")+1]
	if _, err := os.Stat(listFile); !os.IsNotExist(err) {
		t.Errorf("the file list %s was left behind", listFile)
	}
}

func slicesIndex(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

func TestACopyThatFailsPartwayReportsWhatItCopied(t *testing.T) {
	log := `{"level":"info","msg":"Copied (new)","object":"README.md"}
{"level":"error","msg":"Failed to copy: googleapi: Error 503: backend error","object":"TC-1 pass.mp4"}`
	f := &fakeRclone{answer: func([]string) (Output, error) { return Output{Stderr: []byte(log), ExitCode: 5}, nil }}
	got, err := client(f).Copy(context.Background(), "/src", "finnomena:QA", []string{"README.md", "TC-1 pass.mp4"})
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "Error 503") {
		t.Fatalf("err = %v, want ErrUnavailable naming the failure", err)
	}
	if !reflect.DeepEqual(got, []string{"README.md"}) {
		t.Fatalf("copied = %q, want README.md", got)
	}
}

func TestCopyOfNothingNewReportsNothing(t *testing.T) {
	f := &fakeRclone{answer: func([]string) (Output, error) { return Output{Stderr: []byte(jsonNotice + "\n")}, nil }}
	got, err := client(f).Copy(context.Background(), "/src", "finnomena:QA", []string{"README.md"})
	if err != nil || len(got) != 0 {
		t.Fatalf("Copy = %q, %v; want nothing copied", got, err)
	}
}

func TestCopyRefusesANameTheFileListCannotHold(t *testing.T) {
	f := &fakeRclone{answer: func([]string) (Output, error) { return Output{}, nil }}
	if _, err := client(f).Copy(context.Background(), "/src", "finnomena:QA", []string{"a\nb.png"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if len(f.calls) != 0 {
		t.Fatal("rclone ran with a name it would read as two")
	}
}

func TestList(t *testing.T) {
	f := &fakeRclone{answer: func([]string) (Output, error) {
		return Output{Stdout: []byte(lsjsonOut), Stderr: []byte(notice)}, nil
	}}
	got, err := client(f).List(context.Background(), "finnomena:QA/M")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if want := []File{{"README.md", "1readme"}, {"TC-1 pass.png", "1tc1"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("List = %+v, want %+v", got, want)
	}
	if want := []string{"lsjson", "finnomena:QA/M", "--files-only"}; !reflect.DeepEqual(f.calls[0].args, want) {
		t.Errorf("argv = %q, want %q", f.calls[0].args, want)
	}
}

func TestFailuresMapToSentinels(t *testing.T) {
	cases := []struct {
		name   string
		out    Output
		want   error
		says   []string
		denies []string
	}{
		{
			name: "expired token on the shared client",
			out:  Output{Stderr: []byte(notice + "\n" + expiredToken), ExitCode: 1},
			want: ErrAuth,
			says: []string{"rclone config reconnect finnomena:", "shared Google client_id", "during 2026", "https://rclone.org/drive/#making-your-own-client-id"},
		},
		{
			name:   "revoked token on the person's own client",
			out:    Output{Stderr: []byte(`{"level":"critical","msg":"oauth2: cannot fetch token: 400 Bad Request\nResponse: {\"error\": \"invalid_grant\", \"error_description\": \"Token has been expired or revoked.\"}"}`), ExitCode: 7},
			want:   ErrAuth,
			says:   []string{"rclone config reconnect finnomena:"},
			denies: []string{"shared Google client_id"},
		},
		{
			name: "missing remote",
			out:  Output{Stderr: []byte(missingRemote), ExitCode: 1},
			want: ErrRemoteMissing,
		},
		{
			name:   "missing folder, with the notice that is no failure",
			out:    Output{Stderr: []byte(notice + "\n[\n" + missingDir), ExitCode: 3},
			want:   ErrUnavailable,
			says:   []string{"directory not found"},
			denies: []string{"client_id"},
		},
		{
			name:   "a case numbered 401 is no auth failure",
			out:    Output{Stderr: []byte(`{"level":"error","msg":"Failed to copy: googleapi: Error 503: backend error","object":"TC-401 pass.png"}`), ExitCode: 5},
			want:   ErrUnavailable,
			denies: []string{"reconnect"},
		},
	}
	for _, tc := range cases {
		f := &fakeRclone{answer: func([]string) (Output, error) { return tc.out, nil }}
		_, err := client(f).List(context.Background(), "finnomena:QA")
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
			continue
		}
		for _, s := range tc.says {
			if !strings.Contains(err.Error(), s) {
				t.Errorf("%s: %q does not say %q", tc.name, err, s)
			}
		}
		for _, s := range tc.denies {
			if strings.Contains(err.Error(), s) {
				t.Errorf("%s: %q says %q", tc.name, err, s)
			}
		}
	}
}

func TestBinaryFallsBackToHomebrewPaths(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "rclone")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	notOnPath := func(string) (string, error) { return "", errors.New("not found") }
	var ran string
	c := New(Options{LookPath: notOnPath, Fallbacks: []string{filepath.Join(dir, "missing"), bin}, Runner: func(_ context.Context, name string, _ ...string) (Output, error) {
		ran = name
		return Output{}, nil
	}})
	if _, err := c.HasRemote(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	if ran != bin {
		t.Errorf("ran %q, want the fallback %q", ran, bin)
	}

	c = New(Options{LookPath: notOnPath, Fallbacks: []string{filepath.Join(dir, "missing")}})
	if _, err := c.HasRemote(context.Background(), "x"); !errors.Is(err, ErrBinaryMissing) || !strings.Contains(err.Error(), "brew install rclone") {
		t.Errorf("err = %v, want ErrBinaryMissing with the install hint", err)
	}
}

func TestATimedOutCopyIsUnavailable(t *testing.T) {
	c := New(Options{LookPath: onPath, CopyTimeout: time.Millisecond, Runner: func(ctx context.Context, _ string, _ ...string) (Output, error) {
		<-ctx.Done()
		return Output{}, ctx.Err()
	}})
	_, err := c.Copy(context.Background(), "/src", "finnomena:QA", []string{"README.md"})
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want a timeout", err)
	}
}
