package cli

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
)

// setConfigFlagCase is one field flag, the command line that passes it, and the
// complete request body it is allowed to produce.
type setConfigFlagCase struct {
	args []string
	// want is the ENTIRE config the request may carry. Comparing the whole
	// struct is the point: a flag that fills in a second field is exactly the
	// bug, so "carries my value" is not a strong enough assertion.
	want domain.ProjectConfig
}

// setConfigFlagCases covers every entry in setConfigFieldFlags. The test below
// fails if the two lists ever disagree, so a new field flag cannot be added
// without a case here proving it writes only itself.
var setConfigFlagCases = map[string]setConfigFlagCase{
	"default-branch": {
		args: []string{"--default-branch", "develop"},
		want: domain.ProjectConfig{DefaultBranch: "develop"},
	},
	"session-prefix": {
		args: []string{"--session-prefix", "acme"},
		want: domain.ProjectConfig{SessionPrefix: "acme"},
	},
	"model": {
		args: []string{"--model", "claude-opus-4-5"},
		want: domain.ProjectConfig{AgentConfig: domain.AgentConfig{Model: "claude-opus-4-5"}},
	},
	"permission": {
		args: []string{"--permission", "accept-edits"},
		want: domain.ProjectConfig{AgentConfig: domain.AgentConfig{Permissions: domain.PermissionModeAcceptEdits}},
	},
	"worker-agent": {
		args: []string{"--worker-agent", "codex"},
		want: domain.ProjectConfig{Worker: domain.RoleOverride{Harness: domain.HarnessCodex}},
	},
	"orchestrator-agent": {
		args: []string{"--orchestrator-agent", "codex"},
		want: domain.ProjectConfig{Orchestrator: domain.RoleOverride{Harness: domain.HarnessCodex}},
	},
	"env": {
		args: []string{"--env", "FOO=bar"},
		want: domain.ProjectConfig{Env: map[string]string{"FOO": "bar"}},
	},
	"symlink": {
		args: []string{"--symlink", ".env.local"},
		want: domain.ProjectConfig{Symlinks: []string{".env.local"}},
	},
	"post-create": {
		args: []string{"--post-create", "make deps"},
		want: domain.ProjectConfig{PostCreate: []string{"make deps"}},
	},
	"tracker-intake": {
		args: []string{"--tracker-intake"},
		want: domain.ProjectConfig{TrackerIntake: domain.TrackerIntakeConfig{Enabled: true}},
	},
	"tracker-provider": {
		args: []string{"--tracker-provider", "gitlab"},
		want: domain.ProjectConfig{TrackerIntake: domain.TrackerIntakeConfig{Provider: domain.TrackerProviderGitLab}},
	},
	"tracker-repo": {
		args: []string{"--tracker-repo", "acme/demo"},
		want: domain.ProjectConfig{TrackerIntake: domain.TrackerIntakeConfig{Repo: "acme/demo"}},
	},
	"tracker-assignee": {
		args: []string{"--tracker-assignee", "alice"},
		want: domain.ProjectConfig{TrackerIntake: domain.TrackerIntakeConfig{Assignee: "alice"}},
	},
	"git-workflow": {
		args: []string{"--git-workflow", "gitflow"},
		want: domain.ProjectConfig{GitConvention: domain.GitConventionConfig{Workflow: domain.GitWorkflowGitflow}},
	},
	"branch-prefix": {
		args: []string{"--branch-prefix", "feature/"},
		want: domain.ProjectConfig{GitConvention: domain.GitConventionConfig{BranchPrefix: "feature/"}},
	},
	"web-ui": {
		args: []string{"--web-ui"},
		want: domain.ProjectConfig{HasWebUI: true},
	},
	"ios-simulator": {
		args: []string{"--ios-simulator"},
		want: domain.ProjectConfig{HasIOSSimulator: true},
	},
	"no-auto-crew": {
		args: []string{"--no-auto-crew"},
		want: domain.ProjectConfig{DisableAutoCrew: true},
	},
	"pause-before-implementing": {
		args: []string{"--pause-before-implementing"},
		want: domain.ProjectConfig{PauseBeforeImplementing: true},
	},
}

