package chatcontext

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func shapeLine(hash, ts string, system []string, tools ...string) string {
	blocks := make([]map[string]string, 0, len(system))
	for _, s := range system {
		blocks = append(blocks, map[string]string{"type": "text", "text": s})
	}
	toolList := make([]map[string]any, 0, len(tools))
	for _, name := range tools {
		toolList = append(toolList, map[string]any{
			"name":         name,
			"description":  name + " does a thing",
			"input_schema": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]string{"type": "string"}}},
		})
	}
	b, _ := json.Marshal(map[string]any{
		"type": "api-request-shape", "shapeHash": hash, "timestamp": ts,
		"shape": map[string]any{"system": blocks, "tools": toolList},
	})
	return string(b)
}

func attachmentLine(source, role, ts string, contents ...any) string {
	rendered := make([]map[string]any, 0, len(contents))
	for _, c := range contents {
		rendered = append(rendered, map[string]any{"content": c})
	}
	rec := map[string]any{
		"type": "attachment", "timestamp": ts, "isSidechain": false,
		"attachment": map[string]string{"type": source},
	}
	if contents != nil {
		rec["rendered"] = rendered
		rec["renderedRole"] = role
	}
	b, _ := json.Marshal(rec)
	return string(b)
}

func read(t *testing.T, lines ...string) Context {
	t.Helper()
	ctx, err := Read(strings.NewReader(strings.Join(lines, "\n") + "\n"))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return ctx
}

func TestReadsTheSystemPromptAndToolsOfEachDistinctShape(t *testing.T) {
	ctx := read(t,
		shapeLine("h1", "2026-11-07T10:00:00.000Z", []string{"You are a coding agent.", "Be brief."}, "Read", "Bash"),
		`{"type":"api-request","shapeHash":"h1","timestamp":"2026-11-07T10:00:00.001Z"}`,
		shapeLine("h1", "2026-11-07T10:00:05.000Z", []string{"ignored repeat"}),
		shapeLine("h2", "2026-11-07T10:01:00.000Z", []string{"You are a coding agent."}, "Read", "Bash", "Grep"),
	)
	if len(ctx.Shapes) != 2 {
		t.Fatalf("shapes = %d, want 2", len(ctx.Shapes))
	}
	first := ctx.Shapes[0]
	if got := strings.Join(first.System, "|"); got != "You are a coding agent.|Be brief." {
		t.Errorf("system = %q", got)
	}
	if want := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC); !first.TS.Equal(want) {
		t.Errorf("ts = %v, want %v", first.TS, want)
	}
	if len(first.Tools) != 2 || first.Tools[1].Name != "Bash" || first.Tools[1].Description != "Bash does a thing" {
		t.Errorf("tools = %+v", first.Tools)
	}
	if !strings.Contains(string(first.Tools[0].InputSchema), `"path"`) {
		t.Errorf("input schema = %s", first.Tools[0].InputSchema)
	}
	if len(ctx.Shapes[1].Tools) != 3 {
		t.Errorf("second shape tools = %d, want 3", len(ctx.Shapes[1].Tools))
	}
	if ctx.Truncated {
		t.Error("truncated without a cap being hit")
	}
}

