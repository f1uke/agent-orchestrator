package specgen

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
)

func TestReleaseTrainUnionTwinsMatchWhatTheCoreWrites(t *testing.T) {
	at := time.Date(2026, 10, 14, 3, 0, 0, 0, time.UTC)
	degraded := []rt.ResourceStatus{{Kind: "jira.versions", Scope: "project", CompleteAt: &at, ErrorClass: rt.ErrUnavailable}}
	for _, tc := range []struct {
		variant rt.Variant
		twin    any
	}{
		{rt.Clean{CheckedAt: at}, rtOutcomeClean{}},
		{rt.Fired{Hits: []rt.Hit{{ID: "C1~v/21-2026/di"}}, AsOf: at, Degraded: degraded}, rtOutcomeFired{}},
		{rt.Unknown{Missing: degraded}, rtOutcomeUnknown{}},
		{rt.CodeMerged{MRs: []int{7}}, rtCodeMerged{}},
		{rt.CodeMerged{MRs: []int{7}, Via: "feature/demo-wallet"}, rtCodeMerged{}},
		{rt.CodeParked{Branch: "feature/demo-wallet", MRs: []int{9}}, rtCodeParked{}},
		{rt.LLBLandedIn{Train: rt.TrainID{Number: 21, Year: 2026}}, rtLLBLandedIn{}},
		{rt.LLBPlanned{Train: rt.TrainID{Number: 20, Extra: 1, Year: 2026}}, rtLLBPlanned{}},
		{rt.LLBFromEpic{Train: rt.TrainID{Number: 22, Year: 2026}, Epic: "DEMO-500", Covered: 10, Total: 11}, rtLLBFromEpic{}},
		{rt.LLBFromEpic{Train: rt.TrainID{Number: 21, Year: 2026}, Epic: "DEMO-500", Covered: 2, Total: 2, Also: []rt.TrainID{{Number: 22, Year: 2026}}}, rtLLBFromEpic{}},
		{rt.LLBNoTarget{}, rtLLBNoTarget{}},
		{rt.LLBDropped{}, rtLLBDropped{}},
		{rt.IdentityKnown{JiraAccountID: "acc-dee", GitLabUsername: "dee", DisplayName: "Dee"}, rtIdentityKnown{}},
		{rt.IdentityUnknown{Reason: "the GitLab user (gitlab.user) has not been read"}, rtIdentityUnknown{}},
		{rt.CodeOpen{MRs: []int{8}}, rtCodeOpen{}},
		{rt.CodeElsewhere{Branch: "feature/demo", On: rt.Day{Y: 2026, M: 10, D: 1}}, rtCodeElsewhere{}},
		{rt.CodeNone{}, rtCodeNone{}},
		{rt.ToTrain{Train: rt.TrainID{Number: 8, Year: 2026}}, rtDestToTrain{}},
		{rt.ToLLB{Group: "demo-chat", Branch: "feature/demo-chat", Via: &rt.TrainID{Number: 9, Year: 2026}}, rtDestToLLB{}},
		{rt.ToLLB{Branch: "feature/demo-chat"}, rtDestToLLB{}},
		{rt.BeforeFirstTrain{Train: rt.TrainID{Number: 6, Year: 2026}}, rtDestBefore{}},
		{rt.OffFlow{}, rtDestOff{}},
		{rt.NotRouted{}, rtDestNone{}},
		{rt.EffJiraVersionUpdate{Site: "https://jira.example.com", VersionID: "5001", After: rt.VersionFields{Released: true, ReleaseDate: &rt.Day{Y: 2026, M: 10, D: 8}}}, rtEffJiraVersionUpdate{}},
		{rt.EffJiraTransition{Site: "https://jira.example.com", Key: "DEMO-1", From: "Pull Request", To: "Ready for QA"}, rtEffJiraTransition{}},
		{rt.EffJiraApprovers{Site: "https://jira.example.com", VersionID: "5001", After: []rt.Person{{AccountID: "acc-ann", Name: "Ann"}}}, rtEffJiraApprovers{}},
		{rt.EffJiraRelatedWork{Site: "https://jira.example.com", VersionID: "5001", Category: "Development", Title: "Source code scan 3.5.0"}, rtEffJiraRelatedWork{}},
		{rt.EffGitLabRetarget{Host: "gitlab.example.com", App: "di", Project: 1001, IID: 7, From: "develop", To: "feature/sprint-2026-7"}, rtEffGitLabRetarget{}},
		{rt.EffGitLabJobRetry{Host: "gitlab.example.com", App: "di", Project: 1001, JobID: 901, Name: "build_test"}, rtEffGitLabJobRetry{}},
		{rt.EffGitLabMilestoneDue{Host: "gitlab.example.com", App: "di", Project: 1001, MilestoneID: 11, Before: rt.Day{Y: 2026, M: 10, D: 16}, After: rt.Day{Y: 2026, M: 10, D: 17}}, rtEffGitLabMilestoneDue{}},
		{rt.EffCopyText{Target: "https://wiki.example.com/pages/8001", Text: "DEMO-1\nDEMO-2"}, rtEffCopyText{}},
		{rt.EffAOPlanTarget{Plan: "demo-wallet", Before: rt.LLBTargetInput{Kind: rt.TargetUnplanned}, After: rt.LLBTargetInput{Kind: rt.TargetTrain, Train: &rt.TrainID{Number: 21, Year: 2026}}, PlanVersion: 3}, rtEffAOPlanTarget{}},
	} {
		body, err := json.Marshal(tc.variant)
		if err != nil {
			t.Fatal(err)
		}
		twin := reflect.New(reflect.TypeOf(tc.twin))
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.DisallowUnknownFields()
		if err := dec.Decode(twin.Interface()); err != nil {
			t.Fatalf("%T writes %s, which its spec twin %T cannot read: %v", tc.variant, body, tc.twin, err)
		}
		again, err := json.Marshal(twin.Interface())
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(again, body) {
			t.Fatalf("%T writes %s; its spec twin %T writes %s", tc.variant, body, tc.twin, again)
		}
		field, _ := reflect.TypeOf(tc.twin).FieldByName("Kind")
		if got := field.Tag.Get("const"); got != tc.variant.Variant() {
			t.Fatalf("%T declares kind %q, the core writes %q", tc.twin, got, tc.variant.Variant())
		}
	}
}
