package sessionmanager

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/prompts"
)

var updatePrompts = flag.Bool("update", false, "rewrite testdata/prompts/*.md from the assembled system prompts")

// removedCommands are words no standing prompt may teach: each names a command,
// flag or surface AO no longer has. Matched case-insensitively.
var removedCommands = []string{
	"smoke",
	"Tests tab",
	"--still-working",
	"stand-down",
}

// staleTestinyRules are Testiny rules that stopped being true when AO began
// recording results itself: no prompt may still state them.
var staleTestinyRules = []string{
	"AO never writes to Testiny",
	"Every Testiny WRITE waits for the human's explicit yes",
}

// A result written with the raw Testiny CLI skips AO's log, its policy on who
// may write and the tab's refresh. The command may appear only as the thing
// never to run directly: "never" (at most one word between) right before it and
// "directly" right after, so any other mention, an instruction above all, fails.
const directResultWrite = "testiny run results set"

var neverDirectResultWrite = regexp.MustCompile("(?i)\\bnever(?: \\w+)? `" + directResultWrite + "` directly\\b")

// promptCell is one point of the prompt matrix: the session facts and project
// settings that change what buildSystemPrompt assembles.
type promptCell struct {
	kind          domain.SessionKind
	role          domain.CrewRole
	mobileScripts bool
	iosSimulator  bool
	testiny       bool
}

