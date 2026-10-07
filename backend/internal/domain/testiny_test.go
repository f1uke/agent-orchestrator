package domain

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseTestinyRunRef(t *testing.T) {
	cases := []struct {
		in      string
		wantID  TestinyRunID
		wantKey string
	}{
		{"565", 565, ""},
		{" 565 ", 565, ""},
		{"TR-565", 565, ""},
		{"tr-565", 565, ""},
		{"https://app.testiny.io/MOB/testruns/tr/565", 565, "MOB"},
		{"https://app.testiny.io/MOB/testruns/tr/565/", 565, "MOB"},
		{"https://app.testiny.io/MOB/testruns/tr/565?tab=results", 565, "MOB"},
		{"https://app.testiny.io/MOB/testruns/tr/565#x", 565, "MOB"},
		{"http://app.testiny.io/kern/testruns/tr/7", 7, "kern"},
	}
	for _, tc := range cases {
		id, key, err := ParseTestinyRunRef(tc.in)
		if err != nil {
			t.Errorf("ParseTestinyRunRef(%q): unexpected error %v", tc.in, err)
			continue
		}
		if id != tc.wantID || key != tc.wantKey {
			t.Errorf("ParseTestinyRunRef(%q) = (%d, %q), want (%d, %q)", tc.in, id, key, tc.wantID, tc.wantKey)
		}
	}
}

func TestParseTestinyRunRefRejectsEverythingElse(t *testing.T) {
	for _, in := range []string{
		"",
		"0",
		"-5",
		"TR-0",
		"TR565",
		"TC-565",
		"565abc",
		"99999999999999999999",
		"https://evil.example/MOB/testruns/tr/565",
		"https://app.testiny.io.evil.example/MOB/testruns/tr/565",
		"https://app.testiny.io/MOB/testcases/tc/565",
		"https://app.testiny.io/MOB/testruns/tr/",
		"https://app.testiny.io/MOB/testruns/tr/abc",
		"https://app.testiny.io/testruns/tr/565",
		"https://app.testiny.io/MOB/testruns/tr/565/extra",
		"ftp://app.testiny.io/MOB/testruns/tr/565",
	} {
		if id, key, err := ParseTestinyRunRef(in); !errors.Is(err, ErrBadRunRef) {
			t.Errorf("ParseTestinyRunRef(%q) = (%d, %q, %v), want ErrBadRunRef", in, id, key, err)
		}
	}
}

func TestTestinyRunURL(t *testing.T) {
	if got := TestinyRunURL("MOB", 565); got != "https://app.testiny.io/MOB/testruns/tr/565" {
		t.Fatalf("TestinyRunURL = %q", got)
	}
	if got := TestinyRunURL("", 565); got != "" {
		t.Fatalf("TestinyRunURL without a key = %q, want empty", got)
	}
}

func TestTestinyRunIDString(t *testing.T) {
	if got := TestinyRunID(632).String(); got != "TR-632" {
		t.Fatalf("String = %q", got)
	}
}

func TestParseTestinyResults(t *testing.T) {
	long := strings.Repeat("ก", TestinyCommentMax)
	got, err := ParseTestinyResults([]TestinyResult{
		{CaseID: 1, Status: "passed"},
		{CaseID: 2, Status: " FAILED ", Comment: "  ปุ่มยืนยันไม่แสดง  "},
		{CaseID: 3, Status: "BLOCKED", Comment: long},
		{CaseID: 4, Status: "SKIPPED", Comment: "ไม่มีบัญชีทดสอบ"},
		{CaseID: 5, Status: "NOTRUN", Comment: "   "},
	})
	if err != nil {
		t.Fatalf("ParseTestinyResults: %v", err)
	}
	want := []TestinyResult{
		{CaseID: 1, Status: TestinyPassed},
		{CaseID: 2, Status: TestinyFailed, Comment: "ปุ่มยืนยันไม่แสดง"},
		{CaseID: 3, Status: TestinyBlocked, Comment: long},
		{CaseID: 4, Status: TestinySkipped, Comment: "ไม่มีบัญชีทดสอบ"},
		{CaseID: 5, Status: TestinyNotRun},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}

	for name, tc := range map[string]struct {
		in   []TestinyResult
		says string
	}{
		"empty batch":         {nil, "no results"},
		"case id 0":           {[]TestinyResult{{CaseID: 0, Status: "PASSED"}}, "case id"},
		"unknown status":      {[]TestinyResult{{CaseID: 7, Status: "UNTESTED"}}, "TC-7"},
		"failed, no comment":  {[]TestinyResult{{CaseID: 7, Status: "FAILED", Comment: "  "}}, "TC-7"},
		"blocked, no comment": {[]TestinyResult{{CaseID: 7, Status: "BLOCKED"}}, "TC-7"},
		"skipped, no comment": {[]TestinyResult{{CaseID: 7, Status: "SKIPPED"}}, "TC-7"},
		"comment too long":    {[]TestinyResult{{CaseID: 7, Status: "FAILED", Comment: long + "ข"}}, "300"},
		"passed with comment": {[]TestinyResult{{CaseID: 7, Status: "PASSED", Comment: "ok"}}, "TC-7"},
		"notrun with comment": {[]TestinyResult{{CaseID: 7, Status: "NOTRUN", Comment: "later"}}, "TC-7"},
		"a case twice":        {[]TestinyResult{{CaseID: 7, Status: "PASSED"}, {CaseID: 7, Status: "FAILED", Comment: "x"}}, "TC-7"},
	} {
		_, err := ParseTestinyResults(tc.in)
		if !errors.Is(err, ErrBadTestinyResult) || !strings.Contains(err.Error(), tc.says) {
			t.Errorf("%s: err = %v, want ErrBadTestinyResult saying %q", name, err, tc.says)
		}
	}
}

