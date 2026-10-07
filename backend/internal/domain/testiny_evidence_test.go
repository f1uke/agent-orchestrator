package domain

import (
	"errors"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestParseTestinyEvidenceFile(t *testing.T) {
	cases := []struct {
		name string
		want TestinyEvidenceFile
	}{
		{"TC-2124 pass.png", TestinyEvidenceFile{CaseID: 2124, Verdict: TestinyEvidencePass}},
		{"TC-2130 FAIL MOBILITY-4533.mp4", TestinyEvidenceFile{CaseID: 2130, Verdict: TestinyEvidenceFail, JiraKey: "MOBILITY-4533"}},
		{"TC-2131 pass - iPhone 15 iOS 18.png", TestinyEvidenceFile{CaseID: 2131, Verdict: TestinyEvidencePass, Device: "iPhone 15 iOS 18"}},
		{"TC-2131 pass - iPhone 15 iOS 18.2.mov", TestinyEvidenceFile{CaseID: 2131, Verdict: TestinyEvidencePass, Device: "iPhone 15 iOS 18.2"}},
		{"TC-7 FAIL STAR_2-15 - Pixel 8 - Android 14.MP4", TestinyEvidenceFile{CaseID: 7, Verdict: TestinyEvidenceFail, JiraKey: "STAR_2-15", Device: "Pixel 8 - Android 14"}},
		{"TC-9 pass - Galaxy S24 (แอนดรอยด์).jpg", TestinyEvidenceFile{CaseID: 9, Verdict: TestinyEvidencePass, Device: "Galaxy S24 (แอนดรอยด์)"}},
	}
	for _, tc := range cases {
		got, err := ParseTestinyEvidenceFile(tc.name)
		if err != nil {
			t.Errorf("ParseTestinyEvidenceFile(%q): %v", tc.name, err)
			continue
		}
		tc.want.Name = tc.name
		if got != tc.want {
			t.Errorf("ParseTestinyEvidenceFile(%q) = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestParseTestinyEvidenceFileRefusals(t *testing.T) {
	for name, why := range map[string]string{
		"README.md":                        "TC-<case id>",
		"TC-2124 PASS.png":                 "lower case",
		"TC-2124 Pass.png":                 "lower case",
		"TC-2130 fail MOBILITY-4533.mp4":   "upper case",
		"TC-2130 Fail MOBILITY-4533.mp4":   "upper case",
		"TC-2130 FAIL.mp4":                 "no Jira key",
		"TC-2130 FAIL - iPhone.mp4":        "no Jira key",
		"TC-2130 FAIL MOB.mp4":             "not a Jira key",
		"TC-2130 FAIL mobility-4533.mp4":   "not a Jira key",
		"TC-2130 FAIL 4533.mp4":            "not a Jira key",
		"TC-2130 FAIL MOBILITY-0.mp4":      "not a Jira key",
		"TC-2130 FAIL MOBILITY-4533 x.mp4": "after the Jira key",
		"TC-2124 pass iPhone.png":          `after "pass"`,
		"TC-2124 pass - .png":              "no device",
		"TC-2124 pass":                     "extension",
		"TC-2124 pass.":                    "extension",
		"TC-2124.png":                      "no verdict",
		"TC-2124  pass.png":                "no verdict",
		"TC-2124 passed.png":               "no verdict",
		"tc-2124 pass.png":                 "TC-<case id>",
		"TC-0 pass.png":                    "TC-<case id>",
		"TC-0124 pass.png":                 "TC-<case id>",
		"TC-99999999999999999999 pass.png": "TC-<case id>",
		"2124 pass.png":                    "TC-<case id>",
		".png":                             "extension",
		"TC-2124 pass.p ng":                "extension",
		"TC-2124 FAIL MOBILITY-4533-x.mp4": "not a Jira key",
		"TC-2124 FAIL MOBILITY-4533 .mp4":  "after the Jira key",
	} {
		_, err := ParseTestinyEvidenceFile(name)
		if !errors.Is(err, ErrBadTestinyEvidence) {
			t.Errorf("ParseTestinyEvidenceFile(%q) = %v, want ErrBadTestinyEvidence", name, err)
			continue
		}
		if !strings.Contains(err.Error(), why) {
			t.Errorf("ParseTestinyEvidenceFile(%q) = %q, want it to say %q", name, err, why)
		}
	}
}

func evidenceEntries(t *testing.T, files fstest.MapFS) []fs.DirEntry {
	t.Helper()
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestCheckTestinyEvidenceFolder(t *testing.T) {
	folder := fstest.MapFS{
		"README.md":                            {},
		".DS_Store":                            {},
		"TC-1 pass.png":                        {},
		"TC-1 pass - iPhone 15.png":            {},
		"TC-2 FAIL MOBILITY-4533.mp4":          {},
		".hidden/TC-9 pass.png":                {},
		"TC-3 pass - Pixel 8 - Android 14.png": {},
	}
	files, err := CheckTestinyEvidenceFolder(evidenceEntries(t, folder), map[int64]bool{1: true, 2: true, 3: true})
	if err != nil {
		t.Fatalf("CheckTestinyEvidenceFolder: %v", err)
	}
	var names []string
	for _, f := range files {
		names = append(names, f.Name)
	}
	want := []string{"TC-1 pass - iPhone 15.png", "TC-1 pass.png", "TC-2 FAIL MOBILITY-4533.mp4", "TC-3 pass - Pixel 8 - Android 14.png"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("files = %q, want %q (README.md and dotfiles left out)", names, want)
	}
}

func TestCheckTestinyEvidenceFolderReportsEveryProblem(t *testing.T) {
	folder := fstest.MapFS{
		"TC-1 PASS.png":      {},
		"TC-2 pass.png":      {},
		"TC-9 pass.png":      {},
		"notes.txt":          {},
		"old/TC-1 pass.png":  {},
		"TC-3 link.png":      {Mode: fs.ModeSymlink},
		"TC-4 FAIL.mp4":      {},
		".DS_Store":          {},
		"README.md.orig.txt": {},
	}
	_, err := CheckTestinyEvidenceFolder(evidenceEntries(t, folder), map[int64]bool{1: true, 2: true, 3: true, 4: true})
	if !errors.Is(err, ErrBadTestinyEvidence) {
		t.Fatalf("err = %v, want ErrBadTestinyEvidence", err)
	}
	for _, want := range []string{
		"README.md is missing",
		`"TC-1 PASS.png" writes pass in the wrong case`,
		`"TC-9 pass.png" is for TC-9, which is not in the run`,
		`"notes.txt" does not start with TC-<case id>`,
		`"old" is a folder`,
		`"TC-3 link.png" is not a regular file`,
		`"TC-4 FAIL.mp4" gives no Jira key`,
		`"README.md.orig.txt" does not start with TC-<case id>`,
		TestinyEvidenceNaming,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not say %q:\n%s", want, err)
		}
	}
	if strings.Contains(err.Error(), "TC-2 pass.png") || strings.Contains(err.Error(), ".DS_Store") {
		t.Errorf("error names a file that is fine:\n%s", err)
	}
}

func TestCheckTestinyEvidenceFolderNeedsOnlyTheReadme(t *testing.T) {
	files, err := CheckTestinyEvidenceFolder(evidenceEntries(t, fstest.MapFS{"README.md": {}}), nil)
	if err != nil || len(files) != 0 {
		t.Fatalf("CheckTestinyEvidenceFolder(README only) = %v, %v; want no files and no error", files, err)
	}
}

func TestNewTestinyEvidenceTree(t *testing.T) {
	tree, err := NewTestinyEvidenceTree("MOBILITY", 2026,
		TestinyRef{ID: 73, Title: "Sprint 2026-19"}, TestinyRef{ID: 7, Title: "ECoupon on webview"},
		191, "MOBILITY-4533 ECoupon on webview - iOS")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := tree.Rel(), "MOBILITY/2026/Sprint 2026-19/TP-7 - ECoupon on webview/TR-191 - MOBILITY-4533 ECoupon on webview - iOS"; got != want {
		t.Fatalf("Rel() = %q, want %q", got, want)
	}
}

func TestNewTestinyEvidenceTreeRefusesNamesThatAreNotOneFolder(t *testing.T) {
	ms, plan := TestinyRef{ID: 73, Title: "Sprint 2026-19"}, TestinyRef{ID: 7, Title: "Plan"}
	for name, build := range map[string]func() (TestinyEvidenceTree, error){
		"run title with /": func() (TestinyEvidenceTree, error) {
			return NewTestinyEvidenceTree("MOBILITY", 2026, ms, plan, 191, "iOS / Android")
		},
		"plan title with /": func() (TestinyEvidenceTree, error) {
			return NewTestinyEvidenceTree("MOBILITY", 2026, ms, TestinyRef{ID: 7, Title: "a/b"}, 191, "run")
		},
		"milestone title with /": func() (TestinyEvidenceTree, error) {
			return NewTestinyEvidenceTree("MOBILITY", 2026, TestinyRef{ID: 73, Title: "Sprint 19/20"}, plan, 191, "run")
		},
		"empty milestone": func() (TestinyEvidenceTree, error) {
			return NewTestinyEvidenceTree("MOBILITY", 2026, TestinyRef{ID: 73, Title: " "}, plan, 191, "run")
		},
		"hidden project": func() (TestinyEvidenceTree, error) {
			return NewTestinyEvidenceTree(".MOBILITY", 2026, ms, plan, 191, "run")
		},
		"NUL in run title": func() (TestinyEvidenceTree, error) {
			return NewTestinyEvidenceTree("MOBILITY", 2026, ms, plan, 191, "a\x00b")
		},
	} {
		if _, err := build(); !errors.Is(err, ErrBadTestinyEvidence) || !strings.Contains(err.Error(), "change it in Testiny") {
			t.Errorf("%s: err = %v, want ErrBadTestinyEvidence asking to change it in Testiny", name, err)
		}
	}
}

func TestTestinyEvidenceYear(t *testing.T) {
	bangkok := time.FixedZone("ICT", 7*3600)
	start := time.Date(2026, 12, 31, 17, 0, 0, 0, time.UTC)
	created := time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)
	if got := TestinyEvidenceYear(start, created, bangkok); got != 2027 {
		t.Errorf("year of a sprint starting 2027-01-01 in Bangkok = %d, want 2027", got)
	}
	if got := TestinyEvidenceYear(start, created, time.UTC); got != 2026 {
		t.Errorf("year in UTC = %d, want 2026", got)
	}
	if got := TestinyEvidenceYear(time.Time{}, created, bangkok); got != 2025 {
		t.Errorf("year with no start = %d, want the created year 2025", got)
	}
}

func TestDriveFileID(t *testing.T) {
	for in, want := range map[string]string{
		"https://drive.google.com/file/d/1U1WtcGqCfBu9YMh2GZ9LKyzcZSrL05R6/view?usp=drive_link": "1U1WtcGqCfBu9YMh2GZ9LKyzcZSrL05R6",
		"https://drive.google.com/file/d/1a-b_C/view":                                           "1a-b_C",
		"https://drive.google.com/file/d/1abc":                                                  "1abc",
		"https://drive.google.com/file/u/0/d/1abc/view":                                         "1abc",
		"https://drive.google.com/open?id=1abc":                                                 "1abc",
		"https://drive.google.com/uc?id=1abc&export=download":                                   "1abc",
		" http://drive.google.com/file/d/1abc/view ":                                            "1abc",
	} {
		if got, ok := DriveFileID(in); !ok || got != want {
			t.Errorf("DriveFileID(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{
		"",
		"https://drive.google.com/drive/folders/1abc",
		"https://drive.google.com/open?id=",
		"https://drive.google.com/open?id=a/b",
		"https://docs.google.com/document/d/1abc/edit",
		"https://drive.google.com.evil.example/file/d/1abc/view",
		"https://app.testiny.io/MOB/testruns/tr/565",
		"ftp://drive.google.com/file/d/1abc/view",
		"https://drive.google.com/file/d/",
	} {
		if got, ok := DriveFileID(in); ok {
			t.Errorf("DriveFileID(%q) = %q, want no id", in, got)
		}
	}
}

func TestDriveFileURLRoundTrips(t *testing.T) {
	url := DriveFileURL("1U1WtcGqCfBu9YMh2GZ9LKyzcZSrL05R6")
	if url != "https://drive.google.com/file/d/1U1WtcGqCfBu9YMh2GZ9LKyzcZSrL05R6/view?usp=drive_link" {
		t.Fatalf("DriveFileURL = %q", url)
	}
	if id, ok := DriveFileID(url); !ok || id != "1U1WtcGqCfBu9YMh2GZ9LKyzcZSrL05R6" {
		t.Fatalf("DriveFileID(DriveFileURL(id)) = %q, %v", id, ok)
	}
}
