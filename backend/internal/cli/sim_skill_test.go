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

// The `ao sim` catalog an agent reads is a set of files shipped in the daemon,
// not the commands' own --help: a worker is pointed at sim.md, and decides from
// its page table what it can do and which page to open next. So a command that
// exists but is not routed from that page may as well not exist - which is
// exactly what happened to `ao sim drag` until this test existed.
func TestSimSkillPage_RoutesEverySubcommand(t *testing.T) {
	doc := installedSkillPage(t, "sim.md")
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
			t.Fatalf("`ao sim %s` is not routed from sim.md, the page an agent reads first", sub.Name())
		}
	}
}

// On a script-only project (ProjectConfig.MobileScripts) the worker prompt makes
// every check a script run and leaves driving by hand to debugging and
// authoring, and sim.md is the next thing that agent reads - so a page that
// still opened with "drive it with taps" and never said otherwise would teach
// the rule's opposite one click away from the prompt. The page has to say what
// the setting changes and who may drive by hand, and the Maestro page has to
// show the form of `flow run` that runs several flows in one Maestro start-up.
func TestSimSkillPage_TeachesTheScriptOnlyRule(t *testing.T) {
	doc := installedSkillPage(t, "sim.md")
	for _, want := range []string{
		"On a script-only project, every check is a script run and you read what it left.",
		"qa uses them only to author a script nobody has written yet",
		"A screen reached by hand is never evidence.",
		"--mobile-scripts <product> --mobile-platform ios|android",
		"never finished by hand",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("sim.md does not carry %q", want)
		}
	}
	if maestro := installedSkillPage(t, "sim-maestro.md"); !strings.Contains(maestro, "ao sim flow run a.yaml b.yaml") {
		t.Error("sim-maestro.md does not show several flows in one `ao sim flow run`")
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
