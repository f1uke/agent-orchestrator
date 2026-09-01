package domain

import (
	"reflect"
	"strings"
	"testing"
)

// fullProjectConfig is a config with EVERY field set to a distinctive non-zero
// value, including the four that no CLI flag can reach (reviewers,
// responseLanguage, simProfile, approvalRule) and the two that only the API
// writes (systemPromptAdditions, the role agent configs). A merge test is only
// worth anything against a config where every key has something to lose.
func fullProjectConfig() ProjectConfig {
	return ProjectConfig{
		DefaultBranch: "develop",
		SessionPrefix: "acme",
		Env:           map[string]string{"FOO": "bar"},
		Symlinks:      []string{".env.local"},
		PostCreate:    []string{"make deps"},
		AgentConfig:   AgentConfig{Model: "claude-opus-4-5", Permissions: PermissionModeAcceptEdits},
		Worker:        RoleOverride{Harness: HarnessClaudeCode, AgentConfig: AgentConfig{Model: "claude-sonnet-4-5"}},
		Orchestrator:  RoleOverride{Harness: HarnessClaudeCode, AgentConfig: AgentConfig{Model: "claude-opus-4-5"}},
		Reviewers:     []ReviewerConfig{{Harness: ReviewerClaudeCode}},
		TrackerIntake: TrackerIntakeConfig{
			Enabled:  true,
			Provider: TrackerProviderGitLab,
			Repo:     "group/sub/proj",
			Assignee: "alice",
		},
		GitConvention:           GitConventionConfig{Workflow: GitWorkflowGitflow, BranchPrefix: "feature/"},
		SystemPromptAdditions:   SystemPromptAdditions{Orchestrator: "orch", Worker: "work", Reviewer: "rev"},
		ResponseLanguage:        "Thai",
		HasWebUI:                true,
		HasIOSSimulator:         true,
		SimProfile:              &SimProfileConfig{Keep: []string{"com.apple.backboardd"}},
		DisableAutoCrew:         true,
		PauseBeforeImplementing: true,
		ApprovalRule:            ApprovalRule{Enabled: true, Threshold: 3},
	}
}

