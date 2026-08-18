package simrecord_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/simrecord"
)

func TestSlug(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"words become one separator", "Login to Portfolio", "login-to-portfolio"},
		{"punctuation is a separator", "sign-up: step 2 (retry!)", "sign-up-step-2-retry"},
		{"runs of separators collapse", "a   ---   b", "a-b"},
		{"leading and trailing separators go", "  --hello--  ", "hello"},
		{"empty stays empty", "   ", ""},
		{"only punctuation is empty", "!!!???", ""},
		// The realistic case for this human: a name typed in Thai. It must
		// survive as Thai, because they are the one who has to recognise it in
		// a list a week later.
		{"thai survives", "เข้าสู่ระบบ", "เข้าสู่ระบบ"},
		{"thai with spaces", "เข้าสู่ระบบ แล้วไปหน้าพอร์ต", "เข้าสู่ระบบ-แล้วไปหน้าพอร์ต"},
		{"mixed scripts", "Login เข้าสู่ระบบ 2", "login-เข้าสู่ระบบ-2"},
		{"digits and case", "Flow ABC 42", "flow-abc-42"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := simrecord.Slug(tc.in); got != tc.want {
				t.Errorf("Slug(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// The slug is the only sanitising step there is, so it has to be the one that
// makes a name safe. Anything that could climb out of a directory, name a
// hidden file, or be read as an extension must simply not survive it.
func TestSlug_CannotProduceAPathOrAnExtension(t *testing.T) {
	for _, in := range []string{
		"../../etc/passwd", "..", ".", "/absolute/path", `..\..\windows`,
		".hidden", "name.yaml", "a/b", "a:b", "with\x00null", "-",
	} {
		got := simrecord.Slug(in)
		if strings.ContainsAny(got, `/\.:`) || got == ".." || got == "." {
			t.Errorf("Slug(%q) = %q, which can address something other than a name", in, got)
		}
		if name := simrecord.FileName(got, time.Unix(0, 0).UTC()); filepath.Base(name) != name {
			t.Errorf("FileName from Slug(%q) = %q, which is not a bare file name", in, name)
		}
	}
}

func TestSlug_IsBounded(t *testing.T) {
	got := simrecord.Slug(strings.Repeat("ก", 200))
	if n := len([]rune(got)); n > 60 {
		t.Errorf("Slug kept %d runes, want at most 60", n)
	}
	if got == "" {
		t.Error("a long name must be shortened, not dropped")
	}
}

func TestFileName_KeepsTheTimestampSoNamesCannotCollide(t *testing.T) {
	at := time.Date(2026, 8, 18, 4, 57, 22, 711_000_000, time.UTC)
	named := simrecord.FileName("Login to Portfolio", at)
	if named != "login-to-portfolio-20260818-045722.711Z.yaml" {
		t.Errorf("FileName = %q", named)
	}
	unnamed := simrecord.FileName("", at)
	if unnamed != "20260818-045722.711Z.yaml" {
		t.Errorf("an unnamed recording still needs a working file name, got %q", unnamed)
	}
	// Two recordings a human gave the same name are two files, never a
	// question about overwriting one.
	later := simrecord.FileName("Login to Portfolio", at.Add(time.Second))
	if later == named {
		t.Errorf("same name at a different moment must not collide: %q", later)
	}
}

func TestParseFileName(t *testing.T) {
	at := time.Date(2026, 8, 18, 4, 57, 22, 711_000_000, time.UTC)
	cases := []struct {
		base     string
		wantName string
		wantOK   bool
	}{
		{"login-to-portfolio-20260818-045722.711Z.yaml", "login-to-portfolio", true},
		{"20260818-045722.711Z.yaml", "", true},
		// What every flow recorded before names existed is called. It still
		// reads, and the udid is not shown as though it were a name.
		{"20260818-045722.711Z-97882B61-7B22-45C1-9DF8-0E52913C87DA.yaml", "", true},
		{"hand-written.yaml", "", false},
		{"no-stamp-here.yaml", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.base, func(t *testing.T) {
			name, recordedAt, ok := simrecord.ParseFileName(tc.base)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if name != tc.wantName {
				t.Errorf("name = %q, want %q", name, tc.wantName)
			}
			if !recordedAt.Equal(at) {
				t.Errorf("recordedAt = %s, want %s", recordedAt, at)
			}
		})
	}
}

// Whatever a human types must survive being written and read back, or the name
// in the list is not the name they gave it.
func TestFileName_RoundTripsThroughParse(t *testing.T) {
	at := time.Date(2026, 8, 18, 4, 57, 22, 711_000_000, time.UTC)
	for _, typed := range []string{"Login to Portfolio", "เข้าสู่ระบบ แล้วไปหน้าพอร์ต", "retry #3", ""} {
		base := simrecord.FileName(typed, at)
		name, recordedAt, ok := simrecord.ParseFileName(base)
		if !ok {
			t.Fatalf("ParseFileName(%q) did not recognize a name this package wrote", base)
		}
		if want := simrecord.Slug(typed); name != want {
			t.Errorf("round trip of %q gave name %q, want %q", typed, name, want)
		}
		if !recordedAt.Equal(at) {
			t.Errorf("round trip of %q gave time %s, want %s", typed, recordedAt, at)
		}
	}
}
