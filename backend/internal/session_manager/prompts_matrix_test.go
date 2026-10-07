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
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
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

// staleEvidenceRules are what prompts said before agents uploaded evidence
// themselves (the human's decision, 2026-10-07): that evidence is never
// uploaded, or that a person uploads it, pastes its links or is asked first.
// The human is a developer, not QA, so any of them stalls a run that needs no
// yes.
var staleEvidenceRules = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bnever upload`),
	regexp.MustCompile(`(?i)\b(human|person|people)\b[^.]{0,60}\b(upload|paste)\w*`),
	regexp.MustCompile(`(?i)\bpaste\w*\b[^.]{0,40}\blinks?\b`),
}

// directTestinyWrites are the commands AO runs for an agent. A result set with
// the raw Testiny CLI skips AO's log, its policy on who may write and the tab's
// refresh; evidence uploaded with rclone, linked with a raw comment or attached
// to Testiny skips the name check, the log and the duplicate check. Each may
// appear only as a thing never to run directly: "never" (at most one word
// between) right before a list of commands that ends in "directly", so any
// other mention, an instruction above all, fails.
var directTestinyWrites = []string{
	"testiny run results set",
	"testiny run results comment",
	"testiny attach up",
	"rclone",
}

var neverDirectTestinyWrite = regexp.MustCompile("(?i)\\bnever(?: \\w+)? `[^`]+`(?:(?:,| or) `[^`]+`)* directly\\b")

// promptCell is one point of the prompt matrix: the session facts and project
// settings that change what buildSystemPrompt assembles.
type promptCell struct {
	kind          domain.SessionKind
	role          domain.CrewRole
	mobileScripts scriptsMode
	iosSimulator  bool
	testiny       bool
}

// scriptsMode is how a cell's project drives its devices with scripts: not at
// all, from the one shared store checkout (AO could not make the task a
// worktree), from the task's own worktree, or from its own worktree with a
// verify skill the device block defers to.
type scriptsMode string

const (
	scriptsOff    scriptsMode = "off"
	scriptsShared scriptsMode = "shared"
	scriptsOn     scriptsMode = "on"
	scriptsSkill  scriptsMode = "skill"
)

func (c promptCell) name() string {
	who := string(c.kind)
	if c.kind == domain.KindWorker {
		who += "-" + map[domain.CrewRole]string{"": "solo", domain.CrewRoleDev: "dev", domain.CrewRoleQA: "qa"}[c.role]
	}
	return fmt.Sprintf("%s-scripts_%s-sim_%s-testiny_%s", who, c.mobileScripts, onOff(c.iosSimulator), onOff(c.testiny))
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
		cfg.UsesTestiny = true
	}
	if c.mobileScripts != scriptsOff {
		platform := domain.MobilePlatformAndroid
		if c.iosSimulator {
			platform = domain.MobilePlatformIOS
		}
		cfg.MobileScripts = &domain.MobileScriptsConfig{Product: "nter", Platform: platform, Store: "/scripts"}
		if c.mobileScripts == scriptsSkill {
			cfg.MobileScripts.VerifySkill = "projects/nter/verify"
		}
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
		for _, scripts := range []scriptsMode{scriptsOff, scriptsShared, scriptsOn, scriptsSkill} {
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
	m := layeredManager(st, nil)
	scripts := &fakeScripts{probe: ports.ScriptsStoreProbe{Base: "main", OK: c.mobileScripts != scriptsShared, Reason: "not a git repository"}}
	m.SetScriptsStore(scripts)
	got, err := m.buildSystemPrompt(ctx, systemPromptSpec{
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
			assertTeachesNoDirectTestinyWrite(t, got)
			assertLetsAgentsUploadEvidence(t, got)
			assertAllowsTestAccounts(t, got)
			c.assertTestinyBlock(t, got)
			c.assertCaseScriptBlock(t, got)
			c.assertScriptsStore(t, got)
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

// oneBoundProject is how a prompt tied to one Testiny project named it.
var oneBoundProject = regexp.MustCompile("(?i)(in|uses|keeps its manual test cases in) Testiny project `[^`]+`")

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
		"`managing-testiny-qa`",
		"~/.ao/knowledge/mer/plans/<branch>--testiny.md",
		"one task may hold runs from several",
		"Pick the Testiny project from the task's Jira key",
		"`testiny project ls`",
		"ask the human which project",
		`ao testiny link "$AO_CREW_ID" <run-url>`,
		`ao testiny link "$AO_CREW_ID" <run-id> --project <KEY>`,
		"Every other Testiny write waits for the human's explicit yes",
	} {
		if !strings.Contains(block, s) {
			t.Errorf("%s: Testiny block is missing %q", c.name(), s)
		}
	}
	// An AO project is not tied to one Testiny project, so the block names
	// none as the project's own.
	if oneBoundProject.MatchString(block) {
		t.Errorf("%s: Testiny block names a fixed Testiny project: %q", c.name(), oneBoundProject.FindString(block))
	}
	c.assertResultRecording(t, block)
}