func TestReadsInjectionsWithTextContentInEitherForm(t *testing.T) {
	ctx := read(t,
		attachmentLine("skill_listing", "system", "2026-11-07T10:00:01Z", "<system-reminder>\nskills: deploy\n</system-reminder>"),
		attachmentLine("instructions", "user", "2026-11-07T10:00:02Z",
			[]map[string]string{{"type": "text", "text": "Use dashes."}, {"type": "image", "text": ""}, {"type": "text", "text": "Keep it short."}}),
		attachmentLine("queued_command", "system", "2026-11-07T10:00:03Z", "first", "second"),
	)
	got := make([]string, 0, len(ctx.Injections))
	for _, in := range ctx.Injections {
		got = append(got, fmt.Sprintf("%s/%s/%q", in.Source, in.Role, in.Text))
	}
	want := []string{
		`skill_listing/system/"<system-reminder>\nskills: deploy\n</system-reminder>"`,
		`instructions/user/"Use dashes.\nKeep it short."`,
		`queued_command/system/"first\nsecond"`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("injections:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestSkipsWhatTheModelNeverSaw(t *testing.T) {
	sidechain := strings.Replace(attachmentLine("date", "system", "2026-11-07T10:00:00Z", "subagent date"), `"isSidechain":false`, `"isSidechain":true`, 1)
	ctx := read(t,
		attachmentLine("command_permissions", "", "2026-11-07T10:00:00Z"),
		attachmentLine("prompt_snapshot", "system", "2026-11-07T10:00:00Z", ""),
		sidechain,
		attachmentLine("date", "system", "2026-11-07T10:00:00Z", "Today's date is 2026-11-07."),
	)
	if len(ctx.Injections) != 1 || ctx.Injections[0].Source != "date" {
		t.Errorf("injections = %+v, want only the date", ctx.Injections)
	}
}

func TestSurvivesJunkLines(t *testing.T) {
	ctx := read(t,
		"not json at all",
		`{"type":"attachment","attachment":`,
		`{"type":"user","message":{"content":"mentions \"type\":\"attachment\" in text"}}`,
		"",
		attachmentLine("date", "system", "bad time", "Today's date is 2026-11-07."),
	)
	if len(ctx.Injections) != 1 {
		t.Fatalf("injections = %+v, want one", ctx.Injections)
	}
	if !ctx.Injections[0].TS.IsZero() {
		t.Errorf("ts = %v, want zero for an unparseable timestamp", ctx.Injections[0].TS)
	}
	if len(ctx.Shapes) != 0 {
		t.Errorf("shapes = %+v, want none", ctx.Shapes)
	}
}

func TestCutsALongTextOnARuneBoundary(t *testing.T) {
	long := strings.Repeat("é", MaxTextBytes)
	ctx := read(t, attachmentLine("hook_success", "system", "2026-11-07T10:00:00Z", long))
	first := ctx.Injections[0]
	if !ctx.Truncated || !first.Truncated || len(first.Text) > MaxTextBytes || !strings.HasSuffix(first.Text, "é") {
		t.Errorf("text: truncated=%v len=%d, want cut on a rune boundary within %d", first.Truncated, len(first.Text), MaxTextBytes)
	}
}

func TestKeepsTheNewestInjectionsAndTheFirstShapes(t *testing.T) {
	lines := []string{}
	for i := range MaxInjections + 5 {
		lines = append(lines, attachmentLine("date", "system", "2026-11-07T10:00:00Z", fmt.Sprintf("n%d", i)))
	}
	for i := range MaxShapes + 3 {
		lines = append(lines, shapeLine(fmt.Sprintf("h%d", i), "2026-11-07T10:00:00Z", []string{"s"}))
	}
	ctx := read(t, lines...)
	if !ctx.Truncated {
		t.Error("not marked truncated")
	}
	if len(ctx.Injections) != MaxInjections {
		t.Errorf("injections = %d, want %d", len(ctx.Injections), MaxInjections)
	}
	if got, want := ctx.Injections[len(ctx.Injections)-1].Text, fmt.Sprintf("n%d", MaxInjections+4); got != want {
		t.Errorf("last kept %q, want the newest %q", got, want)
	}
	if got := ctx.Injections[0].Text; got != "n5" {
		t.Errorf("first kept %q, want n5: the oldest are the ones dropped", got)
	}
	if len(ctx.Shapes) != MaxShapes {
		t.Errorf("shapes = %d, want %d", len(ctx.Shapes), MaxShapes)
	}
}

func TestSkipsAnOverlongLineAndReadsOn(t *testing.T) {
	prev := maxLineBytes
	maxLineBytes = 1 << 10
	t.Cleanup(func() { maxLineBytes = prev })
	ctx := read(t,
		attachmentLine("date", "system", "2026-11-07T10:00:00Z", "before"),
		attachmentLine("hook_success", "system", "2026-11-07T10:00:01Z", strings.Repeat("x", 200<<10)),
		attachmentLine("date", "system", "2026-11-07T10:00:02Z", "after"),
	)
	if len(ctx.Injections) != 2 || ctx.Injections[0].Text != "before" || ctx.Injections[1].Text != "after" {
		t.Errorf("injections = %+v, want before and after", ctx.Injections)
	}
	if !ctx.Truncated {
		t.Error("a skipped line should mark the context truncated")
	}
}
