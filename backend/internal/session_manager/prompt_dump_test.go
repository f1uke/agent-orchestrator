package sessionmanager

import (
	"os"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestDumpGatedDevPrompt writes the standing prompt a standard dev worker gets
// on a project with the check-in gate on, so tools outside Go (the playbook
// adherence harness under scripts/playbook-adherence) start every run from the
// prompt AO really composes. It does nothing unless AO_DUMP_DEV_PROMPT names
// the output file.
func TestDumpGatedDevPrompt(t *testing.T) {
	out := os.Getenv("AO_DUMP_DEV_PROMPT")
	if out == "" {
		t.Skip("AO_DUMP_DEV_PROMPT not set")
	}
	st := crewPromptStore(t)
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{PauseBeforeImplementing: true}}
	m := layeredManager(st, nil)
	got, err := m.buildSystemPrompt(ctx, systemPromptSpec{
		Kind: domain.KindWorker, ProjectID: "mer", TaskSize: domain.TaskSizeStandard, CrewRole: domain.CrewRoleDev,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, []byte(got+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
