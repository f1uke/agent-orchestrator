package session

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// changesTestRepo's keep.go carries BOTH a committed change (l2 -> CHANGED,
// plus l4) and an uncommitted one (+uncommitted). That is what makes it the
// fixture for the two change levels: the same file, two different answers.

func fileDiffService(t *testing.T, target string) (*Service, string) {
	t.Helper()
	dir := changesTestRepo(t)
	fake := newFakeStore()
	fake.putSessionWithWorkspace("s1", dir)
	rec := fake.sessions["s1"]
	rec.PRTarget = target
	fake.sessions["s1"] = rec
	return newServiceWithStore(t, &multiPRFakeStore{fakeStore: fake}), dir
}

func addedTexts(res DiffContextResult) []string {
	var out []string
	for _, l := range res.Lines {
		if l.Kind == "add" {
			out = append(out, l.Text)
		}
	}
	return out
}

// The branch level: merge-base(target, HEAD) .. working tree. Committed and
// uncommitted work land in ONE list, which is what "everything this branch did"
// means.
func TestWorkspaceFileDiff_TargetBaseSpansCommittedAndUncommitted(t *testing.T) {
	svc, _ := fileDiffService(t, "main")

	res, err := svc.WorkspaceFileDiff(context.Background(), "s1", "keep.go", DiffBaseTarget)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available {
		t.Fatalf("want a diff, got %+v", res)
	}
	got := addedTexts(res)
	want := map[string]bool{"CHANGED": false, "l4": false, "uncommitted": false}
	for _, text := range got {
		if _, ok := want[text]; ok {
			want[text] = true
		}
	}
	for text, seen := range want {
		if !seen {
			t.Fatalf("branch level must include %q; adds were %v", text, got)
		}
	}
}

// The uncommitted level: HEAD .. working tree. This is what Discard Change can
// undo, and it must NOT carry the committed work the branch level carries.
func TestWorkspaceFileDiff_HeadBaseIsUncommittedOnly(t *testing.T) {
	svc, _ := fileDiffService(t, "main")

	res, err := svc.WorkspaceFileDiff(context.Background(), "s1", "keep.go", DiffBaseHead)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available {
		t.Fatalf("want a diff, got %+v", res)
	}
	got := addedTexts(res)
	if len(got) != 1 || got[0] != "uncommitted" {
		t.Fatalf("HEAD base must show only the uncommitted add, got %v", got)
	}
	for _, l := range res.Lines {
		if l.Kind == "del" {
			t.Fatalf("nothing was deleted since HEAD, but got %+v", l)
		}
	}
}

// 🗝 The HEAD base must not depend on a target branch at all. Discard Change is
// about this worktree and HEAD; making it resolve (and background-fetch) a
// target would tie an offline, purely local gesture to the network.
func TestWorkspaceFileDiff_HeadBaseNeedsNoTargetBranch(t *testing.T) {
	svc, _ := fileDiffService(t, "no-such-branch-anywhere")

	head, err := svc.WorkspaceFileDiff(context.Background(), "s1", "keep.go", DiffBaseHead)
	if err != nil {
		t.Fatal(err)
	}
	if !head.Available {
		t.Fatalf("HEAD base must answer with no resolvable target, got %+v", head)
	}

	// Same session, same file, target base: unresolvable, so nothing to show.
	target, err := svc.WorkspaceFileDiff(context.Background(), "s1", "keep.go", DiffBaseTarget)
	if err != nil {
		t.Fatal(err)
	}
	if target.Available {
		t.Fatalf("target base has no branch to resolve; want unavailable, got %+v", target)
	}
}

// An untracked file is wholly added against EITHER base, and the synthesised
// patch has to apply to both - `git diff` reports it under neither.
func TestWorkspaceFileDiff_UntrackedIsAddedUnderBothBases(t *testing.T) {
	svc, _ := fileDiffService(t, "main")

	for _, base := range []DiffBase{DiffBaseTarget, DiffBaseHead} {
		res, err := svc.WorkspaceFileDiff(context.Background(), "s1", "untracked.go", base)
		if err != nil {
			t.Fatalf("%s: %v", base, err)
		}
		if !res.Available {
			t.Fatalf("%s: an untracked file must render as added, got %+v", base, res)
		}
		if got := addedTexts(res); len(got) != 2 || got[0] != "u1" || got[1] != "u2" {
			t.Fatalf("%s: adds = %v, want [u1 u2]", base, got)
		}
	}
}

// An empty base keeps the branch level, so every caller written before the
// selector existed keeps asking the same question.
func TestWorkspaceFileDiff_EmptyBaseDefaultsToTarget(t *testing.T) {
	svc, _ := fileDiffService(t, "main")

	res, err := svc.WorkspaceFileDiff(context.Background(), "s1", "keep.go", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(addedTexts(res)) != 3 {
		t.Fatalf("empty base must mean target, got %v", addedTexts(res))
	}
}

// 🗝 An unknown base is REFUSED, never quietly treated as the default. The two
// bases answer different questions, and silently answering the other one is the
// same class of bug as ConfinedPath clamping `../x` to a file nobody named.
func TestWorkspaceFileDiff_UnknownBaseIsRefused(t *testing.T) {
	svc, _ := fileDiffService(t, "main")

	_, err := svc.WorkspaceFileDiff(context.Background(), "s1", "keep.go", "HEAD~3")
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("want an API error, got %v", err)
	}
	if apiErr.Kind != apierr.KindInvalid || apiErr.Code != "WORKSPACE_FILE_BASE_INVALID" {
		t.Fatalf("err = %+v, want 400/WORKSPACE_FILE_BASE_INVALID", apiErr)
	}
}
