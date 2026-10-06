package prompts

import (
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/knowledgestore"
)

// TestinyProtocol is the Testiny block every worker kind (solo, dev and qa)
// gets on a project that keeps its manual test cases in Testiny, and "" on one
// that does not. testinyProject is ProjectConfig.TestinyProject; projectID is
// the AO project whose knowledge store holds the drafts.
//
// One block for all three, rather than one per role, because who owns Testiny
// depends on whether the task gains a qa, which a dev's prompt cannot know when
// it launches. The block says qa owns it when there is one.
//
// It defers every Testiny convention (the case standard, the language cases are
// written in, plans, runs, milestones, evidence folder names) to the
// managing-testiny-qa skill. What it states itself are AO's own rules: writes
// wait for the human's yes on a draft, evidence is never uploaded, and a run is
// linked to the task so the Testiny tab can show it. AO never writes to Testiny
// itself, so the prompt is the only thing that holds the first two.
func TestinyProtocol(testinyProject, projectID string) string {
	if testinyProject == "" {
		return ""
	}
	return strings.NewReplacer(
		"{{testiny}}", testinyProject,
		"{{plans}}", knowledgestore.PromptDir+"/"+projectID+"/plans",
	).Replace(testinyProtocol)
}

const testinyProtocol = "\n\n" + `## Testiny test cases (AO)

This project keeps its manual test cases in Testiny project ` + "`{{testiny}}`" + `. When your task has a qa member, qa owns everything in this section and dev does not write to Testiny; otherwise you own it. Follow the ` + "`managing-testiny-qa`" + ` skill for every Testiny step: the case standard and the language cases are written in, plans, runs, results, milestones and the evidence folder. Do not restate or improvise its rules.

- **Reading Testiny needs no permission.**
- **Every Testiny WRITE waits for the human's explicit yes**: a case, plan, run, result, attachment or milestone link. Draft it first at ` + "`{{plans}}/<branch>--testiny.md`" + `, show the human that draft, and run the write only after they approve it. A yes covers the draft you showed and nothing more.
- **Never upload evidence**, to Testiny or anywhere else. Save screenshots and recordings in the evidence folder the skill names.
- **Link each run for this task once it exists**, so it shows in the Testiny tab: ` + "`ao testiny link \"$AO_CREW_ID\" <run-id>`" + `. Linking is AO's own record, not a Testiny write, and needs no permission. ` + "`ao testiny runs \"$AO_CREW_ID\"`" + ` shows what is linked and each case's status.
- **Your report names what you did here**: the case and run ids you created and the evidence folder path. For qa that report is the handback to dev.`
