package prompts

import (
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/knowledgestore"
)

// Testiny is what a worker's Testiny block is built from: the project's
// settings, resolved by the session manager.
type Testiny struct {
	// On is ProjectConfig.UsesTestiny. Off renders no block at all.
	On bool
	// ProjectID is the AO project whose knowledge store holds the drafts.
	ProjectID string
	// Skill is the folder of the project's Testiny conventions skill (holding
	// SKILL.md), or "" when the project names none or the folder lacks one.
	Skill string
	// Language is the human-facing language a failed case's reason is written
	// in: the resolved response language, "" meaning English.
	Language string
}

// TestinyProtocol is the Testiny block every worker kind (solo, dev and qa)
// gets on a project that keeps its manual test cases in Testiny, and "" on one
// that does not. role is the crew role ("" for a solo worker); caseScripts is
// the scripts store when the prompt also carries MobileScriptPlay, which then
// owns how a case is played and judged, and nil when it does not.
//
// Every Testiny convention (the case standard, the language cases are written
// in, plans, runs, milestones, comment style, the evidence folder's tree and
// names) belongs to the team, so the block defers it to the skill the project
// names, and to the human when it names none: AO ships no such skill and names
// no user skill of its own accord (#278). What it states itself are AO's own
// rules: recording a result on a linked run goes through `ao testiny result`
// and needs no yes, nor does linking a case to the task's Jira issue (it only
// adds a link and repeats safely); uploading a linked run's evidence folder to
// Drive and posting each file's link on its case's result goes through `ao
// testiny evidence` and needs no yes either (it checks the names, logs every
// upload and link, refuses a closed run, and never posts a link twice); every
// other write waits for the human's yes on a draft; and a run is linked to the
// task so the Testiny tab can show it. It names no Testiny project: an AO
// project is not tied to one (the human's decision, 2026-10-07), so the agent
// picks it from the task's Jira key and asks the human when the key does not
// settle it. Agents upload evidence themselves, unasked (the human's decision,
// 2026-10-07): the human is a developer, not QA, and no person is left to
// upload it or paste its links.
//
// Results and evidence are recorded by whoever plays the run: qa, or a solo
// worker, whose task has no qa until a person adds one (AO then tells it). A
// crew's dev hands that check to qa, so its block names both as qa's and carries
// no loop. The daemon enforces the same split (TESTINY_WRITE_NOT_YOURS); the
// prompt keeps an agent from reaching for a write it would be refused. The
// evidence step sits between the results and the report because a closed run is
// frozen, and the folder's tree needs the run's plan and milestone.
//
// The loop lives here rather than in MobileScriptPlay because it is Testiny's:
// that block renders with Testiny off too, and is qa's alone on a script-only
// project, while a solo worker and a qa without case scripts record results as
// well. Where MobileScriptPlay is present, the loop points at it instead of
// restating how a case is played.
func TestinyProtocol(t Testiny, role string, caseScripts *MobileScripts) string {
	if !t.On {
		return ""
	}
	play := testinyPlay + testinyTestData + "."
	if caseScripts != nil {
		play = testinyPlayCaseScripts + testinyTestData + caseScripts.fill(testinyCaseScriptAccount)
	}
	lang := strings.TrimSpace(t.Language)
	if lang == "" {
		lang = DefaultResponseLanguage
	}
	loop := strings.NewReplacer("{{play}}", play, "{{lang}}", lang).Replace(testinyLoop)
	owner, results, tail := testinyOwnerSolo, testinyResultsRecorded, loop+testinyNotYours+testinyEvidenceStep+testinyReportSolo
	switch role {
	case "dev":
		owner, results, tail = testinyOwnerDev, testinyResultsQAs, testinyReportDev
	case "qa":
		owner, tail = testinyOwnerQA, loop+testinyEvidenceStep+testinyReportQA
	}
	guide := testinyNoSkill
	if t.Skill != "" {
		guide = strings.Replace(testinySkill, "{{skill}}", strings.TrimSuffix(t.Skill, "/")+"/SKILL.md", 1)
	}
	return strings.NewReplacer(
		"{{owner}}", owner,
		"{{guide}}", guide,
		"{{results}}", results,
		"{{tail}}", tail,
		"{{plans}}", knowledgestore.PromptDir+"/"+t.ProjectID+"/plans",
	).Replace(testinyProtocol)
}

