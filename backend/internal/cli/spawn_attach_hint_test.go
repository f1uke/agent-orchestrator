package cli

import (
	"runtime"
	"testing"
)

func TestTmuxAttachHintNamesTheSessionsOwnServer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the tmux runtime, and with it this hint, is macOS/Linux only")
	}
	cases := []struct {
		dataDir, project, branch, id, want string
	}{
		{"/Users/me/.ao/data", "proj", "feature/x", "proj-1", "tmux -S /Users/me/.ao/data/tmux/proj-feature-x attach -t proj-feature-x"},
		{"/Users/me/My AO/data", "proj", "", "proj-2", "tmux -S '/Users/me/My AO/data/tmux/proj-2' attach -t proj-2"},
	}
	for _, c := range cases {
		if got := tmuxAttachHint(c.dataDir, c.project, c.branch, c.id); got != c.want {
			t.Errorf("tmuxAttachHint(%q, %q, %q, %q) = %q, want %q", c.dataDir, c.project, c.branch, c.id, got, c.want)
		}
	}
}
