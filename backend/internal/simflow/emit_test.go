package simflow_test

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/simflow"
)

// baseOpts is the provenance every header test starts from, so each test
// below only states what it is actually asserting about.
func baseOpts() simflow.EmitOptions {
	return simflow.EmitOptions{
		Device:     "iPhone 17 Pro Max",
		Runtime:    "iOS 18.4",
		RecordedAt: "2026-08-17T10:00:00Z",
	}
}

func TestEmit_HeaderCarriesAppIDPlaceholderAndProvenance(t *testing.T) {
	got, err := simflow.Emit(nil, baseOpts())
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	// The house style keeps appId an environment variable - never a literal
	// bundle id. There is no EmitOptions field that could leak one in here;
	// this line is always exactly this string.
	if !strings.HasPrefix(got, "appId: ${APP_ID}\n---\n") {
		t.Fatalf("missing the appId placeholder header, got:\n%s", got)
	}
	if want := "# recorded by ao sim at 2026-08-17T10:00:00Z, device iPhone 17 Pro Max (iOS 18.4)\n"; !strings.Contains(got, want) {
		t.Errorf("missing %q in:\n%s", want, got)
	}
	// There is deliberately no "frontmost at start" line: nothing upstream of
	// this package persists which app was in the foreground when a recording
	// began, and a header line that would always read "unknown" is noise a
	// reader learns to skip - see EmitOptions' own doc comment.
	if strings.Contains(got, "frontmost") {
		t.Fatalf("must not print a frontmost line nobody can honestly fill in, got:\n%s", got)
	}
}

func TestEmit_DoesNotFabricateLaunchApp(t *testing.T) {
	steps := []simflow.Step{
		{Seq: 1, Kind: simflow.StepTap, Choice: simflow.Choice{Rung: simflow.RungText, Text: "Accessibility", Ambiguity: 1}, Plain: "Accessibility"},
	}
	got, err := simflow.Emit(steps, baseOpts())
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if strings.Contains(got, "launchApp") {
		t.Fatalf("must never fabricate launchApp, got:\n%s", got)
	}
	if !strings.Contains(got, "add your own entry point above if this flow must start from a cold app") {
		t.Fatalf("missing the entry-point guidance comment, got:\n%s", got)
	}
}

func TestEmit_EntryOptionEmitsRunFlowAsTheFirstStep(t *testing.T) {
	opts := baseOpts()
	opts.Entry = "../flows/login.yaml"
	got, err := simflow.Emit(nil, opts)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if strings.Contains(got, "add your own entry point") {
		t.Fatalf("the guidance comment must be replaced, not merely joined by runFlow, got:\n%s", got)
	}
	headerEnd := strings.Index(got, "# recorded by ao sim at")
	if headerEnd < 0 {
		t.Fatalf("missing the provenance comment, got:\n%s", got)
	}
	afterHeader := got[headerEnd:]
	nl := strings.Index(afterHeader, "\n")
	firstStepLine := strings.TrimSpace(afterHeader[nl+1:])
	if firstStepLine != "- runFlow: ../flows/login.yaml" {
		t.Fatalf("first step after the header must be runFlow, got %q from:\n%s", firstStepLine, got)
	}
}

func TestEmit_TapUsesTheStoredSelector(t *testing.T) {
	steps := []simflow.Step{
		{Seq: 1, Kind: simflow.StepTap, Choice: simflow.Choice{Rung: simflow.RungText, Text: "Continue", Ambiguity: 1}, Plain: "Continue"},
	}
	got, err := simflow.Emit(steps, baseOpts())
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	// This must be exactly what Render itself would produce for the same
	// Choice - Emit is required to reuse Render, not re-derive it.
	want := simflow.Render(steps[0].Choice, steps[0].Plain)
	if !strings.Contains(got, want) {
		t.Fatalf("want Render's own output %q verbatim in:\n%s", want, got)
	}
}

