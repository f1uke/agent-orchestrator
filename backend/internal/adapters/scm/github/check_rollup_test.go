package github

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// checkRun builds one CheckRun rollup node. startedAt may be empty, which is how
// the tiebreak-by-databaseId path is exercised.
func checkRun(name, conclusion, startedAt string, databaseID float64) map[string]any {
	n := map[string]any{
		"__typename": "CheckRun",
		"name":       name,
		"status":     "COMPLETED",
		"conclusion": conclusion,
		"databaseId": databaseID,
	}
	if startedAt != "" {
		n["startedAt"] = startedAt
	}
	return n
}

// rollupPR wraps rollup contexts in the pullRequest shape the projections read.
func rollupPR(state string, nodes ...map[string]any) map[string]any {
	list := make([]any, 0, len(nodes))
	for _, n := range nodes {
		list = append(list, n)
	}
	return map[string]any{
		"commits": map[string]any{"nodes": []any{
			map[string]any{"commit": map[string]any{"statusCheckRollup": map[string]any{
				"state": state,
				"contexts": map[string]any{
					"nodes":    list,
					"pageInfo": map[string]any{"hasNextPage": false},
				},
			}}},
		}},
	}
}

// TestCISummaryResolvesEachCheckNameToItsLatestRun is the #287 regression: GitHub
// keeps a superseded run in the rollup (and keeps the rollup's own state at
// FAILURE for it), so the verdict has to come from the LATEST run of each name.
func TestCISummaryResolvesEachCheckNameToItsLatestRun(t *testing.T) {
	cases := []struct {
		name       string
		rollup     string
		nodes      []map[string]any
		want       domain.CIState
		wantChecks []string
	}{
		{
			name:   "re-run passed after failing",
			rollup: "FAILURE",
			nodes: []map[string]any{
				checkRun("format", "FAILURE", "2026-09-03T08:13:58Z", 1),
				checkRun("test", "SUCCESS", "2026-09-03T08:13:58Z", 2),
				checkRun("format", "SUCCESS", "2026-09-03T08:16:09Z", 3),
			},
			want:       domain.CIPassing,
			wantChecks: []string{"format", "test"},
		},
		{
			name:   "re-run failed after passing",
			rollup: "FAILURE",
			nodes: []map[string]any{
				checkRun("format", "SUCCESS", "2026-09-03T08:13:58Z", 1),
				checkRun("format", "FAILURE", "2026-09-03T08:16:09Z", 2),
			},
			want:       domain.CIFailing,
			wantChecks: []string{"format"},
		},
		{
			name:   "a different check is still failing",
			rollup: "FAILURE",
			nodes: []map[string]any{
				checkRun("format", "FAILURE", "2026-09-03T08:13:58Z", 1),
				checkRun("format", "SUCCESS", "2026-09-03T08:16:09Z", 2),
				checkRun("lint", "FAILURE", "2026-09-03T08:16:09Z", 3),
			},
			want:       domain.CIFailing,
			wantChecks: []string{"format", "lint"},
		},
		{
			name:       "single failing run",
			rollup:     "FAILURE",
			nodes:      []map[string]any{checkRun("format", "FAILURE", "2026-09-03T08:13:58Z", 1)},
			want:       domain.CIFailing,
			wantChecks: []string{"format"},
		},
		{
			name:   "no timestamps falls back to the run id",
			rollup: "FAILURE",
			nodes: []map[string]any{
				checkRun("format", "FAILURE", "", 100574607346),
				checkRun("format", "SUCCESS", "", 100575231243),
			},
			want:       domain.CIPassing,
			wantChecks: []string{"format"},
		},
		{
			name:   "a re-run commit status supersedes its earlier verdict",
			rollup: "FAILURE",
			nodes: []map[string]any{
				{"__typename": "StatusContext", "context": "ci/legacy", "state": "FAILURE", "createdAt": "2026-09-03T08:13:58Z"},
				{"__typename": "StatusContext", "context": "ci/legacy", "state": "SUCCESS", "createdAt": "2026-09-03T08:16:09Z"},
			},
			want:       domain.CIPassing,
			wantChecks: []string{"ci/legacy"},
		},
		{
			name:   "a pending rollup outranks an otherwise-passing set",
			rollup: "PENDING",
			nodes: []map[string]any{
				checkRun("format", "FAILURE", "2026-09-03T08:13:58Z", 1),
				checkRun("format", "SUCCESS", "2026-09-03T08:16:09Z", 2),
			},
			want:       domain.CIPending,
			wantChecks: []string{"format"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pr := rollupPR(tc.rollup, tc.nodes...)
			if got := ciSummaryFromRollup(pr); got != tc.want {
				t.Fatalf("ciSummaryFromRollup = %q, want %q", got, tc.want)
			}
			// The single-PR Observe path reads the same rollup and must agree,
			// except that it has no aggregate-pending rule of its own.
			if tc.rollup != "PENDING" {
				if got := ciSummaryFromGraphQL(pr); got != tc.want {
					t.Fatalf("ciSummaryFromGraphQL = %q, want %q", got, tc.want)
				}
			}
			var names []string
			for _, ch := range scmChecksFromGraphQL(pr) {
				names = append(names, ch.Name)
			}
			if strings.Join(names, ",") != strings.Join(tc.wantChecks, ",") {
				t.Fatalf("check rows = %v, want one row per check name %v", names, tc.wantChecks)
			}
		})
	}
}

