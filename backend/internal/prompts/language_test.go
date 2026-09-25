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

// TestResponseLanguageDirective_CoversTheResultNoteNotItsLabels. A qa result note
// is human-facing prose - it renders in the Tests tab under WHAT QA SAW - so the
// sentences follow the configured language. Its three leading labels do not: they
// are the scan anchors of a fixed shape (see qaDefault), and a translated anchor
// stops being one. Both halves have to be said HERE, because this directive
// explicitly out-argues every English example above it, so without the carve-out
// the labels would be translated along with the prose - and because qaDefault is
// injected in every language, where language wording does not belong.
func TestResponseLanguageDirective_CoversTheResultNoteNotItsLabels(t *testing.T) {
	got := ResponseLanguageDirective("Thai")
	if !strings.Contains(got, "ao smoke record --note") {
		t.Fatalf("directive must scope the recorded result note into the language:\n%s", got)
	}
	for _, want := range []string{"`Saw:`", "`On:`", "`Full:`"} {
		if !strings.Contains(got, want) {
			t.Fatalf("directive must keep the result-note label %s in English:\n%s", want, got)
		}
	}
	// The labels must be named on the English side of the directive, after the
	// "Keep everything ... in English" sentence - not merely mentioned somewhere.
	english := strings.Index(got, "in English:")
	if english < 0 || strings.Index(got, "`Saw:`") < english {
		t.Fatalf("the result-note labels must sit in the English carve-out, not in the human-facing list:\n%s", got)
	}
	// And none of this may break the English no-op.
	for _, lang := range []string{"", "English"} {
		if ResponseLanguageDirective(lang) != "" {
			t.Fatalf("adding result-note wording must not break the English no-op for %q", lang)
		}
	}
}

// TestResponseLanguageDirective_NamesTheSlipPoints. A long Thai worker session
// drifted to English with the directive present, worst in a few specific places:
// narration between tool calls, tool descriptions, AskUserQuestion options, the
// reply after a task notification / Monitor event, and the first reply after a
// compaction. The directive names each one so the agent cannot read them as
// outside "human-facing output".
func TestResponseLanguageDirective_NamesTheSlipPoints(t *testing.T) {
	got := ResponseLanguageDirective("Thai")
	for _, want := range []string{
		"places where agents most often slip back into English",
		"narration you write between tool calls",
		"`description` you give a Bash or other tool call",
		"AskUserQuestion questions, option labels, and option descriptions",
		"after a task notification, a background task finishing, or a Monitor event",
		"first reply after a context compaction",
		"your reply is still in Thai",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("directive does not name the slip point %q:\n%s", want, got)
		}
	}
	// The slip list is part of the human-facing half, ahead of the English carve-out.
	if strings.Index(got, "AskUserQuestion") > strings.Index(got, "in English:") {
		t.Fatalf("the slip list must come before the English carve-out:\n%s", got)
	}
}

// TestResponseLanguageDirective_EndsWithSelfCheck. The directive is the last
// section of every assembled prompt, and its last sentence is a check the agent
// runs before sending prose, so the final thing it reads is the language rule.
func TestResponseLanguageDirective_EndsWithSelfCheck(t *testing.T) {
	got := ResponseLanguageDirective("Thai")
	want := "Before you send any prose to the human, check its language: if it is not in Thai, rewrite it in Thai first."
	if !strings.HasSuffix(got, "\n\n"+want) {
		t.Fatalf("directive must end with the self-check %q:\n%s", want, got)
	}
	// The repository carve-out is kept as a whole paragraph, just before the check.
	carveOut := "Only the prose you address to a person changes language; the repository and its artifacts stay in English."
	if !strings.Contains(got, carveOut+"\n\n"+want) {
		t.Fatalf("the English carve-out paragraph must stay intact and precede the self-check:\n%s", got)
	}
}

// TestResponseLanguageDirective_Calm. Clarity beats volume: the new wording adds
// no shouting on top of the existing directive.
func TestResponseLanguageDirective_Calm(t *testing.T) {
	got := ResponseLanguageDirective("Thai")
	for _, loud := range []string{"MUST", "NEVER", "ALWAYS", "IMPORTANT", "!"} {
		if strings.Contains(got, loud) {
			t.Fatalf("directive should stay calm, found %q:\n%s", loud, got)
		}
	}
}
