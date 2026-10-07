package simcrash

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	deviceA = "AAAAAAAA-0000-0000-0000-000000000001"
	deviceB = "BBBBBBBB-0000-0000-0000-000000000002"
)

func incidents(reports []Report) []string {
	out := make([]string, 0, len(reports))
	for _, r := range reports {
		out = append(out, r.Header.IncidentID[len(r.Header.IncidentID)-1:])
	}
	return out
}

func TestList_OnlyThisDeviceNewestFirstByTheReportsOwnTime(t *testing.T) {
	reports, err := List("testdata", deviceA, "")
	if err != nil {
		t.Fatal(err)
	}
	// 1 = Nimbus 10:15, 3 = Lantern 09:30, 2 = Nimbus 09:10. Not 4 (another
	// device), not 5 (SpringBoard, no device), and not by file name.
	if got := incidents(reports); !slices.Equal(got, []string{"1", "3", "2"}) {
		t.Fatalf("List(A) = %v, want [1 3 2]", got)
	}
	if reports[0].Path != filepath.Join("testdata", "Nimbus-2026-10-08-101500.ips") {
		t.Errorf("path = %s", reports[0].Path)
	}
}

func TestList_TheUDIDMatchesInAnyCase(t *testing.T) {
	reports, err := List("testdata", strings.ToLower(deviceA), "")
	if err != nil || len(reports) != 3 {
		t.Fatalf("List(lowercase A) = %d reports, %v", len(reports), err)
	}
}

func TestList_ABundleIDNarrowsToThatApp(t *testing.T) {
	reports, err := List("testdata", deviceA, "COM.example.nimbus")
	if err != nil {
		t.Fatal(err)
	}
	if got := incidents(reports); !slices.Equal(got, []string{"1", "2"}) {
		t.Fatalf("List(A, Nimbus) = %v, want [1 2]", got)
	}
	reports, err = List("testdata", deviceB, "com.example.Nimbus")
	if err != nil {
		t.Fatal(err)
	}
	if got := incidents(reports); !slices.Equal(got, []string{"4"}) {
		t.Fatalf("List(B, Nimbus) = %v, want [4]", got)
	}
}

func TestList_NoDirectoryIsNoCrashes(t *testing.T) {
	reports, err := List(filepath.Join(t.TempDir(), "absent"), deviceA, "")
	if err != nil || len(reports) != 0 {
		t.Fatalf("got %v, %v", reports, err)
	}
}

func TestList_AnUnreadableBodyIsKeptWithWhyRatherThanDropped(t *testing.T) {
	dir := t.TempDir()
	body := `{"app_name":"Nimbus","timestamp":"2026-10-08 10:00:00.00 +0700","bundleID":"com.example.Nimbus"}` + "\n" +
		`{"procPath" : "/Devices/` + deviceA + `/data/Containers/Bundle/Application/X/Nimbus.app/Nimbus", "threads" : [` // truncated
	if err := os.WriteFile(filepath.Join(dir, "Nimbus-cut.ips"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	reports, err := List(dir, deviceA, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 || reports[0].Unreadable == "" {
		t.Fatalf("a report for this device with a cut-off body must still be listed, saying why: %+v", reports)
	}
}

func TestCrashedThread_IsTheFaultingOne(t *testing.T) {
	reports, _ := List("testdata", deviceA, "com.example.Nimbus")
	thread, ok := reports[0].CrashedThread()
	if !ok || thread.Name != "worker" {
		t.Fatalf("crashed thread = %+v %v, want the faulting `worker` thread", thread, ok)
	}
}

func TestFrameLine(t *testing.T) {
	reports, _ := List("testdata", deviceA, "com.example.Nimbus")
	r := reports[0]
	thread, _ := r.CrashedThread()
	got := []string{}
	for i, f := range thread.Frames {
		got = append(got, r.FrameLine(i, f))
	}
	want := []string{
		"#0  libswiftCore.dylib  _assertionFailure(_:_:file:line:flags:) + 156",
		"#1  Nimbus  ForecastStore.load() + 88  (ForecastStore.swift:42)",
		"#2  Nimbus + 9216",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("frames:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// An image with no name is named by its path; an index past the list is ???.
	if line := r.FrameLine(0, Frame{ImageIndex: 2, ImageOffset: 10}); line != "#0  libsystem_kernel.dylib + 10" {
		t.Errorf("unnamed image: %q", line)
	}
	if line := r.FrameLine(0, Frame{ImageIndex: 9, ImageOffset: 10}); line != "#0  ??? + 10" {
		t.Errorf("unknown image: %q", line)
	}
}

func TestASILines(t *testing.T) {
	reports, _ := List("testdata", deviceA, "com.example.Nimbus")
	got := reports[0].ASILines()
	if len(got) != 1 || got[0] != "libswiftCore.dylib: Nimbus/ForecastStore.swift:42: Fatal error: Unexpectedly found nil while unwrapping an Optional value" {
		t.Fatalf("asi = %q", got)
	}
	if lines := reports[1].ASILines(); len(lines) != 0 {
		t.Fatalf("a report without asi has asi lines: %q", lines)
	}
}

func TestHeaderFields(t *testing.T) {
	reports, _ := List("testdata", deviceA, "com.example.Nimbus")
	r := reports[1]
	if r.Header.AppVersion != "2.4.0" || r.Header.BuildVersion != "317" || r.Body.Exception.Type != "EXC_CRASH" ||
		r.Body.Exception.Signal != "SIGABRT" || r.Body.Termination.Indicator != "Abort trap: 6" || len(r.Body.LastExceptionBacktrace) != 2 {
		t.Fatalf("report = %+v", r)
	}
	if r.Time.Format("2006-01-02 15:04:05 -0700") != "2026-10-08 09:10:00 +0700" {
		t.Fatalf("time = %v", r.Time)
	}
}
