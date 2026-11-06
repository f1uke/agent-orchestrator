package cli

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/transportsettings"
)

func TestE2E_SettingsWorkerTransportShowsAndSetsTheGlobalDefault(t *testing.T) {
	dir := t.TempDir()
	store, err := transportsettings.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	startDriftTestDaemonWith(t, httpd.APIDeps{TransportSettings: store})

	out, err := runDriftCLI(t, "settings", "worker-transport")
	if err != nil || !strings.Contains(out, "worker transport: chat") {
		t.Fatalf("show on a fresh data dir: %v\n%s\nwant chat, the built-in default", err, out)
	}

	out, err = runDriftCLI(t, "settings", "worker-transport", "terminal")
	if err != nil || !strings.Contains(out, "worker transport: terminal") {
		t.Fatalf("set terminal: %v\n%s", err, out)
	}
	reopened, _ := transportsettings.NewStore(dir)
	if got := reopened.WorkerTransport(); got != domain.TransportTerminal {
		t.Fatalf("the data dir holds %q after the command, want terminal", got)
	}

	out, err = runDriftCLI(t, "settings", "worker-transport", "--json")
	if err != nil || !strings.Contains(out, `"workerTransport": "terminal"`) {
		t.Fatalf("--json: %v\n%s", err, out)
	}
}

func TestSettingsWorkerTransport_RefusesWhatIsNotATransportBeforeTheDaemon(t *testing.T) {
	setConfigEnv(t)
	for _, arg := range []string{"pigeon", "Chat", " "} {
		_, _, err := executeCLI(t, Deps{}, "settings", "worker-transport", arg)
		if got := ExitCode(err); got != 2 {
			t.Fatalf("arg %q: exit code = %d, want 2 (err=%v)", arg, got, err)
		}
	}
	_, _, err := executeCLI(t, Deps{}, "settings", "worker-transport", "chat", "terminal")
	if got := ExitCode(err); got != 2 {
		t.Fatalf("two args: exit code = %d, want 2 (err=%v)", got, err)
	}
}

func TestProjectSetConfig_WorkerTransportIsValidatedAndClearable(t *testing.T) {
	setConfigEnv(t)
	_, _, err := executeCLI(t, Deps{}, "project", "set-config", "demo", "--worker-transport", "pigeon")
	if got := ExitCode(err); got != 2 {
		t.Fatalf("--worker-transport pigeon: exit code = %d, want 2 (err=%v)", got, err)
	}

	got := captureSetConfig(t, "--worker-transport", "")
	if want := []string{"workerTransport"}; strings.Join(got.MergeFields, ",") != strings.Join(want, ",") || got.Config.WorkerTransport != "" {
		t.Fatalf("--worker-transport \"\" sent %+v, want only the field named, empty, so the project inherits again", got)
	}
}

func TestE2E_SpawnPrintsTheTransportTheDaemonResolved(t *testing.T) {
	sessions := &fakeSessionService{resolvedTransport: domain.TransportChat}
	startDriftTestDaemon(t, sessions, &fakeProjectManager{})

	out, err := runDriftCLI(t, "spawn", "--project", "mer", "--harness", "claude-code", "--from", "main", "--prompt", "hi", "--name", "w", "--skip-agent-check")
	if err != nil {
		t.Fatalf("spawn: %v\n%s", err, out)
	}
	if sessions.spawned.Transport != "" {
		t.Fatalf("the CLI sent transport %q for a spawn that named none; the daemon must be left to resolve it", sessions.spawned.Transport)
	}
	if !strings.Contains(out, "transport: chat\n") {
		t.Fatalf("spawn summary:\n%s\nwant it to say transport: chat", out)
	}

	out, err = runDriftCLI(t, "spawn", "--project", "mer", "--harness", "claude-code", "--from", "main", "--prompt", "hi", "--name", "w", "--skip-agent-check", "--transport", "terminal")
	if err != nil || !strings.Contains(out, "transport: terminal\n") {
		t.Fatalf("explicit terminal: %v\n%s", err, out)
	}
}

func TestE2E_SettingsOrchestratorTransportShowsAndSetsTheGlobalDefaultAndKeepsTheWorkerOne(t *testing.T) {
	dir := t.TempDir()
	store, err := transportsettings.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	startDriftTestDaemonWith(t, httpd.APIDeps{TransportSettings: store})

	out, err := runDriftCLI(t, "settings", "orchestrator-transport")
	if err != nil || !strings.Contains(out, "orchestrator transport: chat") {
		t.Fatalf("show on a fresh data dir: %v\n%s\nwant chat, the built-in default", err, out)
	}

	if out, err = runDriftCLI(t, "settings", "worker-transport", "terminal"); err != nil {
		t.Fatalf("set worker terminal: %v\n%s", err, out)
	}
	out, err = runDriftCLI(t, "settings", "orchestrator-transport", "terminal")
	if err != nil || !strings.Contains(out, "orchestrator transport: terminal") {
		t.Fatalf("set terminal: %v\n%s", err, out)
	}
	reopened, _ := transportsettings.NewStore(dir)
	if reopened.OrchestratorTransport() != domain.TransportTerminal || reopened.WorkerTransport() != domain.TransportTerminal {
		t.Fatalf("the data dir holds %+v after the commands, want both terminal", reopened.Get())
	}

	if out, err = runDriftCLI(t, "settings", "orchestrator-transport", "chat"); err != nil {
		t.Fatalf("set chat: %v\n%s", err, out)
	}
	reopened, _ = transportsettings.NewStore(dir)
	if reopened.OrchestratorTransport() != domain.TransportChat || reopened.WorkerTransport() != domain.TransportTerminal {
		t.Fatalf("the data dir holds %+v, want orchestrator chat and worker still terminal", reopened.Get())
	}

	out, err = runDriftCLI(t, "settings", "orchestrator-transport", "--json")
	if err != nil || !strings.Contains(out, `"orchestratorTransport": "chat"`) {
		t.Fatalf("--json: %v\n%s", err, out)
	}
}

func TestSettingsOrchestratorTransport_RefusesWhatIsNotATransportBeforeTheDaemon(t *testing.T) {
	setConfigEnv(t)
	for _, arg := range []string{"pigeon", "Chat", " "} {
		_, _, err := executeCLI(t, Deps{}, "settings", "orchestrator-transport", arg)
		if got := ExitCode(err); got != 2 {
			t.Fatalf("arg %q: exit code = %d, want 2 (err=%v)", arg, got, err)
		}
	}
}

func TestProjectSetConfig_OrchestratorTransportIsValidatedAndClearable(t *testing.T) {
	setConfigEnv(t)
	_, _, err := executeCLI(t, Deps{}, "project", "set-config", "demo", "--orchestrator-transport", "pigeon")
	if got := ExitCode(err); got != 2 {
		t.Fatalf("--orchestrator-transport pigeon: exit code = %d, want 2 (err=%v)", got, err)
	}

	got := captureSetConfig(t, "--orchestrator-transport", "")
	if want := []string{"orchestratorTransport"}; strings.Join(got.MergeFields, ",") != strings.Join(want, ",") || got.Config.OrchestratorTransport != "" {
		t.Fatalf("--orchestrator-transport \"\" sent %+v, want only the field named, empty, so the project inherits again", got)
	}
}