// TestSupersededFailureLeavesNoFailedCheckRow pins the consequence a human sees:
// a check that failed and then passed contributes no failed row, so the board's
// ci_failed status and the CI-failing nudge both stand down.
func TestSupersededFailureLeavesNoFailedCheckRow(t *testing.T) {
	pr := rollupPR("FAILURE",
		checkRun("format", "FAILURE", "2026-09-03T08:13:58Z", 1),
		checkRun("format", "SUCCESS", "2026-09-03T08:16:09Z", 2),
	)
	obs := scmObservationFromGraphQL(ports.SCMPRRef{Repo: ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "octocat", Name: "hello", Repo: "octocat/hello"}, Number: 287}, pr)
	if obs.CI.Summary != string(domain.CIPassing) {
		t.Fatalf("CI summary = %q, want passing", obs.CI.Summary)
	}
	if len(obs.CI.FailedChecks) != 0 {
		t.Fatalf("failed checks = %#v, want none", obs.CI.FailedChecks)
	}
	if len(obs.CI.Checks) != 1 || obs.CI.Checks[0].Conclusion != "success" {
		t.Fatalf("checks = %#v, want the latest run only", obs.CI.Checks)
	}
}

// TestFetchPullRequestsResolvesSupersededRunAcrossContextPages is the whole
// observer path as #287 hit it: 20 contexts per page, so the failing run and the
// re-run that replaced it arrive on DIFFERENT pages and must still collapse.
func TestFetchPullRequestsResolvesSupersededRunAcrossContextPages(t *testing.T) {
	fake := newFakeGH(t)
	fx := basePRFixture()
	var pr map[string]any
	fx.prData(func(m map[string]any) {
		pr = m
		commit := m["commits"].(map[string]any)["nodes"].([]any)[0].(map[string]any)["commit"].(map[string]any)
		roll := commit["statusCheckRollup"].(map[string]any)
		roll["state"] = "FAILURE"
		ctxs := roll["contexts"].(map[string]any)
		ctxs["nodes"] = []any{checkRun("format", "FAILURE", "2026-09-03T08:13:58Z", 100574607346)}
		ctxs["pageInfo"] = map[string]any{"hasNextPage": true, "endCursor": "cursor-1"}
	})
	fake.on(http.MethodPost, "/graphql", func(w http.ResponseWriter, r *http.Request) {
		call := fake.callsTo(http.MethodPost, "/graphql")
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch call {
		case 1:
			if !strings.Contains(string(body), "startedAt") {
				t.Fatalf("batch query must ask for startedAt to order re-runs, body=%s", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"pr0": map[string]any{"pullRequest": pr}}})
		case 2:
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"repo": map[string]any{"pullRequest": map[string]any{
				"commits": map[string]any{"nodes": []any{map[string]any{"commit": map[string]any{"statusCheckRollup": map[string]any{
					"contexts": map[string]any{
						"nodes":    []any{checkRun("format", "SUCCESS", "2026-09-03T08:16:09Z", 100575231243)},
						"pageInfo": map[string]any{"hasNextPage": false, "endCursor": nil},
					},
				}}}}},
			}}}})
		default:
			t.Fatalf("unexpected graphql call %d", call)
		}
	})
	p := newProviderForTest(t, fake)
	obs, err := p.FetchPullRequests(ctx(), []ports.SCMPRRef{{Repo: ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "octocat", Name: "hello", Repo: "octocat/hello"}, Number: 287}})
	if err != nil {
		t.Fatalf("FetchPullRequests: %v", err)
	}
	if len(obs) != 1 {
		t.Fatalf("observations = %#v", obs)
	}
	if obs[0].CI.Summary != string(domain.CIPassing) {
		t.Fatalf("CI summary = %q, want passing (the re-run passed)", obs[0].CI.Summary)
	}
	if len(obs[0].CI.Checks) != 1 || len(obs[0].CI.FailedChecks) != 0 {
		t.Fatalf("checks = %#v failed = %#v, want the latest run only", obs[0].CI.Checks, obs[0].CI.FailedChecks)
	}
}

// TestObserveResolvesSupersededRun covers the single-PR Observe path end to end,
// including that it no longer chases the log tail of a superseded failure.
func TestObserveResolvesSupersededRun(t *testing.T) {
	fake := newFakeGH(t)
	fx := basePRFixture()
	fx.prData(func(pr map[string]any) {
		commit := pr["commits"].(map[string]any)["nodes"].([]any)[0].(map[string]any)["commit"].(map[string]any)
		roll := commit["statusCheckRollup"].(map[string]any)
		roll["state"] = "FAILURE"
		roll["contexts"].(map[string]any)["nodes"] = []any{
			checkRun("format", "FAILURE", "2026-09-03T08:13:58Z", 100574607346),
			checkRun("format", "SUCCESS", "2026-09-03T08:16:09Z", 100575231243),
		}
	})
	fx.install(t, fake)
	p := newProviderForTest(t, fake)

	obs, err := p.Observe(ctx(), fx.prURL())
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if obs.CI != domain.CIPassing {
		t.Fatalf("CI = %q, want passing", obs.CI)
	}
	if len(obs.Checks) != 1 || obs.Checks[0].Status != domain.PRCheckPassed {
		t.Fatalf("checks = %#v, want the latest run only", obs.Checks)
	}
	// No handler is registered for /actions/jobs/…/logs, so a log-tail fetch for
	// the superseded failure would have been recorded as an unexpected request.
	for _, c := range fake.calls() {
		if strings.Contains(c.Path, "/actions/jobs/") {
			t.Fatalf("fetched a log tail for a superseded run: %s", c.Path)
		}
	}
}
