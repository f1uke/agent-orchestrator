package cli

import (
	"regexp"
	"testing"
)

// An orchestrator acts on the human's decision about a proposal from the
// learn.md page it is pointed at, not from --help: a decision command missing
// from that page is one it will not know it can run (the reason the Memory
// inbox's CLI once looked like it could only list).
func TestLearnSkillPage_DocumentsEverySubcommand(t *testing.T) {
	doc := installedSkillPage(t, "learn.md")
	var learn = NewRootCommand(Deps{})
	for _, cmd := range learn.Commands() {
		if cmd.Name() != "learn" {
			continue
		}
		for _, sub := range cmd.Commands() {
			if sub.Hidden || sub.Name() == "help" {
				continue
			}
			if !regexp.MustCompile(`\bao learn ` + regexp.QuoteMeta(sub.Name()) + `\b`).MatchString(doc) {
				t.Errorf("`ao learn %s` is not in the skill page an agent reads", sub.Name())
			}
		}
		return
	}
	t.Fatal("no `ao learn` command")
}