const testinyProtocol = "\n\n" + `## Testiny test cases (AO)

This project keeps its manual test cases in Testiny. It is not tied to one Testiny project: one task may hold runs from several, and each linked run carries its own. {{owner}} {{guide}}

- **Pick the Testiny project from the task's Jira key**, the ` + "`issue`" + ` field of ` + "`ao session get \"$AO_CREW_ID\"`" + `: its prefix names the project by name or key. STAR-2413 is in STAR; MOBILITY-123 is in the MOBILITY project, whose key is MOB. ` + "`testiny project ls`" + ` lists every project with its name and key. When the key does not clearly name one, or the task has no Jira issue, ask the human which project before you write anything.
- **Reading Testiny needs no permission.**
{{results}}
- **Every other Testiny write waits for the human's explicit yes**: creating or editing a case, plan or run, or a milestone link. Draft it at ` + "`{{plans}}/<branch>--testiny.md`" + `, show the human that draft, and run the write only after they approve it. A yes covers the draft you showed and nothing more.
- **Link each run for this task once it exists**, so it shows in the Testiny tab: ` + "`ao testiny link \"$AO_CREW_ID\" <run-url>`" + `, or ` + "`ao testiny link \"$AO_CREW_ID\" <run-id> --project <KEY>`" + `. Linking a run is AO's own record, not a Testiny write, and needs no permission. ` + "`ao testiny runs \"$AO_CREW_ID\"`" + ` shows what is linked, each run's project, and each case's status.{{tail}}`

const testinySkill = "Follow the skill at `{{skill}}` for every Testiny convention: the case standard and the language cases are written in, plans, runs, results, milestones and the evidence folder. Do not restate or improvise its rules."

const testinyNoSkill = "This project names no skill for its Testiny conventions: before you write or edit a case, or build an evidence folder, ask the human for the team's case standard, the language cases are written in, and the evidence folder's layout."

const testinyOwnerSolo = `You own everything in this section, recording results included. If a person adds a qa to your task, AO tells you, and from then on it is qa's.`

const testinyOwnerDev = `Until your task has a qa you own the cases, plans and runs here; once it has one, qa owns everything in this section and you do not write to Testiny.`

const testinyOwnerQA = `You own everything in this section; dev does not write to Testiny.`

const testinyResultsRecorded = `- **Recording a result or linking a case to this task's Jira issue needs no yes.** A result goes on a run already linked to this task: a case's status, with a reason unless it PASSED, and its steps' results. Record it with ` + "`ao testiny result`" + `, never with ` + "`testiny run results set`" + ` directly: AO logs the write, enforces who may write, and updates the Testiny tab. A link only adds the issue to the case as a requirement: linking twice writes nothing, and it never writes to Jira.
- **Uploading a run's evidence and linking it on each result needs no yes either.** Do it with ` + "`ao testiny evidence`" + `, never with ` + "`rclone`" + `, ` + "`testiny run results comment`" + ` or ` + "`testiny attach up`" + ` directly: AO checks the folder's names, uploads it to Google Drive, posts each file's link on its case's result in the run without changing the status, and logs every upload and link. It refuses a closed run or one not linked to this task, and never posts a link twice, so running it again is safe.`

const testinyResultsQAs = `- **Results and evidence are qa's, never yours.** Playing the run, recording each case's result and linking its evidence is the check you ask a qa for once the change is done, so do not record results or upload evidence yourself.`