// Every settable path must write ONLY what it names. This is the data-loss
// regression in its most direct form: one field in, everything else untouched.
func TestMergeConfigFields_WritesOnlyTheNamedField(t *testing.T) {
	incoming := ProjectConfig{
		DefaultBranch: "main",
		SessionPrefix: "zzz",
		Env:           map[string]string{"NEW": "value"},
		Symlinks:      []string{"other.local"},
		PostCreate:    []string{"echo hi"},
		AgentConfig:   AgentConfig{Model: "claude-haiku-4-5", Permissions: PermissionModeDefault},
		Worker:        RoleOverride{Harness: HarnessCodex},
		Orchestrator:  RoleOverride{Harness: HarnessCodex},
		TrackerIntake: TrackerIntakeConfig{
			Enabled:  false,
			Provider: TrackerProviderGitHub,
			Repo:     "acme/demo",
			Assignee: "bob",
		},
		GitConvention:           GitConventionConfig{Workflow: GitWorkflowCustom, BranchPrefix: "chore/"},
		HasWebUI:                false,
		HasIOSSimulator:         false,
		DisableAutoCrew:         false,
		PauseBeforeImplementing: false,
	}

	// path -> the field's value read back off a config, so each case can assert
	// "this one changed" without a switch per field.
	paths := []struct {
		path string
		read func(ProjectConfig) any
	}{
		{"defaultBranch", func(c ProjectConfig) any { return c.DefaultBranch }},
		{"sessionPrefix", func(c ProjectConfig) any { return c.SessionPrefix }},
		{"env", func(c ProjectConfig) any { return c.Env }},
		{"symlinks", func(c ProjectConfig) any { return c.Symlinks }},
		{"postCreate", func(c ProjectConfig) any { return c.PostCreate }},
		{"agentConfig.model", func(c ProjectConfig) any { return c.AgentConfig.Model }},
		{"agentConfig.permissions", func(c ProjectConfig) any { return c.AgentConfig.Permissions }},
		{"worker.agent", func(c ProjectConfig) any { return c.Worker.Harness }},
		{"orchestrator.agent", func(c ProjectConfig) any { return c.Orchestrator.Harness }},
		{"trackerIntake.enabled", func(c ProjectConfig) any { return c.TrackerIntake.Enabled }},
		{"trackerIntake.provider", func(c ProjectConfig) any { return c.TrackerIntake.Provider }},
		{"trackerIntake.repo", func(c ProjectConfig) any { return c.TrackerIntake.Repo }},
		{"trackerIntake.assignee", func(c ProjectConfig) any { return c.TrackerIntake.Assignee }},
		{"gitConvention.workflow", func(c ProjectConfig) any { return c.GitConvention.Workflow }},
		{"gitConvention.branchPrefix", func(c ProjectConfig) any { return c.GitConvention.BranchPrefix }},
		{"hasWebUI", func(c ProjectConfig) any { return c.HasWebUI }},
		{"hasIOSSimulator", func(c ProjectConfig) any { return c.HasIOSSimulator }},
		{"disableAutoCrew", func(c ProjectConfig) any { return c.DisableAutoCrew }},
		{"pauseBeforeImplementing", func(c ProjectConfig) any { return c.PauseBeforeImplementing }},
		{"responseLanguage", func(c ProjectConfig) any { return c.ResponseLanguage }},
		{"reviewers", func(c ProjectConfig) any { return c.Reviewers }},
		{"simProfile", func(c ProjectConfig) any { return c.SimProfile }},
		{"approvalRule", func(c ProjectConfig) any { return c.ApprovalRule }},
		{"systemPromptAdditions", func(c ProjectConfig) any { return c.SystemPromptAdditions }},
	}

	// The table is only a "table over EVERY field" while it stays exhaustive, so
	// a field added to ProjectConfig without a case here fails loudly rather
	// than quietly going untested.
	covered := map[string]bool{}
	for _, tc := range paths {
		top, _, _ := strings.Cut(tc.path, ".")
		covered[top] = true
	}
	cfgType := reflect.TypeOf(ProjectConfig{})
	for i := range cfgType.NumField() {
		name, _, _ := strings.Cut(cfgType.Field(i).Tag.Get("json"), ",")
		if !covered[name] {
			t.Errorf("ProjectConfig field %q (%s) has no merge case; add it to paths", name, cfgType.Field(i).Name)
		}
	}

	for _, tc := range paths {
		t.Run(tc.path, func(t *testing.T) {
			stored := fullProjectConfig()
			merged, err := MergeConfigFields(stored, incoming, []string{tc.path})
			if err != nil {
				t.Fatalf("merge %q: %v", tc.path, err)
			}
			if got, want := tc.read(merged), tc.read(incoming); !reflect.DeepEqual(got, want) {
				t.Errorf("%s = %#v after merge, want the incoming %#v", tc.path, got, want)
			}
			// Every other key must be byte-for-byte what it was. The path list
			// is exhaustive over ProjectConfig, so this covers keys no flag can
			// even name.
			for _, other := range paths {
				if other.path == tc.path {
					continue
				}
				if got, keep := other.read(merged), other.read(stored); !reflect.DeepEqual(got, keep) {
					t.Errorf("merging %q changed %q: %#v, want %#v", tc.path, other.path, got, keep)
				}
			}
		})
	}
}

// The four settings no flag can reach are the sharpest case: a field-flag write
// could only ever destroy them, because there is no spelling that would keep
// them. They must survive a merge that never mentions them.
func TestMergeConfigFields_KeepsFlaglessFields(t *testing.T) {
	stored := fullProjectConfig()
	merged, err := MergeConfigFields(stored, ProjectConfig{PauseBeforeImplementing: true}, []string{"pauseBeforeImplementing"})
	if err != nil {
		t.Fatal(err)
	}
	if merged.ResponseLanguage != "Thai" {
		t.Errorf("responseLanguage = %q, want Thai", merged.ResponseLanguage)
	}
	if !reflect.DeepEqual(merged.Reviewers, stored.Reviewers) {
		t.Errorf("reviewers = %#v, want %#v", merged.Reviewers, stored.Reviewers)
	}
	if !reflect.DeepEqual(merged.SimProfile, stored.SimProfile) {
		t.Errorf("simProfile = %#v, want %#v", merged.SimProfile, stored.SimProfile)
	}
	if merged.ApprovalRule != stored.ApprovalRule {
		t.Errorf("approvalRule = %#v, want %#v", merged.ApprovalRule, stored.ApprovalRule)
	}
}

