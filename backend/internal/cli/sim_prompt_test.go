package cli

import (
	"fmt"
	"regexp"
	"sort"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/aoagents/agent-orchestrator/backend/internal/prompts"
)

// The worker system prompt carries a short `ao sim` section (prompts.
// SimulatorGuidance) that a worker on an iOS project ALWAYS sees, unlike the
// skill page it has to choose to read. That makes it the layer that decides
// what an agent reaches for by default - and the layer that silently went stale
// while the CLI grew `log`, `record`, `flow` and `ao sim tap --label`.
//
// The prompt is deliberately a SUBSET, so "mentions every subcommand" is the
// wrong assertion: it would force the prompt to grow with the CLI, which is the
// opposite of what makes it work. What holds instead is the decision itself.
// simPromptDecisions below is that reviewed decision, one entry per surface, and
// the test asserts it against the real command tree in both directions. A
// command or flag added later has no entry, so it cannot default to "omitted" -
// somebody has to write down which way it went.
//
// "In the prompt" means the prompt TEACHES the invocation, i.e. it contains
// `ao sim <name>` / `--<flag>`. Naming a capability in prose without its command
// (the closing "typing, buttons, recording ... is in the ao skill" pointer) is
// deliberately not teaching it, and is how an omitted surface stays findable.
var simPromptDecisions = map[string]bool{
	// In the prompt: the read -> act -> re-read loop, plus the two facts about
	// a shared device (claim, release) that an agent gets wrong unprompted.
	"list":    true,
	"claim":   true,
	"release": true,
	"ax":      true,
	"tap":     true,
	"drag":    true,
	"shot":    true,
	// `log` is here because it is the app's own account of what a gesture did,
	// which the screen often cannot give, and because the blocked-main-thread
	// diagnosis sends an agent straight to it.
	"log": true,

	// Skill page only. Not "less useful" - just not what an agent gets WRONG
	// without being told, which is the only thing the always-seen layer buys.
	//
	// `swipe` is the two-point case of `drag`, and the prompt says so where it
	// teaches `drag`; teaching both spends lines on one gesture.
	"swipe": false,
	// `type` and `button` are how to do a thing an agent already knows it wants;
	// their hazards (keyboard remapping, buttons that report success and do
	// nothing) are reported by the commands themselves at the moment of use.
	"type":   false,
	"button": false,
	// `flow` and `record` are multi-step surfaces (start/status/stop,
	// check/run, entry points, Maestro) reached only when a task asks for a
	// replayable flow. That is a task instruction, not a standing hazard, and
	// an agent that reaches for one will read the page that explains it.
	"flow":   false,
	"record": false,

	// Flags, keyed "<command> --<flag>", for prompt-worthy commands only: a
	// flag on an omitted command is covered by the command's own decision.
	// This is the level `ao sim tap --label` drifted at - it changed the
	// shortest correct way to tap something without changing the command list.
	"tap --label": true,
	// --id is the same mechanism with a stabler key; one example teaches the
	// form, and the closing pointer names the other.
	"tap --id":       false,
	"ax --format":    false,
	"ax --max-nodes": false,
	// `--settle` re-reads until the screen stops changing, for a screen whose
	// content arrives late. Omitted because the loop the prompt already
	// teaches - read, act, re-read - is self-correcting for ACTING: an agent
	// that acts on a half-drawn screen sees the result on its next read. The
	// path that is NOT self-correcting is recording, where a selector is
	// written down once from whatever was on screen, and that one settles by
	// itself without the agent asking. What is left is the deliberate case
	// (asserting a screen is final), which is a task instruction rather than a
	// standing hazard - and it costs about a second, which is the wrong thing
	// to teach as a default.
	"ax --settle":     false,
	"claim --ttl":     false,
	"drag --duration": false,
	"shot --output":   false,
	"log --follow":    false,
	"log --grep":      false,
	"log --max-lines": false,
	"log --process":   false,
	"log --since":     false,
}

// ambientSimFlags are the two flags nearly every `ao sim` command carries, so
// deciding them per command would be twenty entries saying the same thing:
// `--json` is output shape, and `--udid` is device selection, which the prompt
// settles once in prose (which device you may drive at all) rather than command
// by command. Excluding them is itself a reviewed decision - they are named
// here rather than filtered by a pattern so the exclusion cannot quietly widen.
var ambientSimFlags = map[string]bool{"json": true, "udid": true}

