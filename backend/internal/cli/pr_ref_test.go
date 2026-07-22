package cli

import (
	"context"
	"testing"
)

func TestResolvePRRefGitLabMRURL(t *testing.T) {
	c := &commandContext{deps: Deps{}.withDefaults()}
	cases := []struct {
		name    string
		ref     string
		want    string
		wantErr bool
	}{
		{
			name: "nested group MR url",
			ref:  "https://gitlab.example.com/group/sub/proj/-/merge_requests/123",
			want: "https://gitlab.example.com/group/sub/proj/-/merge_requests/123",
		},
		{
			name: "trailing sub-tab and query are normalized away",
			ref:  "https://gitlab.example.com/group/proj/-/merge_requests/7/diffs?tab=x",
			want: "https://gitlab.example.com/group/proj/-/merge_requests/7",
		},
		{
			name:    "missing iid is a usage error, not a github fallthrough",
			ref:     "https://gitlab.example.com/group/proj/-/merge_requests/",
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.resolvePRRef(context.Background(), tc.ref, projectDetails{})
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected usage error, got %q", got)
				}
				if ExitCode(err) != 2 {
					t.Fatalf("exit code = %d, want 2 (usage error)", ExitCode(err))
				}
				return
			}
			if err != nil {
				t.Fatalf("resolvePRRef(%q): %v", tc.ref, err)
			}
			if got != tc.want {
				t.Fatalf("resolvePRRef(%q) = %q, want %q", tc.ref, got, tc.want)
			}
		})
	}
}

func TestResolvePRRefGitHubUnchanged(t *testing.T) {
	c := &commandContext{deps: Deps{}.withDefaults()}
	got, err := c.resolvePRRef(context.Background(), "https://github.com/aoagents/agent-orchestrator/pull/142", projectDetails{})
	if err != nil || got != "https://github.com/aoagents/agent-orchestrator/pull/142" {
		t.Fatalf("github url = (%q, %v)", got, err)
	}
	got, err = c.resolvePRRef(context.Background(), "142", projectDetails{Repo: "https://github.com/aoagents/agent-orchestrator"})
	if err != nil || got != "https://github.com/aoagents/agent-orchestrator/pull/142" {
		t.Fatalf("github number = (%q, %v)", got, err)
	}
}
