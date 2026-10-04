package decide

import (
	"strings"
	"testing"
)

func TestDiff_AChangedLineIsTheRemovalThenTheAddition(t *testing.T) {
	got := Diff("/m/x.md", "a\nold\nc\n", "a\nnew\nc\n")
	if !strings.Contains(got, "@@ -1,3 +1,3 @@\n a\n-old\n+new\n c\n") {
		t.Errorf("diff =\n%s", got)
	}
}
