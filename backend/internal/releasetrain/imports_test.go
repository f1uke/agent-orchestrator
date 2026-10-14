package releasetrain

import (
	"os/exec"
	"strings"
	"testing"
)

func TestCoreImportsOnlyTheStandardLibrary(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", ".").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	self, err := exec.Command("go", "list", ".").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for _, dep := range strings.Fields(string(out)) {
		if dep != strings.TrimSpace(string(self)) {
			t.Fatalf("releasetrain imports %s; the core must import only the standard library", dep)
		}
	}
}
