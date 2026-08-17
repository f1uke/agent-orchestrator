package simflow_test

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/simflow"
)

func TestRender_UniqueTextIsTheBareStringHouseStyle(t *testing.T) {
	got := simflow.Render(simflow.Choice{Rung: simflow.RungText, Text: "Accessibility", Ambiguity: 1}, "Accessibility")
	want := "- tapOn: \"Accessibility\"\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRender_RepeatedTextEmitsIndexAndSaysWhy(t *testing.T) {
	got := simflow.Render(simflow.Choice{
		Rung: simflow.RungTextIndex, Text: "Continue", Index: 1, Ambiguity: 3,
	}, "Continue")
	if !strings.Contains(got, "# 3 elements share this text") {
		t.Errorf("missing the ambiguity comment: %q", got)
	}
	for _, want := range []string{"- tapOn:", "    text: \"Continue\"", "    index: 1"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestRender_EscapedTextSaysItWasEscaped(t *testing.T) {
	got := simflow.Render(simflow.Choice{
		Rung: simflow.RungText, Text: `See all \(12\)`, Escaped: true, Ambiguity: 1,
	}, "See all (12)")
	if !strings.Contains(got, "# escaped: the label contains regex characters") {
		t.Errorf("missing the escaping comment: %q", got)
	}
	if !strings.Contains(got, `- tapOn: "See all \\(12\\)"`) {
		t.Errorf("missing the escaped selector: %q", got)
	}
}

func TestRender_IDRungSaysWhyItIsNotText(t *testing.T) {
	got := simflow.Render(simflow.Choice{Rung: simflow.RungID, ID: "search-field"}, "")
	if !strings.Contains(got, "# no label; matched on the accessibility id") {
		t.Errorf("missing the id comment: %q", got)
	}
	if !strings.Contains(got, "    id: \"search-field\"") {
		t.Errorf("missing the id selector: %q", got)
	}
}

func TestRender_PointRungIsMarkedBrittle(t *testing.T) {
	got := simflow.Render(simflow.Choice{Rung: simflow.RungPoint, PercentX: 50, PercentY: 86}, "")
	if !strings.Contains(got, "breaks on any layout change") {
		t.Errorf("missing the brittleness warning: %q", got)
	}
	if !strings.Contains(got, `    point: "50%,86%"`) {
		t.Errorf("missing the point selector: %q", got)
	}
}

func TestRender_OffScreenEmitsScrollNotTap(t *testing.T) {
	got := simflow.Render(simflow.Choice{
		Rung: simflow.RungText, Text: "See all", Ambiguity: 1, OffScreen: true,
	}, "See all")
	if strings.Contains(got, "tapOn") {
		t.Errorf("off-screen element must not be tapped directly: %q", got)
	}
	for _, want := range []string{"# off screen - scroll to it first", "- scrollUntilVisible:", "    element: \"See all\"", "    direction: DOWN"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestRender_NoneRungSaysItCannotBeAddressed(t *testing.T) {
	got := simflow.Render(simflow.Choice{Rung: simflow.RungNone, OffScreen: true}, "")
	if !strings.Contains(got, "# no label, no id and no reachable point") {
		t.Errorf("missing the unaddressable comment: %q", got)
	}
	if strings.Contains(got, "- tapOn") || strings.Contains(got, "- scrollUntilVisible") {
		t.Errorf("must emit no command at all: %q", got)
	}
}

func TestRender_AlwaysEndsWithExactlyOneNewline(t *testing.T) {
	for name, c := range map[string]simflow.Choice{
		"text":  {Rung: simflow.RungText, Text: "A", Ambiguity: 1},
		"index": {Rung: simflow.RungTextIndex, Text: "A", Ambiguity: 2},
		"id":    {Rung: simflow.RungID, ID: "a"},
		"point": {Rung: simflow.RungPoint},
		"none":  {Rung: simflow.RungNone},
	} {
		got := simflow.Render(c, "A")
		if !strings.HasSuffix(got, "\n") || strings.HasSuffix(got, "\n\n") {
			t.Errorf("%s: bad trailing newline in %q", name, got)
		}
	}
}

// A YAML double-quoted scalar processes backslash escapes: `\(` is not a valid
// one and makes the whole document fail to parse. Verified against the real
// `maestro check-syntax`, which rejects the single-backslash form and accepts
// this one. %q is what keeps the two in step.
func TestRender_EscapedTextIsValidYAMLNotJustEscaped(t *testing.T) {
	got := simflow.Render(simflow.Choice{
		Rung: simflow.RungText, Text: `a\(b`, Escaped: true, Ambiguity: 1,
	}, "a(b")
	if strings.Contains(got, `"a\(b"`) {
		t.Fatalf("emitted a single-backslash escape, which YAML rejects: %q", got)
	}
	if !strings.Contains(got, `"a\\(b"`) {
		t.Errorf("want the backslash doubled for YAML, got %q", got)
	}
}