func TestParseTestinyResultsWithSteps(t *testing.T) {
	got, err := ParseTestinyResults([]TestinyResult{
		{CaseID: 1, Status: "failed", Comment: "step 2", Steps: []TestinyStepResult{{N: 3, Status: "passed"}, {N: 2, Status: " FAILED "}}},
		{CaseID: 2, Steps: []TestinyStepResult{{N: 1, Status: "BLOCKED"}}},
	})
	if err != nil {
		t.Fatalf("ParseTestinyResults: %v", err)
	}
	want := []TestinyResult{
		{CaseID: 1, Status: TestinyFailed, Comment: "step 2", Steps: []TestinyStepResult{{N: 2, Status: TestinyFailed}, {N: 3, Status: TestinyPassed}}},
		{CaseID: 2, Steps: []TestinyStepResult{{N: 1, Status: TestinyBlocked}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}

	for name, tc := range map[string]struct {
		in   TestinyResult
		says string
	}{
		"no status and no steps":   {TestinyResult{CaseID: 7}, "TC-7"},
		"a comment with no status": {TestinyResult{CaseID: 7, Comment: "x", Steps: []TestinyStepResult{{N: 1, Status: "FAILED"}}}, "TC-7"},
		"step 0":                   {TestinyResult{CaseID: 7, Steps: []TestinyStepResult{{N: 0, Status: "PASSED"}}}, "TC-7 step 0"},
		"a step twice":             {TestinyResult{CaseID: 7, Steps: []TestinyStepResult{{N: 2, Status: "PASSED"}, {N: 2, Status: "FAILED"}}}, "TC-7 step 2"},
		"unknown step status":      {TestinyResult{CaseID: 7, Steps: []TestinyStepResult{{N: 1, Status: "UNTESTED"}}}, "TC-7 step 1"},
	} {
		_, err := ParseTestinyResults([]TestinyResult{tc.in})
		if !errors.Is(err, ErrBadTestinyResult) || !strings.Contains(err.Error(), tc.says) {
			t.Errorf("%s: err = %v, want ErrBadTestinyResult saying %q", name, err, tc.says)
		}
	}
}

func TestParseTestinyCaseRef(t *testing.T) {
	for in, want := range map[string]int64{"7166": 7166, "TC-7166": 7166, "tc-12": 12, " 7166 ": 7166} {
		got, err := ParseTestinyCaseRef(in)
		if err != nil || got != want {
			t.Errorf("ParseTestinyCaseRef(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "0", "TC-0", "TR-7166", "7166x", "-1", "99999999999999999999"} {
		if _, err := ParseTestinyCaseRef(in); !errors.Is(err, ErrBadCaseRef) {
			t.Errorf("ParseTestinyCaseRef(%q) err = %v, want ErrBadCaseRef", in, err)
		}
	}
}

func TestTestinyCasePriorityNamesTestinysLevels(t *testing.T) {
	for level, want := range map[int]string{0: "Critical", 1: "High", 2: "Medium", 3: "Low", 4: "4"} {
		if got := NewTestinyCasePriority(level); got != (TestinyCasePriority{Level: level, Label: want}) {
			t.Errorf("NewTestinyCasePriority(%d) = %+v, want label %q", level, got, want)
		}
	}
}
