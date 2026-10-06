package domain

import (
	"errors"
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