// A named field is written even when its value is the zero one - that is the
// whole point of naming it. Without this a bool could be turned on and never
// off, and a string set and never cleared.
func TestMergeConfigFields_WritesZeroValues(t *testing.T) {
	stored := fullProjectConfig()
	merged, err := MergeConfigFields(stored, ProjectConfig{}, []string{
		"hasWebUI", "defaultBranch", "env", "gitConvention.workflow",
	})
	if err != nil {
		t.Fatal(err)
	}
	if merged.HasWebUI {
		t.Error("hasWebUI = true, want false: naming a bool must be able to turn it off")
	}
	if merged.DefaultBranch != "" {
		t.Errorf("defaultBranch = %q, want cleared", merged.DefaultBranch)
	}
	if merged.Env != nil {
		t.Errorf("env = %#v, want cleared", merged.Env)
	}
	if merged.GitConvention.Workflow != "" {
		t.Errorf("gitConvention.workflow = %q, want cleared", merged.GitConvention.Workflow)
	}
	// The sibling under the same parent is untouched: the path names a leaf,
	// not the subtree it hangs off.
	if merged.GitConvention.BranchPrefix != "feature/" {
		t.Errorf("gitConvention.branchPrefix = %q, want feature/", merged.GitConvention.BranchPrefix)
	}
	if !merged.HasIOSSimulator {
		t.Error("hasIOSSimulator was cleared by a merge that never named it")
	}
}

// A map or slice leaf is stated whole, not appended to: `--env A=B` says what
// the env IS, so removing an entry stays possible.
func TestMergeConfigFields_ReplacesMapsAndSlicesWhole(t *testing.T) {
	stored := fullProjectConfig()
	incoming := ProjectConfig{
		Env:      map[string]string{"NEW": "value"},
		Symlinks: []string{"only.local"},
	}
	merged, err := MergeConfigFields(stored, incoming, []string{"env", "symlinks"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(merged.Env, map[string]string{"NEW": "value"}) {
		t.Errorf("env = %#v, want exactly the incoming map (not merged with FOO=bar)", merged.Env)
	}
	if !reflect.DeepEqual(merged.Symlinks, []string{"only.local"}) {
		t.Errorf("symlinks = %#v, want exactly the incoming list", merged.Symlinks)
	}
	// The stored config must not have been mutated through its shared map header.
	if stored.Env["FOO"] != "bar" {
		t.Errorf("stored env was mutated: %#v", stored.Env)
	}
}

// An empty mask changes nothing. Service.SetConfig reads it as "replace", so
// this only guards the helper itself against a stray identity write.
func TestMergeConfigFields_NoFieldsIsIdentity(t *testing.T) {
	stored := fullProjectConfig()
	merged, err := MergeConfigFields(stored, ProjectConfig{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(merged, stored) {
		t.Errorf("merge with no fields = %#v, want the stored config unchanged", merged)
	}
}

// A path that names nothing is refused. Skipping it silently is the failure
// mode this whole change exists to remove: a write that reports success while
// doing something other than what was asked.
func TestMergeConfigFields_RejectsUnknownPaths(t *testing.T) {
	for name, path := range map[string]string{
		"unknown leaf":        "responseLanguag",
		"unknown nested leaf": "gitConvention.branchPrefixx",
		"go field name":       "DefaultBranch",
		"through a scalar":    "defaultBranch.nope",
		"through a pointer":   "simProfile.keep",
		"empty":               "",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := MergeConfigFields(fullProjectConfig(), ProjectConfig{}, []string{path}); err == nil {
				t.Fatalf("merge %q succeeded, want an error", path)
			}
		})
	}
}
