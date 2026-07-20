package session

import "testing"

// resolveTargetChain is the SINGLE target-branch resolution used by both the
// session read model and the Files panel's Changes mode. These cases pin the
// precedence, because "two independent notions of target branch" is the exact
// bug this feature exists to remove.
func TestResolveTargetChain_Precedence(t *testing.T) {
	open := func(b string) targetPR { return targetPR{Branch: b, Open: true} }
	closed := func(b string) targetPR { return targetPR{Branch: b, Open: false} }

	cases := []struct {
		name           string
		prs            []targetPR
		prTarget       string
		baseBranch     string
		projectDefault string
		wantBranch     string
		wantSource     string
	}{{
		// The forge is ground truth: a PR retargeted directly on GitHub/GitLab
		// must win over AO's stored intent, or AO would report a target the
		// PR does not actually have.
		name: "open PR beats the stored target",
		prs:  []targetPR{open("release/2.1")}, prTarget: "develop", baseBranch: "main", projectDefault: "main",
		wantBranch: "release/2.1", wantSource: TargetFromPR,
	}, {
		name:     "an open PR beats a closed one",
		prs:      []targetPR{closed("old"), open("develop")},
		prTarget: "main", baseBranch: "main", projectDefault: "main",
		wantBranch: "develop", wantSource: TargetFromPR,
	}, {
		name: "a closed PR still beats the stored target",
		prs:  []targetPR{closed("develop")}, prTarget: "main", baseBranch: "main", projectDefault: "main",
		wantBranch: "develop", wantSource: TargetFromPR,
	}, {
		// The headline case: a session with no PR yet reports its STORED target.
		name: "stored target when there is no PR", prTarget: "develop",
		baseBranch: "main", projectDefault: "main",
		wantBranch: "develop", wantSource: TargetFromSessionPRTarget,
	}, {
		// Sessions created before the target was recorded: fall back, never guess
		// in a way that hides the fact that it was inferred.
		name: "legacy session falls back to its base branch", baseBranch: "release/2.1", projectDefault: "main",
		wantBranch: "release/2.1", wantSource: TargetFromSessionBase,
	}, {
		name: "legacy session with nothing stored falls back to the project default", projectDefault: "main",
		wantBranch: "main", wantSource: TargetFromProject,
	}, {
		name:       "nothing known resolves to nothing, never to a guessed main",
		wantBranch: "", wantSource: "",
	}, {
		// A PR row that carries no target branch must not shadow the stored
		// value with an empty string.
		name: "a PR with no recorded target does not shadow the stored one",
		prs:  []targetPR{open("")}, prTarget: "develop", projectDefault: "main",
		wantBranch: "develop", wantSource: TargetFromSessionPRTarget,
	}, {
		name: "whitespace-only stored values are ignored", prTarget: "   ", baseBranch: " ", projectDefault: "main",
		wantBranch: "main", wantSource: TargetFromProject,
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			branch, source := resolveTargetChain(tc.prs, tc.prTarget, tc.baseBranch, tc.projectDefault)
			if branch != tc.wantBranch || source != tc.wantSource {
				t.Fatalf("got (%q, %q), want (%q, %q)", branch, source, tc.wantBranch, tc.wantSource)
			}
		})
	}
}