func TestEmit_TypeBecomesInputText(t *testing.T) {
	steps := []simflow.Step{
		{Seq: 1, Kind: simflow.StepType, Text: "hello@example.com"},
	}
	got, err := simflow.Emit(steps, baseOpts())
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if !strings.Contains(got, `- inputText: "hello@example.com"`) {
		t.Fatalf("missing inputText step, got:\n%s", got)
	}
}

func TestEmit_SwipeUsesIntegerPercentages(t *testing.T) {
	steps := []simflow.Step{
		{Seq: 1, Kind: simflow.StepSwipe, X: 0.5, Y: 0.865, ToX: 0.5, ToY: 0.1},
	}
	got, err := simflow.Emit(steps, baseOpts())
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	// 0.865 rounds to 87 (not 86, not 86.5): the same rounding rule For's own
	// percent conversion uses, reused rather than re-implemented.
	want := `- swipe: {start: "50%,87%", end: "50%,10%"}`
	if !strings.Contains(got, want) {
		t.Fatalf("want %q in:\n%s", want, got)
	}
}

func TestEmit_ButtonHomeBecomesPressKeyHome(t *testing.T) {
	steps := []simflow.Step{
		{Seq: 1, Kind: simflow.StepButton, Detail: "home"},
	}
	got, err := simflow.Emit(steps, baseOpts())
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if !strings.Contains(got, "- pressKey: Home\n") {
		t.Fatalf("missing pressKey: Home, got:\n%s", got)
	}
}