// testinyLoop opens the loop qa and a solo worker follow to play a run and
// record it. A crew's dev does not record results: playing the run is the check
// it hands to qa.
const testinyLoop = "**Playing a run, start to finish.**"

const evidenceCommand = "`ao testiny evidence \"$AO_CREW_ID\" <run-id>`"

func (c promptCell) assertResultRecording(t *testing.T, block string) {
	t.Helper()
	records := c.role != domain.CrewRoleDev
	if has := strings.Contains(block, testinyLoop); has != records {
		t.Fatalf("%s: the record-a-run loop is present = %v, want %v:\n%s", c.name(), has, records, block)
	}
	if !records {
		for _, s := range []string{"Results and evidence are qa's", "do not record results or upload evidence yourself"} {
			if !strings.Contains(block, s) {
				t.Errorf("%s: dev is not told results and evidence are qa's, missing %q:\n%s", c.name(), s, block)
			}
		}
		for _, s := range []string{"ao testiny result", "ao testiny evidence", "testiny case link"} {
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
		// Whoever records results uploads the run's evidence and links it on
		// each result through AO, unasked, while the run is still open.
		"**Uploading a run's evidence and linking it on each result needs no yes either.**",
		evidenceCommand,
		"before the run is closed",
		"the run has no plan or no milestone",
		"its Drive folder",
		"what `ao testiny evidence` refused and why",
	} {
		if !strings.Contains(block, s) {
			t.Errorf("%s: the record-a-run loop is missing %q:\n%s", c.name(), s, block)
		}
	}
	// The evidence goes up after the results are recorded and before the report
	// that names its Drive folder.
	result, evidence := strings.Index(block, `ao testiny result "$AO_CREW_ID"`), strings.Index(block, evidenceCommand)
	report := max(strings.Index(block, "**Your finish report names**"), strings.Index(block, "**Hand back**"))
	if result < 0 || evidence <= result || report <= evidence {
		t.Errorf("%s: the loop does not record results, then upload evidence, then report (at %d, %d, %d):\n%s", c.name(), result, evidence, report, block)
	}
	// Only a solo worker can be refused as not the task's qa: a person may add a
	// qa to its task while it runs. qa is never refused that way.
	solo := c.role == ""
	if has := strings.Contains(block, "`TESTINY_WRITE_NOT_YOURS`"); has != solo {
		t.Errorf("%s: the loop explains TESTINY_WRITE_NOT_YOURS = %v, want %v:\n%s", c.name(), has, solo, block)
	}
	// Where qa plays cases with case scripts, the loop points at that block
	// rather than restating how a case is played and judged.
	defers := c.role == domain.CrewRoleQA && c.mobileScripts != scriptsOff
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
		"`/scripts/accounts/nter.json`",
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

// assertScriptsStore checks where a worker is told its scripts go: its task's
// own store worktree, published with `ao scripts publish`, or the one shared
// checkout when AO could not make it one. A verify-skill project's device block
// defers to the skill and names no gesture or recording command.
func (c promptCell) assertScriptsStore(t *testing.T, got string) {
	t.Helper()
	worker := c.kind == domain.KindWorker && c.mobileScripts != scriptsOff
	isolated := worker && (c.mobileScripts == scriptsOn || c.mobileScripts == scriptsSkill)
	skill := worker && c.mobileScripts == scriptsSkill
	for s, want := range map[string]bool{
		"`ao scripts publish`":                                   isolated,
		"`$AO_SCRIPTS_STORE`":                                    isolated,
		"Accounts stay in the main checkout":                     isolated,
		"shared with other sessions":                             worker && !isolated,
		"the project's verify skill (AO)":                        skill,
		"`$AO_SCRIPTS_STORE/projects/nter/verify/SKILL.md`":      skill,
		"the way the verify skill says":                          skill && c.role == domain.CrewRoleQA,
		"`ao sim doctor --app <bundle id> --expect <your .app>`": skill && c.iosSimulator,
	} {
		if has := strings.Contains(got, s); has != want {
			t.Errorf("%s: carries %q = %v, want %v", c.name(), s, has, want)
		}
	}
	if skill {
		device := got[strings.Index(got, "the project's verify skill (AO)"):]
		device = device[:strings.Index(device, "Nothing in the store goes into your pull request.")]
		for _, s := range []string{"ao sim tap", "ao sim shot", "flow record", "bin/flow run"} {
			if strings.Contains(device, s) {
				t.Errorf("%s: the verify-skill device block restates %q, which the skill owns", c.name(), s)
			}
		}
	}
}

// caseScriptHeading is qa's block on a script-only project, Testiny on or off:
// every case it plays on a device runs as one case script, and a case passes
// only on its assertions AND a comparison with its Figma frame.
const caseScriptHeading = "## Playing test cases with Maestro scripts (AO)"

func (c promptCell) assertCaseScriptBlock(t *testing.T, got string) {
	t.Helper()
	want := c.mobileScripts != scriptsOff && c.role == domain.CrewRoleQA
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
	// On a Testiny project the screenshots are the run's evidence: the block
	// sends qa to the Testiny block, which follows it, rather than restating
	// how evidence reaches Drive.
	if !strings.Contains(block, `"Playing a run, start to finish" in the Testiny block below`) {
		t.Errorf("%s: case-script block does not point its evidence at the Testiny block:\n%s", c.name(), block)
	}
	if c.testiny && strings.Index(got, testinyHeading) < strings.Index(got, caseScriptHeading) {
		t.Errorf("%s: the case-script block says the Testiny block is below it, and it is not", c.name())
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
		assertTeachesNoDirectTestinyWrite(t, prompts.DefaultBase(k)+prompts.CoordinationFloor(k))
		assertLetsAgentsUploadEvidence(t, prompts.DefaultBase(k)+prompts.CoordinationFloor(k))
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

func assertTeachesNoDirectTestinyWrite(t *testing.T, prompt string) {
	t.Helper()
	rest := neverDirectTestinyWrite.ReplaceAllString(prompt, "")
	for _, cmd := range directTestinyWrites {
		if loc := regexp.MustCompile(`(?i)\b` + cmd + `\b`).FindStringIndex(rest); loc != nil {
			from, to := max(0, loc[0]-120), min(len(rest), loc[0]+120)
			t.Errorf("prompt names %q other than to say never to run it directly: ...%s...", cmd, rest[from:to])
		}
	}
}

func assertLetsAgentsUploadEvidence(t *testing.T, prompt string) {
	t.Helper()
	for _, rule := range staleEvidenceRules {
		if loc := rule.FindStringIndex(prompt); loc != nil {
			from, to := max(0, loc[0]-120), min(len(prompt), loc[1]+120)
			t.Errorf("prompt still keeps evidence from agents (%s): ...%s...", rule, prompt[from:to])
		}
	}
}
