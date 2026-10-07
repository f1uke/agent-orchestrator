package prompts

import (
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/knowledgestore"
)

// TestinyProtocol is the Testiny block every worker kind (solo, dev and qa)
// gets on a project that keeps its manual test cases in Testiny, and "" on one
// that does not. testinyProject is ProjectConfig.TestinyProject; projectID is
// the AO project whose knowledge store holds the drafts; role is the crew role
// ("" for a solo worker); caseScripts is the scripts store when the prompt also
// carries MobileScriptPlay, which then owns how a case is played and judged, and
// nil when it does not.
//
// It defers every Testiny convention (the case standard, the language cases are
// written in, plans, runs, milestones, comment style, evidence folder names) to
// the managing-testiny-qa skill. What it states itself are AO's own rules:
// recording a result on a linked run goes through `ao testiny result` and needs
// no yes, nor does linking a case to the task's Jira issue (it only adds a link
// and repeats safely), every other write waits for the human's yes on a draft,
// evidence is never uploaded, and a run is linked to the task so the Testiny tab
// can show it.
//
// Results are recorded by whoever plays the run: qa, or a solo worker, whose
// task has no qa until a person adds one (AO then tells it). A crew's dev hands
// that check to qa, so its block names results as qa's and carries no loop. The
// daemon enforces the same split (TESTINY_WRITE_NOT_YOURS); the prompt keeps an
// agent from reaching for a write it would be refused.
//
// The loop lives here rather than in MobileScriptPlay because it is Testiny's:
// that block renders with Testiny off too, and is qa's alone on a script-only
// project, while a solo worker and a qa without case scripts record results as
// well. Where MobileScriptPlay is present, the loop points at it instead of
// restating how a case is played.
func TestinyProtocol(testinyProject, projectID, role string, caseScripts *MobileScripts) string {
	if testinyProject == "" {
		return ""
	}
	play := testinyPlay + testinyTestData + "."
	if caseScripts != nil {
		play = testinyPlayCaseScripts + testinyTestData + caseScripts.fill(testinyCaseScriptAccount)
	}
	loop := strings.Replace(testinyLoop, "{{play}}", play, 1)
	owner, results, tail := testinyOwnerSolo, testinyResultsRecorded, loop+testinyNotYours+testinyReportSolo
	switch role {
	case "dev":
		owner, results, tail = testinyOwnerDev, testinyResultsQAs, testinyReportDev
	case "qa":
		owner, tail = testinyOwnerQA, loop+testinyReportQA
	}
	return strings.NewReplacer(
		"{{owner}}", owner,
		"{{results}}", results,
		"{{tail}}", tail,
		"{{testiny}}", testinyProject,
		"{{plans}}", knowledgestore.PromptDir+"/"+projectID+"/plans",
	).Replace(testinyProtocol)
}

const testinyProtocol = "\n\n" + `## Testiny test cases (AO)

This project keeps its manual test cases in Testiny project ` + "`{{testiny}}`" + `. {{owner}} Follow the ` + "`managing-testiny-qa`" + ` skill for every Testiny step: the case standard and the language cases are written in, plans, runs, results, milestones and the evidence folder. Do not restate or improvise its rules.

- **Reading Testiny needs no permission.**
{{results}}
- **Every other Testiny write waits for the human's explicit yes**: creating or editing a case, plan or run, a milestone link or an attachment. Draft it first at ` + "`{{plans}}/<branch>--testiny.md`" + `, show the human that draft, and run the write only after they approve it. A yes covers the draft you showed and nothing more.
- **Never upload evidence**, to Testiny or anywhere else. Save screenshots and recordings in the run's QA Evidence folder, with the names the skill gives.
- **Link each run for this task once it exists**, so it shows in the Testiny tab: ` + "`ao testiny link \"$AO_CREW_ID\" <run-id>`" + `. Linking a run is AO's own record, not a Testiny write, and needs no permission. ` + "`ao testiny runs \"$AO_CREW_ID\"`" + ` shows what is linked and each case's status.{{tail}}`

const testinyOwnerSolo = `You own everything in this section, recording results included. If a person adds a qa to your task, AO tells you, and from then on it is qa's.`

const testinyOwnerDev = `Until your task has a qa you own the cases, plans and runs here; once it has one, qa owns everything in this section and you do not write to Testiny.`

const testinyOwnerQA = `You own everything in this section; dev does not write to Testiny.`

