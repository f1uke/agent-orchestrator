package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/skillassets"
)

// An agent learns what to write into `--note` from two places that are not the
// prompt: this command's own long help, and the `smoke` page shipped in the
// daemon. Both have to teach the same shape, because a note that arrives as a
// paragraph is one no person reads in the panel it renders in - and a shape
// taught in one place and not the other is the drift that produced the paragraph
// in the first place.
func TestSmokeRecordShape_TaughtByBothTheHelpAndTheSkillPage(t *testing.T) {
	dir := t.TempDir()
	if err := skillassets.Install(dir); err != nil {
		t.Fatalf("install skill: %v", err)
	}
	page, err := os.ReadFile(filepath.Join(skillassets.Dir(dir, false), "commands", "smoke.md"))
	if err != nil {
		t.Fatalf("read smoke.md: %v", err)
	}

	for _, surface := range []struct {
		name string
		text string
	}{
		{"ao smoke record --help", smokeRecordLong},
		{"the shipped smoke.md", string(page)},
	} {
		// The labels are the scan anchors and are verbatim everywhere.
		for _, want := range []string{"Saw:", "On:", "Full:"} {
			if !strings.Contains(surface.text, want) {
				t.Errorf("%s does not teach the result-note label %q", surface.name, want)
			}
		}
		// The rest is prose, emphasised differently in plain help and in markdown,
		// so match it case-insensitively: what is pinned is that it is SAID.
		for _, want := range []string{
			// The two halves that must survive any shortening.
			"observation and nothing else", "wrong build",
			// Where the detail goes instead, so it is relocated and not lost.
			"PR body",
			// A budget, so "short" is not left to taste.
			"400",
		} {
			if !strings.Contains(strings.ToLower(surface.text), strings.ToLower(want)) {
				t.Errorf("%s does not teach the result-note shape: missing %q", surface.name, want)
			}
		}
		// A skip note answers a different question and must not be forced into
		// the three lines - saying so is what keeps the shape from being read as
		// a format for every note.
		if !strings.Contains(surface.text, "answers a different question") {
			t.Errorf("%s does not exempt a --verdict skip note from the result-note shape", surface.name)
		}
	}
}