// captureSetConfig runs `ao project set-config demo <args...>` against a stub
// daemon and returns the request body it sent.
func captureSetConfig(t *testing.T, args ...string) projectsvc.SetConfigInput {
	t.Helper()
	cfg := setConfigEnv(t)
	srv, capture := projectServer(t, http.StatusOK, `{"project":{"id":"demo","path":"/repo/demo"}}`)
	writeRunFileFor(t, cfg, srv)

	argv := append([]string{"project", "set-config", "demo"}, args...)
	if _, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, argv...); err != nil {
		t.Fatalf("set-config %v: %v\nstderr=%s", args, err, errOut)
	}
	var got projectsvc.SetConfigInput
	if err := json.Unmarshal(capture.body, &got); err != nil {
		t.Fatalf("decode request: %v\nbody=%s", err, capture.body)
	}
	return got
}

// Every field flag must write ONE field and name it, so the daemon can leave the
// rest of the stored config alone. This is the regression test for the incident:
// `set-config <id> --pause-before-implementing` destroyed five settings and
// emptied a sixth because the request could not say which field was meant.
func TestProjectSetConfig_EveryFieldFlagWritesOnlyItsOwnField(t *testing.T) {
	paths := map[string]string{}
	for _, f := range setConfigFieldFlags {
		paths[f.flag] = f.path
		if _, ok := setConfigFlagCases[f.flag]; !ok {
			t.Errorf("field flag --%s has no case in setConfigFlagCases", f.flag)
		}
	}
	for flag := range setConfigFlagCases {
		if _, ok := paths[flag]; !ok {
			t.Errorf("setConfigFlagCases has --%s, which is not a registered field flag", flag)
		}
	}

	for flag, tc := range setConfigFlagCases {
		t.Run(flag, func(t *testing.T) {
			got := captureSetConfig(t, tc.args...)
			if want := []string{paths[flag]}; !reflect.DeepEqual(got.MergeFields, want) {
				t.Errorf("mergeFields = %v, want exactly %v", got.MergeFields, want)
			}
			if !reflect.DeepEqual(got.Config, tc.want) {
				t.Errorf("config = %#v, want exactly %#v", got.Config, tc.want)
			}
		})
	}
}

// The flag table has to stay complete on its own, not just against the cases
// above: a field flag registered on the command but missing from
// setConfigFieldFlags would silently go back to replacing the whole config.
func TestSetConfigFieldFlagsCoverEveryFieldFlag(t *testing.T) {
	// Flags that choose a WRITE MODE rather than a config field. They must not
	// appear in the merge table.
	meta := map[string]bool{"clear": true, "config-json": true, "json": true, "help": true}

	known := map[string]bool{}
	for _, f := range setConfigFieldFlags {
		known[f.flag] = true
	}
	newProjectSetConfigCommand(nil).Flags().VisitAll(func(f *pflag.Flag) {
		if meta[f.Name] {
			if known[f.Name] {
				t.Errorf("--%s selects a write mode and must not be in setConfigFieldFlags", f.Name)
			}
			return
		}
		if !known[f.Name] {
			t.Errorf("--%s writes a config field but is missing from setConfigFieldFlags, "+
				"so passing it would replace the whole stored config", f.Name)
		}
	})
}

// Merging must not make a setting one-way. A bool passed as false is a write,
// because what counts is that the caller named the flag - not what it named it
// with.
func TestProjectSetConfig_BoolFlagCanBeTurnedOff(t *testing.T) {
	for _, flag := range []string{"web-ui", "ios-simulator", "no-auto-crew", "tracker-intake", "pause-before-implementing"} {
		t.Run(flag, func(t *testing.T) {
			got := captureSetConfig(t, "--"+flag+"=false")
			if len(got.MergeFields) != 1 {
				t.Fatalf("mergeFields = %v, want the one field the caller named", got.MergeFields)
			}
			if !reflect.DeepEqual(got.Config, domain.ProjectConfig{}) {
				t.Errorf("config = %#v, want every field at its zero value", got.Config)
			}
		})
	}
}

