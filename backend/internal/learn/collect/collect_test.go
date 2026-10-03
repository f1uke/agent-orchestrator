package collect

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func turn(id int64, text string, source domain.LearnSourceClass) domain.LearnExcerpt {
	return domain.LearnExcerpt{ID: id, SessionID: "p-1", HumanText: text, SourceClass: source, TurnAt: time.Date(2026, 10, 1, 10, 0, int(id), 0, time.UTC)}
}

func TestDrafts_AnchorOnTheQuoteNotTheTurnReference(t *testing.T) {
	turns := []domain.LearnExcerpt{
		turn(11, "ทำเลย", domain.LearnSourceTyped),
		turn(12, "no - drive the simulator only through\nscripts, never tap by hand", domain.LearnSourceTyped),
	}
	answer := `{"lessons":[
	 {"kind":"correction","statement":"Drive simulators only through scripts.","applies_when":"verifying on a simulator","scope_hint":"global",
	  "quote":"drive the simulator only through scripts","turn":"t1","agent_before":"tapped by hand","supersedes":"","confidence":0.9},
	 {"kind":"rule","statement":"Never deploy on Friday.","applies_when":"deploying","scope_hint":"project",
	  "quote":"never deploy on friday","turn":"t2","agent_before":"","supersedes":"","confidence":0.8},
	 {"kind":"opinion","statement":"x","applies_when":"","scope_hint":"global","quote":"ทำเลย","turn":"t1","agent_before":"","supersedes":"","confidence":0.5}
	]}`
	drafts, rejected, err := Drafts(json.RawMessage(answer), domain.LearnDraft{ProjectID: "p", SessionID: "p-1", TaskKey: "solo:p-1"}, turns, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 1 {
		t.Fatalf("drafts = %+v", drafts)
	}
	d := drafts[0]
	// The model said t1; the quote is in t2 (across a line break), so t2 it is.
	if d.AnchorExcerptID != 12 || d.EvidenceExcerptIDs[0] != 12 || d.TaskKey != "solo:p-1" || d.Status != domain.LearnDraftOpen {
		t.Errorf("draft = %+v", d)
	}
	if len(rejected) != 2 || !strings.Contains(rejected[0].Reason, "not in any human turn") || rejected[1].Reason != "unknown kind" {
		t.Errorf("rejected = %+v; a quote found nowhere is invented and must be refused", rejected)
	}
}

func TestDrafts_FlagsAcceptedSuggestionsAndResolvesSupersedes(t *testing.T) {
	turns := []domain.LearnExcerpt{turn(21, "yes always squash before merge", domain.LearnSourceSuggestionAccepted)}
	open := []domain.LearnDraft{{ID: 7, Statement: "Merge commits are fine."}}
	answer := `{"lessons":[{"kind":"rule","statement":"Squash before merging.","applies_when":"merging","scope_hint":"weird",
	  "quote":"always squash before merge","turn":"t1","agent_before":"","supersedes":"d7","confidence":1.7,"about":"agent_practice"},
	 {"kind":"rule","statement":"Another.","applies_when":"","scope_hint":"repo","quote":"always squash","turn":"t1","agent_before":"","supersedes":"d99","confidence":-1,"about":"invented"}]}`
	drafts, _, err := Drafts(json.RawMessage(answer), domain.LearnDraft{SessionID: "p-1"}, turns, open)
	if err != nil {
		t.Fatal(err)
	}
	if !drafts[0].Weak || drafts[0].SupersedesID != 7 || drafts[0].ScopeHint != "" || drafts[0].Confidence != 1 || drafts[0].About != domain.LearnAboutAgentPractice {
		t.Errorf("first = %+v", drafts[0])
	}
	if drafts[1].SupersedesID != 0 || drafts[1].Confidence != 0 {
		t.Errorf("an unknown draft id must not supersede anything: %+v", drafts[1])
	}
	if drafts[1].About != "" {
		t.Errorf("an unknown about tag must be stored as untagged, not refused by the column check: %q", drafts[1].About)
	}
}

func TestBatches_RespectTurnAndByteBounds(t *testing.T) {
	var turns []domain.LearnExcerpt
	for i := 0; i < MaxBatchTurns+5; i++ {
		turns = append(turns, turn(int64(i), "short", domain.LearnSourceTyped))
	}
	if b := Batches(turns); len(b) != 2 || len(b[0]) != MaxBatchTurns || len(b[1]) != 5 {
		t.Errorf("by turns: %d batches", len(b))
	}
	big := strings.Repeat("x", MaxBatchBytes/2)
	b := Batches([]domain.LearnExcerpt{turn(1, big, domain.LearnSourceTyped), turn(2, big, domain.LearnSourceTyped), turn(3, "s", domain.LearnSourceTyped)})
	if len(b) != 2 || len(b[0]) != 1 {
		t.Errorf("by bytes: %d batches (%d in the first)", len(b), len(b[0]))
	}
}

func TestInput_CarriesTurnsAndOpenDraftsButNoInternalIDs(t *testing.T) {
	in, err := Input(Session{Project: "p", Kind: "worker"}, []domain.LearnExcerpt{turn(99, "hello", domain.LearnSourceTyped)}, []domain.LearnDraft{{ID: 5, Statement: "S"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(in, `"id":"t1"`) || !strings.Contains(in, `"id":"d5"`) || strings.Contains(in, "99") {
		t.Errorf("input = %s", in)
	}
}

func TestTaskKey(t *testing.T) {
	at := time.Date(2026, 10, 3, 23, 0, 0, 0, time.UTC)
	cases := []struct {
		rec   domain.SessionRecord
		found bool
		want  string
	}{
		{domain.SessionRecord{ID: "p-2", CrewID: "p-1"}, true, "crew:p-1"},
		{domain.SessionRecord{ID: "p-9", ProjectID: "p", Kind: domain.KindOrchestrator}, true, "orch:p:2026-10-03"},
		{domain.SessionRecord{ID: "p-3", Kind: domain.KindWorker}, true, "solo:p-3"},
		{domain.SessionRecord{}, false, "solo:p-4"},
	}
	for _, c := range cases {
		id := c.rec.ID
		if id == "" {
			id = "p-4"
		}
		if got := TaskKey(c.rec, c.found, id, at); got != c.want {
			t.Errorf("TaskKey(%+v) = %s, want %s", c.rec, got, c.want)
		}
	}
}

func TestSchemaIsValidJSON(t *testing.T) {
	var v map[string]any
	if err := json.Unmarshal([]byte(Schema), &v); err != nil {
		t.Fatal(err)
	}
}