const testinyResultsRecorded = `- **Recording a result or linking a case to this task's Jira issue needs no yes.** A result goes on a run already linked to this task: a case's status, with a reason unless it PASSED, and its steps' results. Record it with ` + "`ao testiny result`" + `, never with ` + "`testiny run results set`" + ` directly: AO logs the write, enforces who may write, and updates the Testiny tab. A link only adds the issue to the case as a requirement: linking twice writes nothing, and it never writes to Jira.`

const testinyResultsQAs = `- **Results are qa's, never yours.** Playing the run and recording each case's result is the check you ask a qa for once the change is done, so do not record results yourself.`

const testinyLoop = "\n\n" + `**Playing a run, start to finish.**

1. **Plan.** ` + "`ao testiny runs \"$AO_CREW_ID\"`" + ` lists the runs linked to this task. None yet: draft the cases, plan and run, get the human's yes, create them, and link the run.
2. **Link each case to this task's Jira issue** as a requirement: every case you create, right after you create it, and every case in a run linked to this task. Run ` + "`testiny case link <case-id> <JIRA-KEY>`" + `, with the key from the ` + "`issue`" + ` field of ` + "`ao session get \"$AO_CREW_ID\"`" + ` (` + "`jira:<KEY>`" + `). If the task has no Jira issue, skip this step and say so in your report.
3. **Play each case.** Read it first: ` + "`ao testiny case \"$AO_CREW_ID\" <case-id>`" + ` prints its test data, precondition, and each step with its expected result. {{play}}
4. **Record each case and its steps:** ` + "`ao testiny result \"$AO_CREW_ID\" <run-id> <case-id> --status <STATUS> [--comment \"<reason>\"] [--step <n>=<STATUS> ...]`" + `, or a whole run at once with ` + "`--from-file`" + `.
   - **Steps:** when the case has steps, add ` + "`--step <n>=<STATUS>`" + ` for each step you played, numbered as ` + "`ao testiny case`" + ` prints them, in the same call as the case. A step you never reached gets none.
   - **PASSED** only when every step passed and both checks hold, with no comment.
   - **FAILED** with a short reason in plain Thai: one or two sentences on what went wrong.
   - **BLOCKED** when you could not drive the case (UNDRIVEABLE), with the reason from your attempt, never a guess.
   - **No Figma frame linked:** record what the expected result gives, and leave the visual check for a person.
   - **Refused with ` + "`TESTINY_RESULT_SET_BY_PERSON`" + `:** a person already decided that case or step, and their status stands. Report your finding instead; never retry it or work around it.`

// testinyNotYours is a solo worker's alone: a person may add a qa to its task
// while it runs, and from then on the daemon refuses its results.
const testinyNotYours = "\n" + `   - **Refused with ` + "`TESTINY_WRITE_NOT_YOURS`" + `:** a qa has joined your task, and results are its to record.`

const testinyPlay = `Play it from that, and judge it on two checks: its expected result and, for a case that shows UI, the screen against its Figma frame. A step that cannot be undone (submit, buy, delete) stays a person's.`

const testinyPlayCaseScripts = `Write its case script from that, or check that its script still matches it, then play it with the script, as "Playing test cases with Maestro scripts" above says: its assertions and the Figma comparison are the two checks.`

// testinyTestData opens the Test Data bullet. Test Data holds int/uat test
// accounts only, which are safe to use and to store.
const testinyTestData = "\n   - **Test Data** names the int/uat test account and data the case needs: use it to play the case"

// testinyCaseScriptAccount finishes the Test Data bullet where qa writes case
// scripts: a script takes its account through --account, so the account goes
// in the store's accounts file. The store's no-data-in-script rule is about
// reuse, not secrecy.
const testinyCaseScriptAccount = `. When the case script needs that account and ` + "`{{root}}/accounts/{{product}}.json`" + ` does not have it yet, add it there under a clear id (the file is git-ignored; its shape is in ` + "`accounts/{{product}}.example.json`" + `) and pass it to the script with ` + "`--account <id>`" + `. The script still takes the account through ` + "`--account`" + `, never as values written into it.`

const testinyReportItems = `the commit you tested, each run's link with its counts, every case that did not pass and why, the cases and runs you created, the cases you linked to the Jira issue (or that the task has none), the evidence folder path, and what is left for a person: visual checks with no Figma frame, steps that cannot be undone, and cases a person had already set.`

const testinyReportSolo = "\n5. **Your finish report names** " + testinyReportItems

const testinyReportQA = "\n5. **Hand back** to dev with " + testinyReportItems

const testinyReportDev = "\n" + `- **Your report names what you did here**: the case and run ids you created, and the path of any draft you wrote.`