// The same holds for a string cleared to empty: `--default-branch ""` is a
// request, and only the mask can carry it.
func TestProjectSetConfig_StringFlagCanBeCleared(t *testing.T) {
	got := captureSetConfig(t, "--default-branch", "")
	if want := []string{"defaultBranch"}; !reflect.DeepEqual(got.MergeFields, want) {
		t.Fatalf("mergeFields = %v, want %v", got.MergeFields, want)
	}
	if got.Config.DefaultBranch != "" {
		t.Errorf("defaultBranch = %q, want empty", got.Config.DefaultBranch)
	}
}

// Several flags at once name several fields, and still nothing else.
func TestProjectSetConfig_SeveralFlagsNameSeveralFields(t *testing.T) {
	got := captureSetConfig(t, "--git-workflow", "custom", "--branch-prefix", "feat/", "--web-ui")
	want := []string{"gitConvention.workflow", "gitConvention.branchPrefix", "hasWebUI"}
	if !reflect.DeepEqual(got.MergeFields, want) {
		t.Fatalf("mergeFields = %v, want %v", got.MergeFields, want)
	}
	if got.Config.GitConvention.Workflow != domain.GitWorkflowCustom ||
		got.Config.GitConvention.BranchPrefix != "feat/" || !got.Config.HasWebUI {
		t.Errorf("config = %#v", got.Config)
	}
}

// --config-json keeps replacing. It is documented as replacing, it is the way to
// edit a setting no flag covers, and read-modify-write callers depend on it - so
// it must send NO mask, whatever else is on the command line.
func TestProjectSetConfig_ConfigJSONStillReplaces(t *testing.T) {
	got := captureSetConfig(t, "--config-json", `{"defaultBranch":"develop","responseLanguage":"Thai"}`)
	if got.MergeFields != nil {
		t.Fatalf("mergeFields = %v, want none: --config-json replaces the whole config", got.MergeFields)
	}
	want := domain.ProjectConfig{DefaultBranch: "develop", ResponseLanguage: "Thai"}
	if !reflect.DeepEqual(got.Config, want) {
		t.Errorf("config = %#v, want %#v", got.Config, want)
	}
}

// --config-json wins over a field flag passed alongside it, as documented, and
// it must not smuggle that flag in as a mask entry either - a mask plus a whole
// config would write the flag's field twice and merge the rest.
func TestProjectSetConfig_ConfigJSONOverridesFieldFlags(t *testing.T) {
	got := captureSetConfig(t, "--web-ui", "--config-json", `{"defaultBranch":"develop"}`)
	if got.MergeFields != nil {
		t.Fatalf("mergeFields = %v, want none", got.MergeFields)
	}
	if got.Config.HasWebUI {
		t.Errorf("config = %#v, want --config-json to win over --web-ui", got.Config)
	}
}

// --clear keeps clearing: an empty config, replacing.
func TestProjectSetConfig_ClearStillClears(t *testing.T) {
	got := captureSetConfig(t, "--clear")
	if got.MergeFields != nil {
		t.Fatalf("mergeFields = %v, want none: --clear replaces the config with an empty one", got.MergeFields)
	}
	if !got.Config.IsZero() {
		t.Errorf("config = %#v, want empty", got.Config)
	}
}

// A command line that names no field at all is still a usage error, and must not
// reach the daemon - an empty mask there means "replace", which is precisely
// what a caller who named nothing did not ask for.
func TestProjectSetConfig_NoFieldFlagsIsUsageError(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := projectServer(t, http.StatusOK, `{"project":{"id":"demo","path":"/repo/demo"}}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "project", "set-config", "demo")
	if err == nil {
		t.Fatalf("set-config with no flags must fail; stdout=%s", out)
	}
	if ExitCode(err) != 2 {
		t.Errorf("exit code = %d, want 2 (usage error)", ExitCode(err))
	}
	if capture.configPuts != 0 {
		t.Errorf("nothing to write must not write; got %d PUTs to the config endpoint", capture.configPuts)
	}
	if msg := err.Error() + errOut; !strings.Contains(msg, "at least one config flag") {
		t.Errorf("error should say what to pass, got: %s", msg)
	}
}