// simGuidanceBudget caps the always-seen block. Brevity is the whole reason the
// prompt layer works, so growing it has to be a deliberate act: if the text
// genuinely needs more room, raise this and say why in the commit.
const simGuidanceBudget = 3000

func TestSimGuidance_DecidesEverySubcommand(t *testing.T) {
	guidance := prompts.SimulatorGuidance()

	if len(guidance) > simGuidanceBudget {
		t.Errorf("the simulator guidance is %d bytes, over its %d-byte budget: it is the block every iOS worker always sees, so either cut something or raise the budget deliberately", len(guidance), simGuidanceBudget)
	}

	// Whole word, so `ao sim drag` does not satisfy a check for `ao sim dr`,
	// and `--id` is not satisfied by `--identifier`.
	teaches := func(surface string) bool {
		pattern := `\bao sim ` + regexp.QuoteMeta(surface) + `\b`
		if cmd, flag, isFlag := splitSimFlagKey(surface); isFlag {
			// A flag counts as taught where it is shown ON the command it
			// belongs to, so `--duration` taught for `drag` can never be read
			// as `swipe` having been taught it.
			pattern = `\bao sim ` + regexp.QuoteMeta(cmd) + `\b[^\n]*--` + regexp.QuoteMeta(flag) + `\b`
		}
		return regexp.MustCompile(pattern).MatchString(guidance)
	}

	decided := map[string]bool{}
	for _, surface := range simSurfaces(t) {
		want, reviewed := simPromptDecisions[surface]
		if !reviewed {
			t.Errorf("`ao sim %s` is new and nothing decided whether it belongs in the worker prompt. Add it to simPromptDecisions: true means prompts.SimulatorGuidance() teaches it, false means the skill page is enough.", surface)
			continue
		}
		decided[surface] = true
		switch got := teaches(surface); {
		case want && !got:
			t.Errorf("`ao sim %s` is marked prompt-worthy but the worker prompt does not teach it", surface)
		case !want && got:
			t.Errorf("`ao sim %s` is marked skill-page-only but the worker prompt teaches it; either flip the decision or take it back out", surface)
		}
	}

	var stale []string
	for surface := range simPromptDecisions {
		if !decided[surface] {
			stale = append(stale, surface)
		}
	}
	sort.Strings(stale)
	for _, surface := range stale {
		t.Errorf("simPromptDecisions decides `ao sim %s`, which the CLI does not have: a decision about a command that does not exist is the same drift the other way", surface)
	}
}

// simSurfaces is every `ao sim` surface a decision is owed for: each visible
// subcommand, plus the non-ambient flags of the ones the prompt teaches. A
// subcommand of an omitted command is covered by the parent's decision, so the
// walk stops there rather than listing `record start` and friends.
func simSurfaces(t *testing.T) []string {
	t.Helper()
	var sim *cobra.Command
	for _, cmd := range NewRootCommand(Deps{}).Commands() {
		if cmd.Name() == "sim" {
			sim = cmd
		}
	}
	if sim == nil {
		t.Fatal("no `ao sim` command")
	}

	var surfaces []string
	var walk func(parent string, cmd *cobra.Command)
	walk = func(parent string, cmd *cobra.Command) {
		for _, sub := range cmd.Commands() {
			if sub.Hidden || sub.Name() == "help" {
				continue
			}
			name := sub.Name()
			if parent != "" {
				name = parent + " " + name
			}
			surfaces = append(surfaces, name)
			if !simPromptDecisions[name] {
				continue
			}
			sub.Flags().VisitAll(func(f *pflag.Flag) {
				if !ambientSimFlags[f.Name] {
					surfaces = append(surfaces, fmt.Sprintf("%s --%s", name, f.Name))
				}
			})
			walk(name, sub)
		}
	}
	walk("", sim)
	sort.Strings(surfaces)
	return surfaces
}

// splitSimFlagKey reads a "<command> --<flag>" decision key back apart.
func splitSimFlagKey(surface string) (cmd, flag string, ok bool) {
	for i := 0; i+2 < len(surface); i++ {
		if surface[i] == ' ' && surface[i+1] == '-' && surface[i+2] == '-' {
			return surface[:i], surface[i+3:], true
		}
	}
	return surface, "", false
}
