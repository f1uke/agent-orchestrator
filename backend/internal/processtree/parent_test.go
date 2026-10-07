package processtree

import (
	"os"
	"os/exec"
	"runtime"
	"testing"
)

func TestParentOfThisProcessIsItsParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("parent lookup is unix-only")
	}
	got, err := Parent(os.Getpid())
	if err != nil {
		t.Fatalf("Parent: %v", err)
	}
	if got != os.Getppid() {
		t.Errorf("Parent(self) = %d, want %d", got, os.Getppid())
	}
}

func TestParentOfAChildIsThisProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("parent lookup is unix-only")
	}
	cmd := exec.Command("sleep", "5")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	got, err := Parent(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("Parent: %v", err)
	}
	if got != os.Getpid() {
		t.Errorf("Parent(child) = %d, want %d", got, os.Getpid())
	}
}

func TestParentOfAnExitedProcessIsAnError(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if got, err := Parent(cmd.Process.Pid); err == nil {
		t.Errorf("Parent(reaped pid) = %d, want an error", got)
	}
}