func (c promptCell) name() string {
	who := string(c.kind)
	if c.kind == domain.KindWorker {
		who += "-" + map[domain.CrewRole]string{"": "solo", domain.CrewRoleDev: "dev", domain.CrewRoleQA: "qa"}[c.role]
	}
	return fmt.Sprintf("%s-scripts_%s-sim_%s-testiny_%s", who, onOff(c.mobileScripts), onOff(c.iosSimulator), onOff(c.testiny))
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// config is the project the cell's prompt is built for. A script-only project
// targets iOS when it has a simulator and Android when it does not, so the
// matrix renders both platforms' blocks.
func (c promptCell) config() domain.ProjectConfig {
	cfg := domain.ProjectConfig{HasIOSSimulator: c.iosSimulator}
	if c.testiny {
		cfg.TestinyProject = "MOB"
	}
	if c.mobileScripts {
		platform := domain.MobilePlatformAndroid
		if c.iosSimulator {
			platform = domain.MobilePlatformIOS
		}
		cfg.MobileScripts = &domain.MobileScriptsConfig{Product: "nter", Platform: platform}
	}
	return cfg
}

// promptMatrix is every standing prompt a session can be built with. A new
// project setting that changes a prompt is one more loop here.
func promptMatrix() []promptCell {
	who := []promptCell{
		{kind: domain.KindOrchestrator},
		{kind: domain.KindWorker},
		{kind: domain.KindWorker, role: domain.CrewRoleDev},
		{kind: domain.KindWorker, role: domain.CrewRoleQA},
	}
	var cells []promptCell
	for _, w := range who {
		for _, scripts := range []bool{false, true} {
			for _, sim := range []bool{false, true} {
				for _, testiny := range []bool{false, true} {
					c := w
					c.mobileScripts, c.iosSimulator, c.testiny = scripts, sim, testiny
					cells = append(cells, c)
				}
			}
		}
	}
	return cells
}

func (c promptCell) build(t *testing.T) string {
	t.Helper()
	st := crewPromptStore(t)
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: c.config()}
	got, err := layeredManager(st, nil).buildSystemPrompt(ctx, systemPromptSpec{
		Kind: c.kind, ProjectID: "mer", TaskSize: domain.TaskSizeStandard, CrewRole: c.role,
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// TestPromptMatrix_TeachesNoRemovedCommand assembles every standing prompt
// through the real builder and refuses one that teaches a command AO no longer
// has. Run with -update to rewrite testdata/prompts, so a reviewer reads every
// rendered prompt in the diff.
func TestPromptMatrix_TeachesNoRemovedCommand(t *testing.T) {
	dir := filepath.Join("testdata", "prompts")
	if *updatePrompts {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range promptMatrix() {
		t.Run(c.name(), func(t *testing.T) {
			got := c.build(t)
			assertTeachesNoRemovedCommand(t, got)
			assertTeachesNoDirectResultWrite(t, got)
			assertAllowsTestAccounts(t, got)
			c.assertTestinyBlock(t, got)
			c.assertCaseScriptBlock(t, got)
			path := filepath.Join(dir, c.name()+".md")
			if *updatePrompts {
				if err := os.WriteFile(path, []byte(got+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run go test ./internal/session_manager -run TestPromptMatrix -update)", err)
			}
			if string(want) != got+"\n" {
				t.Fatalf("%s is stale: run go test ./internal/session_manager -run TestPromptMatrix -update and review the diff", path)
			}
		})
	}
}

// testinyHeading is the block every worker kind gets on a Testiny project, and
// nothing else gets: the orchestrator dispatches, and the reviewer prompt is
// built by the review engine.
const testinyHeading = "## Testiny test cases (AO)"

func (c promptCell) assertTestinyBlock(t *testing.T, got string) {
	t.Helper()
	want := c.testiny && c.kind == domain.KindWorker
	block := section(got, testinyHeading)
	if has := block != ""; has != want {
		t.Fatalf("%s: Testiny block present = %v, want %v", c.name(), has, want)
	}
	if !want {
		return
	}
	for _, s := range []string{
		"Testiny project `MOB`",
		"`managing-testiny-qa`",
		"~/.ao/knowledge/mer/plans/<branch>--testiny.md",
		`ao testiny link "$AO_CREW_ID" <run-id>`,
		"Never upload evidence",
		"Every other Testiny write waits for the human's explicit yes",
	} {
		if !strings.Contains(block, s) {
			t.Errorf("%s: Testiny block is missing %q", c.name(), s)
		}
	}
	c.assertResultRecording(t, block)
}

// testinyLoop opens the loop qa and a solo worker follow to play a run and
// record it. A crew's dev does not record results: playing the run is the check
// it hands to qa.
const testinyLoop = "**Playing a run, start to finish.**"

func (c promptCell) assertResultRecording(t *testing.T, block string) {
	t.Helper()
	records := c.role != domain.CrewRoleDev
	if has := strings.Contains(block, testinyLoop); has != records {
		t.Fatalf("%s: the record-a-run loop is present = %v, want %v:\n%s", c.name(), has, records, block)
	}
	if !records {
		for _, s := range []string{"Results are qa's", "do not record results yourself"} {
			if !strings.Contains(block, s) {
				t.Errorf("%s: dev is not told results are qa's, missing %q:\n%s", c.name(), s, block)
			}
		}
		for _, s := range []string{"ao testiny result", "testiny case link"} {
			if strings.Contains(block, s) {
				t.Errorf("%s: dev is taught %q, which is qa's:\n%s", c.name(), s, block)
			}
		}
		return
	}
	for _, s := range []string{
		"**Recording a result or linking a case to this task's Jira issue needs no yes.**",
		// Every case qa creates for the task, and every case in its linked runs,
		// is linked to the task's Jira issue as a requirement.
		"`testiny case link <case-id> <JIRA-KEY>`",
		"every case you create, right after you create it, and every case in a run linked to this task",
		"`issue` field of `ao session get \"$AO_CREW_ID\"`",
		"no Jira issue, skip this step and say so in your report",
		"the cases you linked to the Jira issue",
		"never with `testiny run results set` directly",
		`ao testiny result "$AO_CREW_ID" <run-id> <case-id> --status <STATUS> [--comment "<reason>"] [--step <n>=<STATUS> ...]`,
		// A case with steps records each step it played on the case's own call.
		"add `--step <n>=<STATUS>` for each step you played",
		"in the same call as the case",
		"**PASSED** only when every step passed and both checks hold, with no comment",
		"plain Thai",
		"UNDRIVEABLE",
		"No Figma frame linked",
		"`TESTINY_RESULT_SET_BY_PERSON`",
		"never retry it or work around it",
		"each run's link with its counts",
		`ao testiny case "$AO_CREW_ID" <case-id>`,
		"**Test Data** names the int/uat test account and data the case needs",
	} {
		if !strings.Contains(block, s) {
			t.Errorf("%s: the record-a-run loop is missing %q:\n%s", c.name(), s, block)
		}
	}
	// Only a solo worker can be refused as not the task's qa: a person may add a
	// qa to its task while it runs. qa is never refused that way.
	solo := c.role == ""
	if has := strings.Contains(block, "`TESTINY_WRITE_NOT_YOURS`"); has != solo {
		t.Errorf("%s: the loop explains TESTINY_WRITE_NOT_YOURS = %v, want %v:\n%s", c.name(), has, solo, block)
	}
	// Where qa plays cases with case scripts, the loop points at that block
	// rather than restating how a case is played and judged.
	defers := c.role == domain.CrewRoleQA && c.mobileScripts
	if has := strings.Contains(block, `as "Playing test cases with Maestro scripts" above says`); has != defers {
		t.Errorf("%s: the loop defers playing to the case-script block = %v, want %v:\n%s", c.name(), has, defers, block)
	}
	if !defers && !strings.Contains(block, "its Figma frame") {
		t.Errorf("%s: the loop does not judge a case against its Figma frame:\n%s", c.name(), block)
	}
	// A case script takes the account its case's Test Data names through
	// --account, so where qa writes case scripts the loop says how that account
	// reaches the store's accounts file. Elsewhere there is no store to add it to.
	for _, s := range []string{
		"`~/Documents/Projects/mobile-ui-scripts/accounts/nter.json`",
		"`accounts/nter.example.json`",
		"pass it to the script with `--account <id>`",
	} {
		if has := strings.Contains(block, s); has != defers {
			t.Errorf("%s: the loop says %q = %v, want %v:\n%s", c.name(), s, has, defers, block)
		}
	}
}

// Test Data and the store's accounts hold int/uat test accounts only, which
// are safe to use and to store (the human's decision, 2026-10-08). No prompt
// may forbid copying them, or an agent refuses the very account a case needs.
var forbidsTestAccounts = []string{"Never copy an email or password", "never copy them into"}

func assertAllowsTestAccounts(t *testing.T, prompt string) {
	t.Helper()
	for _, s := range forbidsTestAccounts {
		if i := strings.Index(prompt, s); i >= 0 {
			from, to := max(0, i-120), min(len(prompt), i+120)
			t.Errorf("prompt forbids copying int/uat test accounts: ...%s...", prompt[from:to])
		}
	}
}

// section is the "## " block that starts at heading, up to the next one, or ""
// when the prompt has no such block.
func section(prompt, heading string) string {
	i := strings.Index(prompt, heading)
	if i < 0 {
		return ""
	}
	block := prompt[i:]
	if end := strings.Index(block[len(heading):], "\n## "); end >= 0 {
		block = block[:len(heading)+end]
	}
	return block
}

// caseScriptHeading is qa's block on a script-only project, Testiny on or off:
// every case it plays on a device runs as one case script, and a case passes
// only on its assertions AND a comparison with its Figma frame.
const caseScriptHeading = "## Playing test cases with Maestro scripts (AO)"

func (c promptCell) assertCaseScriptBlock(t *testing.T, got string) {
	t.Helper()
	want := c.mobileScripts && c.role == domain.CrewRoleQA
	block := section(got, caseScriptHeading)
	if has := block != ""; has != want {
		t.Fatalf("%s: case-script block present = %v, want %v", c.name(), has, want)
	}
	if !want {
		return
	}
	for _, s := range []string{
		"bin/flow run nter cases/<area>/<behaviour>",
		"projects/nter/cases/<area>/<behaviour>.yaml",
		"# testiny: <project_key> TC-<id>",
		"nter-case-<behaviour>-<step>",
		"Figma",
		"only when both hold",
		"UNDRIVEABLE",
	} {
		if !strings.Contains(block, s) {
			t.Errorf("%s: case-script block is missing %q:\n%s", c.name(), s, block)
		}
	}
	// The script's screenshots feed the design check, so the block never sends
	// qa back to reading the screen by hand to judge a case.
	for _, s := range []string{"ao sim shot", "screencap"} {
		if strings.Contains(block, s) {
			t.Errorf("%s: case-script block still judges with %q:\n%s", c.name(), s, block)
		}
	}
}

// The blocks assembled outside buildSystemPrompt: every editable default base
// (the reviewer's is built by the review engine) and the response-language
// directive, which renders nothing for English and so never shows in the matrix.
func TestPromptBlocks_TeachNoRemovedCommand(t *testing.T) {
	for _, k := range prompts.KnownKinds() {
		assertTeachesNoRemovedCommand(t, prompts.DefaultBase(k)+prompts.CoordinationFloor(k))
		assertTeachesNoDirectResultWrite(t, prompts.DefaultBase(k)+prompts.CoordinationFloor(k))
	}
	assertTeachesNoRemovedCommand(t, prompts.ResponseLanguageDirective("Thai"))
}

func assertTeachesNoRemovedCommand(t *testing.T, prompt string) {
	t.Helper()
	lower := strings.ToLower(prompt)
	for _, word := range append(removedCommands, staleTestinyRules...) {
		if i := strings.Index(lower, strings.ToLower(word)); i >= 0 {
			from, to := max(0, i-120), min(len(prompt), i+120)
			t.Errorf("prompt still says %q, which no longer holds: ...%s...", word, prompt[from:to])
		}
	}
}

func assertTeachesNoDirectResultWrite(t *testing.T, prompt string) {
	t.Helper()
	rest := neverDirectResultWrite.ReplaceAllString(prompt, "")
	if i := strings.Index(strings.ToLower(rest), directResultWrite); i >= 0 {
		from, to := max(0, i-120), min(len(rest), i+120)
		t.Errorf("prompt names %q other than to say never to run it directly: ...%s...", directResultWrite, rest[from:to])
	}
}
