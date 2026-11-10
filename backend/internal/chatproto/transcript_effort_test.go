package chatproto

import (
	"fmt"
	"testing"
)

const (
	applyEffortRequest  = `{"type":"control_request","request_id":"%s","request":{"subtype":"apply_flag_settings","settings":{"effortLevel":"%s"}}}`
	successNoBody       = `{"type":"control_response","response":{"subtype":"success","request_id":"%s"}}`
	refusedControl      = `{"type":"control_response","response":{"subtype":"error","request_id":"%s","error":"nope"}}`
	getSettingsRequest  = `{"type":"control_request","request_id":"%s","request":{"subtype":"get_settings"}}`
	getSettingsResponse = `{"type":"control_response","response":{"subtype":"success","request_id":"%s","response":{"effective":{},"sources":{},"applied":{"model":"claude-sonnet-5-5","effort":"%s"}}}}`
)

func TestSpawnTakesTheEffortFromItsCommandLine(t *testing.T) {
	for _, tt := range []struct{ argv, want string }{
		{`["claude","--effort","high"]`, "high"},
		{`["claude","--effort=low"]`, "low"},
		{`["claude","--","--effort","high"]`, ""},
		{`["claude","--effort"]`, ""},
		{`["claude","--model","opus"]`, ""},
	} {
		tr := foldEntries(t, []Entry{synth(1, DirHost, `{"kind":"spawn","resume":"","argv":`+tt.argv+`}`)})
		if tr.Effort != tt.want || tr.ChosenEffort != "" {
			t.Errorf("spawn %s: Effort = %q ChosenEffort = %q, want %q and no chosen level", tt.argv, tr.Effort, tr.ChosenEffort, tt.want)
		}
	}
}

func TestFoldTakesTheEffortFromAGetSettingsAnswer(t *testing.T) {
	tr := foldEntries(t, []Entry{
		synth(1, DirHost, `{"kind":"spawn","argv":["claude"]}`),
		synth(2, DirIn, fmt.Sprintf(getSettingsRequest, "gs-1")),
		synth(3, DirOut, fmt.Sprintf(getSettingsResponse, "gs-1", "medium")),
	})
	if tr.Effort != "medium" || tr.ChosenEffort != "" {
		t.Errorf("Effort = %q ChosenEffort = %q, want medium in use and nothing chosen", tr.Effort, tr.ChosenEffort)
	}

	missing := foldEntries(t, []Entry{
		synth(1, DirHost, `{"kind":"spawn","argv":["claude","--effort","low"]}`),
		synth(2, DirIn, fmt.Sprintf(getSettingsRequest, "gs-1")),
		synth(3, DirOut, `{"type":"control_response","response":{"subtype":"success","request_id":"gs-1","response":{"applied":{"model":"m"}}}}`),
	})
	if missing.Effort != "low" {
		t.Errorf("an answer without applied.effort left Effort = %q, want the spawn's low", missing.Effort)
	}
}

func TestFoldFollowsAnAcceptedEffortChange(t *testing.T) {
	entries := []Entry{
		synth(1, DirHost, `{"kind":"spawn","argv":["claude","--effort","high"]}`),
		synth(2, DirIn, fmt.Sprintf(applyEffortRequest, "ef-1", "low")),
		synth(3, DirOut, fmt.Sprintf(successNoBody, "ef-1")),
		synth(4, DirIn, fmt.Sprintf(applyEffortRequest, "ef-2", "max")),
		synth(5, DirOut, fmt.Sprintf(refusedControl, "ef-2")),
	}
	if tr := foldEntries(t, entries[:2]); tr.Effort != "high" || tr.ChosenEffort != "" {
		t.Errorf("before claude answered: Effort = %q ChosenEffort = %q, want the spawn's high and no choice", tr.Effort, tr.ChosenEffort)
	}
	if tr := foldEntries(t, entries[:3]); tr.Effort != "low" || tr.ChosenEffort != "low" {
		t.Errorf("after the accepted change: Effort = %q ChosenEffort = %q, want low and low", tr.Effort, tr.ChosenEffort)
	}
	if tr := foldEntries(t, entries); tr.Effort != "low" || tr.ChosenEffort != "low" {
		t.Errorf("after the refused change: Effort = %q ChosenEffort = %q, want it unchanged", tr.Effort, tr.ChosenEffort)
	}
}

func TestFoldKeepsTheChosenEffortAcrossARespawn(t *testing.T) {
	tr := foldEntries(t, []Entry{
		synth(1, DirIn, fmt.Sprintf(applyEffortRequest, "ef-1", "low")),
		synth(2, DirOut, fmt.Sprintf(successNoBody, "ef-1")),
		synth(3, DirHost, `{"kind":"exit","code":0}`),
		synth(4, DirHost, `{"kind":"spawn","argv":["claude"]}`),
	})
	if tr.ChosenEffort != "low" || tr.Effort != "" {
		t.Errorf("after a respawn: ChosenEffort = %q Effort = %q, want the choice kept and the level in use unknown until claude says", tr.ChosenEffort, tr.Effort)
	}
}

func TestFoldIgnoresControlRequestsThatCarryNoEffort(t *testing.T) {
	tr := foldEntries(t, []Entry{
		synth(1, DirIn, `{"type":"control_request","request_id":"a","request":{"subtype":"apply_flag_settings","settings":{"theme":"dark"}}}`),
		synth(2, DirOut, fmt.Sprintf(successNoBody, "a")),
		synth(3, DirIn, `{"type":"control_request","request_id":"b","request":{"subtype":"apply_flag_settings","settings":{"effortLevel":null}}}`),
		synth(4, DirOut, fmt.Sprintf(successNoBody, "b")),
	})
	if tr.Effort != "" || tr.ChosenEffort != "" {
		t.Errorf("Effort = %q ChosenEffort = %q, want neither set", tr.Effort, tr.ChosenEffort)
	}
}
