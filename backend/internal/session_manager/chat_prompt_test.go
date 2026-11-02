package sessionmanager

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/prompts"
)

const chatViewLine = "The user reads your replies in AO's desktop chat view, which renders GitHub-flavored Markdown; it is not a terminal. The chat view does not send you screenshots or its page content."

func TestChatWorkerPromptSaysTheReplyIsReadInTheChatView(t *testing.T) {
	f := newChatFixture(t)
	if _, err := f.m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "do it", Transport: domain.TransportChat}); err != nil {
		t.Fatal(err)
	}
	sp := f.agent.lastLaunch.SystemPrompt
	if n := strings.Count(sp, chatViewLine); n != 1 {
		t.Fatalf("chat worker prompt carries the chat-view line %d times, want 1:\n%s", n, sp)
	}
	if line, pointer := strings.Index(sp, chatViewLine), strings.Index(sp, "## Using the ao CLI"); line > pointer {
		t.Fatalf("the chat-view line sits after the ao CLI pointer; it must precede it so the response-language directive stays last:\n%s", sp)
	}
	if !strings.HasSuffix(sp, prompts.ConfidentialityGuard) {
		t.Fatalf("the confidentiality guard is no longer last for an English project:\n%s", sp)
	}
}

func TestTerminalWorkerPromptIsUnchanged(t *testing.T) {
	f := newChatFixture(t)
	if _, err := f.m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "do it", Transport: domain.TransportTerminal}); err != nil {
		t.Fatal(err)
	}
	got := f.agent.lastLaunch.SystemPrompt
	want, err := f.m.buildSystemPrompt(ctx, systemPromptSpec{
		Kind: domain.KindWorker, ProjectID: "mer", TaskSize: domain.TaskSizeStandard, CrewRole: domain.CrewRoleDev, PRTarget: "main",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, chatViewLine) {
		t.Fatalf("terminal worker prompt carries the chat-view line:\n%s", got)
	}
	if got != want {
		t.Fatalf("terminal worker prompt differs from the transport-free prompt\n got: %q\nwant: %q", got, want)
	}
}

func TestRestoredChatSessionKeepsTheChatViewLine(t *testing.T) {
	f := newChatFixture(t)
	rec, err := f.m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "do it", Transport: domain.TransportChat})
	if err != nil {
		t.Fatal(err)
	}
	row := f.st.sessions[rec.ID]
	row.IsTerminated = true
	f.st.sessions[rec.ID] = row
	f.agent.lastLaunch = ports.LaunchConfig{}
	if _, err := f.m.Restore(ctx, rec.ID); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(f.agent.lastLaunch.SystemPrompt, chatViewLine); n != 1 {
		t.Fatalf("restored chat prompt carries the chat-view line %d times, want 1:\n%s", n, f.agent.lastLaunch.SystemPrompt)
	}
}

func TestOrchestratorPromptGetsTheLineOnlyOnChat(t *testing.T) {
	f := newChatFixture(t)
	build := func(transport domain.Transport) string {
		t.Helper()
		sp, err := f.m.buildSystemPrompt(ctx, systemPromptSpec{Kind: domain.KindOrchestrator, ProjectID: "mer", Transport: transport})
		if err != nil {
			t.Fatal(err)
		}
		return sp
	}
	bare, terminal, chat := build(""), build(domain.TransportTerminal), build(domain.TransportChat)
	if terminal != bare {
		t.Fatalf("terminal orchestrator prompt changed\n got: %q\nwant: %q", terminal, bare)
	}
	if strings.Count(chat, chatViewLine) != 1 {
		t.Fatalf("chat orchestrator prompt lacks the chat-view line:\n%s", chat)
	}
}
