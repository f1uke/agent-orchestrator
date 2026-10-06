package sessionmanager

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
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

// promptCell is one point of the prompt matrix: the session facts and project
// settings that change what buildSystemPrompt assembles.
type promptCell struct {
	kind          domain.SessionKind
	role          domain.CrewRole
	mobileScripts bool
	iosSimulator  bool
}

func (c promptCell) name() string {
	who := string(c.kind)
	if c.kind == domain.KindWorker {
		who += "-" + map[domain.CrewRole]string{"": "solo", domain.CrewRoleDev: "dev", domain.CrewRoleQA: "qa"}[c.role]
	}
	return fmt.Sprintf("%s-scripts_%s-sim_%s", who, onOff(c.mobileScripts), onOff(c.iosSimulator))
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
				c := w
				c.mobileScripts, c.iosSimulator = scripts, sim
				cells = append(cells, c)
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

// The blocks assembled outside buildSystemPrompt: every editable default base
// (the reviewer's is built by the review engine) and the response-language
// directive, which renders nothing for English and so never shows in the matrix.
func TestPromptBlocks_TeachNoRemovedCommand(t *testing.T) {
	for _, k := range prompts.KnownKinds() {
		assertTeachesNoRemovedCommand(t, prompts.DefaultBase(k)+prompts.CoordinationFloor(k))
	}
	assertTeachesNoRemovedCommand(t, prompts.ResponseLanguageDirective("Thai"))
}

func assertTeachesNoRemovedCommand(t *testing.T, prompt string) {
	t.Helper()
	lower := strings.ToLower(prompt)
	for _, word := range removedCommands {
		if i := strings.Index(lower, strings.ToLower(word)); i >= 0 {
			from, to := max(0, i-120), min(len(prompt), i+120)
			t.Errorf("prompt teaches %q, which AO no longer has: ...%s...", word, prompt[from:to])
		}
	}
}
