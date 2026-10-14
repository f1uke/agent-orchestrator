package releasetrain

import (
	"encoding/json"
	"reflect"
	"testing"
)

type wireLLB struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Plan     map[string]any `json:"plan"`
	Target   map[string]any `json:"target"`
	Branches []struct {
		App      string `json:"app"`
		Branch   string `json:"branch"`
		OpenedOn string `json:"openedOn"`
		State    string `json:"state"`
		Ahead    *int   `json:"ahead"`
		Train    string `json:"train"`
		Landing  *struct {
			IID int `json:"iid"`
		} `json:"landing"`
		Cards []struct {
			Key string         `json:"key"`
			MR  map[string]any `json:"mr"`
		} `json:"cards"`
	} `json:"branches"`
}

func TestBoardJSONCarriesLongLivedGroupsTheirTargetKindAndCutover(t *testing.T) {
	f := newFixture(t, today)
	f.version("di", "21", "3.5.0", liveFreeze, liveDue)
	f.version("di", "22", "3.6.0", "2026-10-20", "2026-10-27")
	f.mr("di", "DEMO-50 other", "feat/DEMO-50", "feature/sprint-20-2026", MRMerged, "2026-10-01", "2026-10-01")
	f.mr("di", "DEMO-1 wallet", "feat/DEMO-1", "feature/demo-wallet", MRMerged, "2026-09-01", "2026-09-02")
	f.mr("di", "DEMO-2 bond", "feat/DEMO-2", "feature/demo-bond", MRMerged, "2026-09-01", "2026-09-02")
	f.mr("di", "bond", "feature/demo-bond", "feature/sprint-21-2026", MRMerged, "2026-10-12", "2026-10-12")
	f.mr("di", "DEMO-3 chat", "feat/DEMO-3", "feature/demo-chat", MROpened, "2026-09-01", "")
	f.ahead("di", "feature/demo-wallet", 2)
	f.human.Plans = []LlbPlan{
		{ID: "demo-wallet", Name: "Wallet", TargetKind: TargetTrain, TargetTrain: &TrainID{Number: 22, Year: fixtureYear}, Version: 3, Note: "after contract"},
		{ID: "demo-chat", Name: "Chat", TargetKind: TargetDropped, Version: 1},
	}
	raw, err := json.Marshal(f.reconcile())
	if err != nil {
		t.Fatal(err)
	}
	var w struct {
		LLBs    []wireLLB         `json:"llbs"`
		Cutover map[string]string `json:"cutover"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, g := range w.LLBs {
		b := g.Branches[0]
		train, _ := g.Target["train"].(string)
		got[g.ID] = g.Target["kind"].(string) + " " + train + " " + b.State + " " + b.Train
		if len(b.Cards) != 1 || b.Cards[0].MR["iid"] == nil || b.OpenedOn != "2026-09-01" {
			t.Fatalf("%s branch = %+v, want one card with its MR and the opened day", g.ID, b)
		}
	}
	want := map[string]string{
		"demo-wallet": "planned 22-2026 open ",
		"demo-bond":   "landed 22-2026 landed 22-2026",
		"demo-chat":   "dropped  open ",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("groups = %v, want %v", got, want)
	}
	for _, g := range w.LLBs {
		if g.ID == "demo-wallet" && (g.Plan["version"] != 3.0 || g.Plan["note"] != "after contract" || *g.Branches[0].Ahead != 2) {
			t.Fatalf("wallet = %+v, want the plan's version and note and ahead 2", g)
		}
	}
	if !reflect.DeepEqual(w.Cutover, map[string]string{"di": "2026-10-01"}) {
		t.Fatalf("cutover = %v", w.Cutover)
	}
}

func TestBoardJSONHasEmptyLongLivedListsNotNull(t *testing.T) {
	raw, err := json.Marshal(newFixture(t, today).reconcile())
	if err != nil {
		t.Fatal(err)
	}
	var w map[string]json.RawMessage
	if err := json.Unmarshal(raw, &w); err != nil {
		t.Fatal(err)
	}
	if string(w["llbs"]) != "[]" || string(w["cutover"]) != "{}" {
		t.Fatalf("llbs = %s, cutover = %s; want [] and {}", w["llbs"], w["cutover"])
	}
}