const testinyLoop = "\n\n" + `**Playing a run, start to finish.**

1. **Plan.** ` + "`ao testiny runs \"$AO_CREW_ID\"`" + ` lists the runs linked to this task. None yet: draft the cases, the plan, and a run of that plan on the sprint's milestone (the evidence folder needs both), get the human's yes, create them, and link the run.
2. **Link each case to this task's Jira issue** as a requirement: every case you create, right after you create it, and every case in a run linked to this task. Run ` + "`testiny case link <case-id> <JIRA-KEY>`" + `, with the key from the ` + "`issue`" + ` field of ` + "`ao session get \"$AO_CREW_ID\"`" + `. If the task has no Jira issue, skip this step and say so in your report.
3. **Play each case.** Read it first: ` + "`ao testiny case \"$AO_CREW_ID\" <case-id>`" + ` prints its test data, precondition, and each step with its expected result. {{play}}
4. **Record each case and its steps:** ` + "`ao testiny result \"$AO_CREW_ID\" <run-id> <case-id> --status <STATUS> [--comment \"<reason>\"] [--step <n>=<STATUS> ...]`" + `, or a whole run at once with ` + "`--from-file`" + `. Give ` + "`--step <n>=<STATUS>`" + ` for each step you played, numbered as ` + "`ao testiny case`" + ` prints them, in the same call as the case; a step you never reached gets none.
   - **PASSED** only when every step passed and both checks hold, with no comment.
   - **FAILED** with a short reason in plain {{lang}}: one or two sentences on what went wrong.
   - **BLOCKED** when you could not drive the case (UNDRIVEABLE), with the reason from your attempt, never a guess.
   - **No Figma frame linked:** record what the expected result gives, and leave the visual check for a person.
   - **Refused with ` + "`TESTINY_RESULT_SET_BY_PERSON`" + `:** a person already decided that case or step, and their status stands. Report your finding instead; never retry it or work around it.`

// testinyNotYours is a solo worker's alone: a person may add a qa to its task
// while it runs, and from then on the daemon refuses its results.
const testinyNotYours = "\n" + `   - **Refused with ` + "`TESTINY_WRITE_NOT_YOURS`" + `:** a qa has joined your task, and results and evidence are its to record.`

const testinyPlay = `Play it from that, and judge it on two checks: its expected result and, for a case that shows UI, the screen against its Figma frame. ` + oneShotOrder

const testinyPlayCaseScripts = `Write its case script from that, or check that its script still matches it, then play it with the script, as "Playing test cases with Maestro scripts" above says: its assertions and the Figma comparison are the two checks.`

// testinyTestData opens the Test Data bullet. Test Data holds int/uat test
// accounts only, which are safe to use and to store.
const testinyTestData = "\n   - **Test Data** names the int/uat test account and data the case needs: use it to play the case"

// testinyCaseScriptAccount finishes the Test Data bullet where qa writes case
// scripts: a script takes its account through --account, so the account goes
// in the store's accounts file. The store's no-data-in-script rule is about
// reuse, not secrecy.
const testinyCaseScriptAccount = `. When the case script needs that account and ` + "`{{root}}/accounts/{{product}}.json`" + ` does not have it yet, add it there under a clear id (the file is git-ignored; its shape is in ` + "`accounts/{{product}}.example.json`" + `) and pass it to the script with ` + "`--account <id>`" + `. The script still takes the account through ` + "`--account`" + `, never as values written into it.`

// testinyEvidenceStep follows the results: a closed run is frozen, so the links
// go on before anyone closes it.
const testinyEvidenceStep = "\n" + `5. **Upload the evidence and link it on each result**, before the run is closed: a closed run is frozen. Build the run's folder under QA Evidence as the team's conventions say, every name read from Testiny, with its README and each case's screenshots or recordings. Then run ` + "`ao testiny evidence \"$AO_CREW_ID\" <run-id>`" + `. Refused for a name or a file: fix every problem it lists and run it again. Refused because the run has no plan or no milestone: adding one is a Testiny write, so draft it and ask the human, then run it again. Any other refusal (a closed run, no Drive folder set, Drive sign-in): report it with its message, and never work around it.`

const testinyReportItems = `the commit you tested, each run's link with its counts, every case that did not pass and why, the cases and runs you created, the cases you linked to the Jira issue (or that the task has none), whether each case ran against the real API or which mock set and why, the run's evidence folder and its Drive folder, whether every case's evidence is linked on its result (or what ` + "`ao testiny evidence`" + ` refused and why), and what is left for a person: visual checks with no Figma frame, steps you could not play, and cases a person had already set.`

const testinyReportSolo = "\n6. **Your finish report names** " + testinyReportItems

const testinyReportQA = "\n6. **Hand back** to dev with " + testinyReportItems

const testinyReportDev = "\n" + `- **Your report names what you did here**: the case and run ids you created, and the path of any draft you wrote.`
