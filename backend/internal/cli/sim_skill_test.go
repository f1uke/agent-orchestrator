package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/skillassets"
)

// The `ao sim` catalog an agent reads is a file shipped in the daemon, not the
// command's own --help: a worker is pointed at <dataDir>/skills/.../sim.md and
// decides from that what it can do. So a command that exists but is not in that
// page may as well not exist - which is exactly what happened to `ao sim drag`
// until this test existed.
func TestSimSkillPage_DocumentsEverySubcommand(t *testing.T) {
	dir := t.TempDir()
	if err := skillassets.Install(dir); err != nil {
		t.Fatalf("install skill: %v", err)
	}
	page, err := os.ReadFile(filepath.Join(skillassets.Dir(dir, false), "commands", "sim.md"))
	if err != nil {
		t.Fatalf("read sim.md: %v", err)
	}
	doc := string(page)

	var sim *cobra.Command
	for _, cmd := range NewRootCommand(Deps{}).Commands() {
		if cmd.Name() == "sim" {
			sim = cmd
		}
	}
	if sim == nil {
		t.Fatal("no `ao sim` command")
	}
	for _, sub := range sim.Commands() {
		if sub.Hidden || sub.Name() == "help" {
			continue
		}
		// Whole word: "ao sim drag" is a substring of "ao sim dragx", and a
		// catalog that documents a command that does not exist is the same
		// failure as one that omits a command that does.
		mentioned := regexp.MustCompile(`\bao sim ` + regexp.QuoteMeta(sub.Name()) + `\b`)
		if !mentioned.MatchString(doc) {
			t.Fatalf("`ao sim %s` is not in the skill page an agent reads", sub.Name())
		}
	}
}

// On a script-only project (ProjectConfig.MobileScripts) the worker prompt rules
// out step-by-step driving, and the skill page is the next thing that agent
// reads - so a page that still opened with "drive it with taps" and never said
// otherwise would teach the rule's opposite one click away from the prompt. The
// page has to say what the setting changes, and has to show the form of `flow
// run` that runs several flows in one Maestro start-up.
func TestSimSkillPage_TeachesTheScriptOnlyRule(t *testing.T) {
	doc := installedSkillPage(t, "sim.md")
	for _, want := range []string{
		"On a script-only project, a script moves the app and you read what it left.",
		"--mobile-scripts <product> --mobile-platform ios|android",
		"never finished by hand",
		"ao sim flow run   <file>...",
		"ao sim flow run a.yaml b.yaml",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("sim.md does not carry %q", want)
		}
	}
}

// Every `ao project set-config` field flag has a row in the page an agent reads
// before it changes a project's config. The table had fallen nine flags behind
// the command, which is how a setting nobody can find gets turned on by nobody.
func TestProjectSkillPage_DocumentsEverySetConfigFlag(t *testing.T) {
	doc := installedSkillPage(t, "project.md")
	for _, f := range setConfigFieldFlags {
		if !regexp.MustCompile("\\| `--" + regexp.QuoteMeta(f.flag) + "[ `]").MatchString(doc) {
			t.Errorf("`ao project set-config --%s` has no row in project.md", f.flag)
		}
	}
}

func installedSkillPage(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	if err := skillassets.Install(dir); err != nil {
		t.Fatalf("install skill: %v", err)
	}
	page, err := os.ReadFile(filepath.Join(skillassets.Dir(dir, false), "commands", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(page)
}
