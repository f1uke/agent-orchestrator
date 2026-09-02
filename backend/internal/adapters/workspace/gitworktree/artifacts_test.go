package gitworktree

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/reclaimsettings"
)

// z builds `git status --porcelain -z` output from records.
func z(records ...string) string {
	if len(records) == 0 {
		return ""
	}
	return strings.Join(records, "\x00") + "\x00"
}

func TestClassifyStatus_UntrackedBuildOutputIsRegenerable(t *testing.T) {
	patterns := reclaimsettings.DefaultArtifactPatterns
	// The three shapes measured on real native-app worktrees.
	out := z("?? derivedDataPath/", "?? TestResults.xcresult/", "?? fastlane/xcov_report/")

	blocking, artifacts := classifyStatus(out, patterns)

	if len(blocking) != 0 {
		t.Fatalf("build output must not block teardown, got blocking=%v", blocking)
	}
	if len(artifacts) != 3 {
		t.Fatalf("want 3 artefact paths, got %v", artifacts)
	}
}

func TestClassifyStatus_ModifiedTrackedFileAlwaysBlocks(t *testing.T) {
	patterns := reclaimsettings.DefaultArtifactPatterns
	// A modified tracked file is human work even when it sits INSIDE a
	// directory whose name is on the artefact list.
	out := z(" M frontend/pnpm-lock.yaml", " M Pods/Manifest.lock", "?? derivedDataPath/")

	blocking, artifacts := classifyStatus(out, patterns)

	if len(blocking) != 2 {
		t.Fatalf("tracked modifications must block, got blocking=%v", blocking)
	}
	if len(artifacts) != 1 || artifacts[0] != "derivedDataPath/" {
		t.Fatalf("want only the untracked artefact, got %v", artifacts)
	}
}

func TestClassifyStatus_UnrecognisedUntrackedFileBlocks(t *testing.T) {
	// The safety default: anything not positively identified as build output
	// keeps the worktree.
	blocking, artifacts := classifyStatus(z("?? notes.md"), reclaimsettings.DefaultArtifactPatterns)

	if len(blocking) != 1 || blocking[0].rel != "notes.md" {
		t.Fatalf("unrecognised untracked file must block, got %v", blocking)
	}
	if len(artifacts) != 0 {
		t.Fatalf("want no artefacts, got %v", artifacts)
	}
}

// TestClassifyStatus_CollapsedUntrackedParentBlocks pins a real git behaviour
// that the safety default handles correctly by accident of design, so it stays
// deliberate: git collapses a WHOLLY untracked directory to its topmost entry,
// reporting `?? fastlane/` rather than `?? fastlane/xcov_report/`. The parent is
// not a recognised artefact, so the worktree is kept — the right call, because
// that directory may hold anything at all, not just the report.
func TestClassifyStatus_CollapsedUntrackedParentBlocks(t *testing.T) {
	blocking, artifacts := classifyStatus(z("?? fastlane/"), reclaimsettings.DefaultArtifactPatterns)

	if len(blocking) != 1 || blocking[0].rel != "fastlane/" {
		t.Fatalf("an unrecognised collapsed parent must block, got blocking=%v", blocking)
	}
	if len(artifacts) != 0 {
		t.Fatalf("want no artefacts, got %v", artifacts)
	}
}

func TestClassifyStatus_NoPatternsMeansNothingIsRegenerable(t *testing.T) {
	// Artefact clearing switched off: build output goes back to blocking, which
	// is the pre-existing behaviour.
	blocking, artifacts := classifyStatus(z("?? derivedDataPath/"), nil)

	if len(blocking) != 1 {
		t.Fatalf("with no patterns everything must block, got %v", blocking)
	}
	if len(artifacts) != 0 {
		t.Fatalf("want no artefacts, got %v", artifacts)
	}
}