// Maestro's KeyCode has no app-switcher entry. Emitting nothing for it would
// silently drop a step the recording says happened, so Emit must fail and
// name the step it could not translate instead.
func TestEmit_ButtonAppSwitcherFailsAndNamesTheStep(t *testing.T) {
	steps := []simflow.Step{
		{Seq: 1, Kind: simflow.StepTap, Choice: simflow.Choice{Rung: simflow.RungText, Text: "Settings", Ambiguity: 1}, Plain: "Settings"},
		{Seq: 2, Kind: simflow.StepButton, Detail: "app-switcher"},
	}
	_, err := simflow.Emit(steps, baseOpts())
	if err == nil {
		t.Fatal("want an error for an untranslatable app-switcher step, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "2") {
		t.Errorf("error must name the step (seq 2), got: %v", err)
	}
	if !strings.Contains(msg, "app-switcher") {
		t.Errorf("error must name what could not be translated, got: %v", err)
	}
}

func TestEmit_ScreenChangeStepGetsAnExtendedWaitUntil(t *testing.T) {
	steps := []simflow.Step{
		{
			Seq: 1, Kind: simflow.StepTap, ScreenChange: true,
			Choice: simflow.Choice{Rung: simflow.RungText, Text: "Accessibility", Ambiguity: 1},
			Plain:  "Accessibility",
		},
	}
	got, err := simflow.Emit(steps, baseOpts())
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if !strings.Contains(got, "- extendedWaitUntil:\n") {
		t.Fatalf("missing extendedWaitUntil, got:\n%s", got)
	}
	if !strings.Contains(got, `    visible: "Accessibility"`) {
		t.Fatalf("extendedWaitUntil must wait on the same selector as the step, got:\n%s", got)
	}
	if !strings.Contains(got, "    timeout: 10000\n") {
		t.Fatalf("missing the timeout, got:\n%s", got)
	}
	waitAt := strings.Index(got, "- extendedWaitUntil:")
	tapAt := strings.Index(got, "- tapOn:")
	if tapAt == -1 {
		tapAt = strings.Index(got, `- tapOn: "Accessibility"`)
	}
	if waitAt < 0 || tapAt < 0 || waitAt > tapAt {
		t.Fatalf("extendedWaitUntil must come before the tap, got:\n%s", got)
	}
}

func TestEmit_NonScreenChangeStepDoesNotGetOne(t *testing.T) {
	steps := []simflow.Step{
		{
			Seq: 1, Kind: simflow.StepTap, ScreenChange: false,
			Choice: simflow.Choice{Rung: simflow.RungText, Text: "Accessibility", Ambiguity: 1},
			Plain:  "Accessibility",
		},
	}
	got, err := simflow.Emit(steps, baseOpts())
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if strings.Contains(got, "extendedWaitUntil") {
		t.Fatalf("must not emit extendedWaitUntil when ScreenChange is false, got:\n%s", got)
	}
}

// An accessibility id is more stable than a label, so a screen change onto a
// screen whose landmark has only an id must still get a wait - and it has to
// be nested, because a bare string under `visible:` is a text matcher, and an
// id compared as text would be wrong.
func TestEmit_ScreenChangeWithIDSelectorWaitsOnTheID(t *testing.T) {
	steps := []simflow.Step{
		{
			Seq: 1, Kind: simflow.StepTap, ScreenChange: true,
			Choice: simflow.Choice{Rung: simflow.RungID, ID: "search-field"},
		},
	}
	got, err := simflow.Emit(steps, baseOpts())
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if strings.Contains(got, `visible: "search-field"`) {
		t.Fatalf("an id must not be waited on as a bare string (that is a text matcher), got:\n%s", got)
	}
	for _, want := range []string{
		"- extendedWaitUntil:\n",
		"    visible:\n",
		`      id: "search-field"` + "\n",
		"    timeout: 10000\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

// A point cannot become "visible" and an off-screen element was never on
// screen, so neither gets a wait - the step itself is still emitted either
// way.
func TestEmit_ScreenChangeWithoutSomethingStableEmitsNoWait(t *testing.T) {
	cases := []struct {
		name string
		step simflow.Step
		want string // substring the step itself must still produce
	}{
		{
			name: "point rung",
			step: simflow.Step{
				Seq: 1, Kind: simflow.StepTap, ScreenChange: true,
				Choice: simflow.Choice{Rung: simflow.RungPoint, PercentX: 50, PercentY: 80},
			},
			want: `point: "50%,80%"`,
		},
		{
			name: "off screen despite a text rung",
			step: simflow.Step{
				Seq: 1, Kind: simflow.StepTap, ScreenChange: true,
				Choice: simflow.Choice{Rung: simflow.RungText, Text: "See all", Ambiguity: 1, OffScreen: true, ScrollDirection: simflow.ScrollDown},
				Plain:  "See all",
			},
			want: "scrollUntilVisible",
		},
		{
			name: "no selector at all",
			step: simflow.Step{
				Seq: 1, Kind: simflow.StepTap, ScreenChange: true,
				Choice: simflow.Choice{Rung: simflow.RungNone},
			},
			want: "no label, no id and no reachable point",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := simflow.Emit([]simflow.Step{tc.step}, baseOpts())
			if err != nil {
				t.Fatalf("Emit: %v", err)
			}
			if strings.Contains(got, "extendedWaitUntil") {
				t.Fatalf("must not emit extendedWaitUntil for %s, got:\n%s", tc.name, got)
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("the step itself must still be emitted for %s (want %q), got:\n%s", tc.name, tc.want, got)
			}
		})
	}
}

// Same trap slice 1 measured: a YAML double-quoted scalar processes backslash
// escapes, so a regex escape written with a single backslash is not valid
// YAML and makes the whole document fail to parse.
func TestEmit_EscapedSelectorIsValidYAML(t *testing.T) {
	steps := []simflow.Step{
		{
			Seq: 1, Kind: simflow.StepTap,
			Choice: simflow.Choice{Rung: simflow.RungText, Text: `See all \(12\)`, Escaped: true, Ambiguity: 1},
			Plain:  "See all (12)",
		},
	}
	got, err := simflow.Emit(steps, baseOpts())
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if strings.Contains(got, `"See all \(12\)"`) {
		t.Fatalf("emitted a single-backslash escape, which YAML rejects: %q", got)
	}
	if !strings.Contains(got, `"See all \\(12\\)"`) {
		t.Fatalf("want the backslash doubled for YAML, got:\n%s", got)
	}
}
