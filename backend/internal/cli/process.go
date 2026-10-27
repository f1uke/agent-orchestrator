package cli

import (
	"os"
	"os/exec"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

type processStartConfig struct {
	Path   string
	Args   []string
	Env    []string
	Stdout *os.File
	Stderr *os.File
}

func startProcess(cfg processStartConfig) error {
	cmd := exec.Command(cfg.Path, cfg.Args...)
	cmd.Env = cfg.Env
	cmd.Stdout = cfg.Stdout
	cmd.Stderr = cfg.Stderr
	// Detach the daemon into its own session/process group so a Ctrl-C in the
	// terminal where `ao start` is waiting for readiness doesn't also SIGINT the
	// freshly spawned daemon (it would otherwise share the launcher's group).
	aoprocess.Detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