func TestClassifyStatus_StagedAndDeletedEntriesBlock(t *testing.T) {
	out := z("A  newfile.go", "D  removed.go", "MM both.go", "UU conflicted.go")

	blocking, artifacts := classifyStatus(out, reclaimsettings.DefaultArtifactPatterns)

	if len(blocking) != 4 {
		t.Fatalf("want all 4 index states blocking, got %v", blocking)
	}
	if len(artifacts) != 0 {
		t.Fatalf("want no artefacts, got %v", artifacts)
	}
}

func TestParseStatusZ_RenameConsumesOriginField(t *testing.T) {
	// A rename record is "R  <new>\0<old>\0". The old path must not surface as
	// a separate entry, or it would be double-counted.
	out := z("R  new/name.go", "old/name.go", "?? derivedDataPath/")

	entries := parseStatusZ(out)

	if len(entries) != 2 {
		t.Fatalf("want 2 entries (rename + untracked), got %d: %+v", len(entries), entries)
	}
	if entries[0].rel != "new/name.go" || entries[1].rel != "derivedDataPath/" {
		t.Fatalf("unexpected entries: %+v", entries)
	}
}

func TestParseStatusZ_PathsWithSpacesSurviveIntact(t *testing.T) {
	// The reason for -z: plain --porcelain would C-quote this path.
	entries := parseStatusZ(z("?? My Project/DerivedData/"))

	if len(entries) != 1 || entries[0].rel != "My Project/DerivedData/" {
		t.Fatalf("path with a space was mangled: %+v", entries)
	}
}

func TestIsRegenerable(t *testing.T) {
	patterns := reclaimsettings.DefaultArtifactPatterns
	cases := []struct {
		rel  string
		want bool
		why  string
	}{
		{"derivedDataPath/", true, "exact segment match, trailing slash trimmed"},
		{"fastlane/xcov_report/", true, "nested segment match"},
		{"TestResults.xcresult/", true, "glob pattern *.xcresult"},
		{"node_modules/", true, "web build output"},
		{"PodsHelper/", false, "segment equality is exact, not a prefix"},
		{"MyPods/", false, "must not match a substring"},
		{"src/main.go", false, "ordinary source file"},
		{"build/", false, "bare build is deliberately NOT an artefact pattern"},
		{"dist/", false, "bare dist is deliberately NOT an artefact pattern"},
		{"", false, "empty path"},
	}
	for _, tc := range cases {
		if got := isRegenerable(tc.rel, patterns); got != tc.want {
			t.Errorf("isRegenerable(%q) = %v, want %v (%s)", tc.rel, got, tc.want, tc.why)
		}
	}
}

func TestIsRegenerable_EmptyPatternIsIgnored(t *testing.T) {
	// An empty string in a user-supplied pattern list must not match everything.
	if isRegenerable("src/main.go", []string{""}) {
		t.Fatal("an empty pattern must never match")
	}
}

// statusWord turns git's XY code into the word a person reads next to a path.
// The table is here rather than inferred from a live repo because half these
// codes need a staged index, a conflict or a rename to reproduce, and the words
// are what someone decides on.
func TestStatusWord_NamesWhatHappenedToTheFile(t *testing.T) {
	cases := map[string]string{
		"??": ports.UncommittedUntracked,
		" M": ports.UncommittedModified,
		"M ": ports.UncommittedModified,
		"MM": ports.UncommittedModified,
		"A ": ports.UncommittedAdded,
		"AM": ports.UncommittedAdded,
		" D": ports.UncommittedDeleted,
		"D ": ports.UncommittedDeleted,
		"R ": ports.UncommittedRenamed,
		"C ": ports.UncommittedRenamed,
		"UU": ports.UncommittedConflicted,
		"AA": ports.UncommittedConflicted,
		"DU": ports.UncommittedConflicted,
		" T": ports.UncommittedModified,
		"!!": ports.UncommittedChanged,
		"":   ports.UncommittedChanged,
	}
	for code, want := range cases {
		if got := statusWord(code); got != want {
			t.Errorf("statusWord(%q) = %q, want %q", code, got, want)
		}
	}
}
