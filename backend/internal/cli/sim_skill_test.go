package cli

import (
	"os"
	"path/filepath"
	"regexp"
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
