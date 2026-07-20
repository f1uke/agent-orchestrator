package prompts

import (
	"strings"
	"testing"
)

// TestResponseLanguageDirective_DefaultIsNoOp: English and blank render nothing so
// the default agent path is byte-for-byte unchanged and spends no extra tokens
// (mirrors TaskSizeDirective's standard/deep no-op).
func TestResponseLanguageDirective_DefaultIsNoOp(t *testing.T) {
	for _, lang := range []string{"", "   ", "English", "english", "  ENGLISH ", "\tEnglish\n"} {
		if got := ResponseLanguageDirective(lang); got != "" {
			t.Fatalf("ResponseLanguageDirective(%q) = %q, want empty", lang, got)
		}
	}
}

// TestResponseLanguageDirective_NonEnglish: a real language renders a strong,
// cleanly-appendable directive that (a) names the language for human-facing prose,
// (b) explicitly carves out that code/commits/PRs/branches stay English.
func TestResponseLanguageDirective_NonEnglish(t *testing.T) {
	got := ResponseLanguageDirective("Thai")
	if !strings.HasPrefix(got, "\n\n") {
		t.Fatalf("directive must start with a blank-line separator so it appends cleanly:\n%q", got)
	}
	// The configured language must appear so the directive reflects the setting.
	if !strings.Contains(got, "Thai") {
		t.Fatalf("directive must name the configured language:\n%s", got)
	}
	// Human-facing scoping.
	for _, want := range []string{
		"human-facing",
		"status updates",
		"final report",
		"questions to the human",
		"review comments",
	} {
		if !strings.Contains(strings.ToLower(got), strings.ToLower(want)) {
			t.Fatalf("directive missing human-facing scope wording %q:\n%s", want, got)
		}
	}
	// English carve-out for repository artifacts.
	for _, want := range []string{
		"CODE",
		"COMMIT MESSAGES",
		"BRANCH NAMES",
		"file names",
		"English",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("directive missing English carve-out term %q:\n%s", want, got)
		}
	}
	// It must say the directive overrides the ambient English so it wins.
	if !strings.Contains(strings.ToLower(got), "even when") && !strings.Contains(strings.ToLower(got), "overrides") {
		t.Fatalf("directive should assert it overrides the ambient English:\n%s", got)
	}
	// No em dash: honor the plain-dash house rule for new prose.
	if strings.Contains(got, "—") {
		t.Fatalf("directive must use plain '-' not em dash:\n%s", got)
	}
}

// TestResponseLanguageDirective_CoversSmokeChecklist: a smoke-test checklist is
// human-facing prose - the user plays it live in the Tests tab - so its case prose
// must follow the configured language. The always-injected SmokeChecklistProtocol
// is written in English and hands the model a concrete English JSON example, so
// without an explicit mention here the nearby example wins and cases come out in
// English. The smoke tooling (fileRef, prNum, the ao smoke set command, JSON keys)
// stays English like every other technical identifier.
func TestResponseLanguageDirective_CoversSmokeChecklist(t *testing.T) {
	got := ResponseLanguageDirective("Thai")
	// The case prose fields must be named so the model knows exactly what translates.
	for _, want := range []string{
		"smoke-test checklist",
		"name", "why", "steps", "expected",
	} {
		if !strings.Contains(strings.ToLower(got), strings.ToLower(want)) {
			t.Fatalf("directive must scope smoke case prose (%q) into the language:\n%s", want, got)
		}
	}
	// The tooling half of a smoke case must stay English.
	for _, want := range []string{"fileRef", "prNum", "ao smoke set"} {
		if !strings.Contains(got, want) {
			t.Fatalf("directive must keep smoke tooling %q in English:\n%s", want, got)
		}
	}
	// The smoke mention must live in the language directive, which is already a
	// no-op for English - never in the always-injected protocol.
	for _, lang := range []string{"", "English"} {
		if ResponseLanguageDirective(lang) != "" {
			t.Fatalf("adding smoke wording must not break the English no-op for %q", lang)
		}
	}
}

// TestResponseLanguageDirective_LanguageReflected: the exact configured value is
// what appears (free-form language name, trimmed).
func TestResponseLanguageDirective_LanguageReflected(t *testing.T) {
	for _, lang := range []string{"Japanese", "Português (Brasil)", "  Thai  "} {
		got := ResponseLanguageDirective(lang)
		want := strings.TrimSpace(lang)
		if !strings.Contains(got, want) {
			t.Fatalf("ResponseLanguageDirective(%q) must contain %q:\n%s", lang, want, got)
		}
	}
}

// TestResolveResponseLanguage_Precedence: the project override wins when set; a
// blank override falls back to the global default; both blank yields "".
func TestResolveResponseLanguage_Precedence(t *testing.T) {
	cases := []struct {
		name            string
		projectOverride string
		globalDefault   string
		want            string
	}{
		{"override wins over global", "Thai", "English", "Thai"},
		{"blank override falls back to global", "", "Japanese", "Japanese"},
		{"whitespace override falls back to global", "   ", "Japanese", "Japanese"},
		{"both blank yields empty", "", "", ""},
		{"global blank, override set", "Thai", "", "Thai"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveResponseLanguage(tc.projectOverride, tc.globalDefault); got != tc.want {
				t.Fatalf("ResolveResponseLanguage(%q, %q) = %q, want %q", tc.projectOverride, tc.globalDefault, got, tc.want)
			}
		})
	}
}
