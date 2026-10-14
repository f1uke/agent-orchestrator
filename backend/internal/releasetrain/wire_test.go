package releasetrain

import (
	"encoding/json"
	"sort"
	"testing"
)

type wireBoard struct {
	Apps   []map[string]any `json:"apps"`
	Trains []struct {
		ID       string `json:"id"`
		Label    string `json:"label"`
		Phase    string `json:"phase"`
		Versions []struct {
			Cards []struct {
				Code map[string]any `json:"code"`
			} `json:"cards"`
		} `json:"versions"`
	} `json:"trains"`
	Sources []struct {
		System string           `json:"system"`
		Issues []map[string]any `json:"issues"`
	} `json:"sources"`
	Alerts struct {
		Hits []struct {
			ID    string `json:"id"`
			Train string `json:"train"`
		} `json:"hits"`
		Rules []struct {
			Rule    string         `json:"rule"`
			Outcome map[string]any `json:"outcome"`
		} `json:"rules"`
	} `json:"alerts"`
}

func wire(t *testing.T, b Board) wireBoard {
	t.Helper()
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	var w wireBoard
	if err := json.Unmarshal(raw, &w); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
	return w
}

func TestBoardJSONTagsEverySealedVariantWithItsKind(t *testing.T) {
	f := everyRuleFires(t)
	f.version("da", "22", "3.6.0", "2026-10-20", "2026-10-27").issue("DEMO-9", "To Do", "new")
	f.stale(KindJiraVersions.Name)
	w := wire(t, f.reconcile())

	outcomes := map[string]int{}
	listed := map[string]bool{}
	for _, h := range w.Alerts.Hits {
		listed[h.ID] = true
	}
	for _, r := range w.Alerts.Rules {
		kind, _ := r.Outcome["kind"].(string)
		outcomes[kind]++
		if kind == "fired" {
			ids, ok := r.Outcome["hits"].([]any)
			if !ok || len(ids) == 0 {
				t.Fatalf("%s fired outcome hits = %v, want hit ids", r.Rule, r.Outcome["hits"])
			}
			if id, _ := ids[0].(string); id == "" || (r.Rule != "C2" && r.Rule != "C3" && r.Rule != "E5" && r.Rule != "F1" && !listed[id]) {
				t.Fatalf("%s fired outcome names %v, which the hit list does not carry", r.Rule, ids[0])
			}
		}
	}
	for _, k := range []string{"clean", "fired", "unknown"} {
		if outcomes[k] == 0 {
			t.Fatalf("outcome kinds %v, want some %s", outcomes, k)
		}
	}
	if len(outcomes) != 3 {
		t.Fatalf("outcome kinds %v, want only clean, fired and unknown", outcomes)
	}

	codes := map[string]bool{}
	for _, tr := range w.Trains {
		for _, v := range tr.Versions {
			for _, c := range v.Cards {
				codes[c.Code["kind"].(string)] = true
			}
		}
	}
	if !codes["none"] {
		t.Fatalf("card code kinds %v, want none for a card with no MR", codes)
	}
}

func TestBoardJSONNamesTrainsByKeyWithALabelAndAPhase(t *testing.T) {
	f := newFixture(t, today)
	f.published("di", "20", "3.4.0", "2026-10-01", "2026-10-08")
	healthy(f)
	f.version("di", "22", "3.6.0", "2026-10-20", "2026-10-27")
	w := wire(t, f.reconcile())

	var got []string
	for _, tr := range w.Trains {
		got = append(got, tr.ID+" "+tr.Label+" "+tr.Phase)
	}
	want := []string{"20-2026 #20 past", "21-2026 #21 current", "22-2026 #22 next"}
	if len(got) != len(want) {
		t.Fatalf("trains = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("trains = %v, want %v", got, want)
		}
	}
	for _, h := range w.Alerts.Hits {
		if h.Train != "" && h.Train != "20-2026" && h.Train != "21-2026" && h.Train != "22-2026" {
			t.Fatalf("hit %s names train %q, want a train key", h.ID, h.Train)
		}
	}
	if len(w.Apps) != 4 || w.Apps[0]["id"] != "di" || w.Apps[0]["name"] != "Demo iOS" || w.Apps[0]["family"] != "demo" {
		t.Fatalf("apps = %v, want the four configured apps with name and family", w.Apps)
	}
}

func TestBoardSourcesListStaleAndFailingResourcesPerSystem(t *testing.T) {
	f := newFixture(t, today)
	healthy(f)
	f.stale(KindJiraVersions.Name)
	f.statuses[KindTags.Name] = func(st *ResourceStatus) { st.ErrorClass = ErrRateLimited }
	w := wire(t, f.reconcile())

	issues := map[string][]string{}
	var systems []string
	for _, s := range w.Sources {
		systems = append(systems, s.System)
		for _, m := range s.Issues {
			issues[s.System] = append(issues[s.System], m["kind"].(string)+" "+m["errorClass"].(string))
		}
	}
	sort.Strings(systems)
	if len(systems) != 2 || systems[0] != "gitlab" || systems[1] != "jira" {
		t.Fatalf("sources = %v, want gitlab and jira", systems)
	}
	if len(issues["jira"]) != 1 || issues["jira"][0] != "jira.versions unavailable" {
		t.Fatalf("jira issues = %v, want the stale jira.versions", issues["jira"])
	}
	if len(issues["gitlab"]) != 4 || issues["gitlab"][0] != "gitlab.tags rate_limited" {
		t.Fatalf("gitlab issues = %v, want the four apps' rate-limited tags, still fresh", issues["gitlab"])
	}
}
