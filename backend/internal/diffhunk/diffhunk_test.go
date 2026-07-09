package diffhunk

import "testing"

const sampleDiff = `diff --git a/foo.go b/foo.go
index 111..222 100644
--- a/foo.go
+++ b/foo.go
@@ -10,6 +10,7 @@ func foo() {
 	ctx := 10
 	ctx2 := 11
-	old := 12
+	added := 12
+	added2 := 13
 	ctx3 := 14
 	ctx4 := 15
`

func TestHunkForLineFindsCoveringHunk(t *testing.T) {
	// New line 13 is the "added2 := 13" line.
	lines, found := HunkForLine(sampleDiff, 13)
	if !found {
		t.Fatal("expected to find a hunk covering new line 13")
	}
	// The added line at new 13 must be classified add.
	var got *Line
	for i := range lines {
		if lines[i].Kind == KindAdd && lines[i].NewLine == 13 {
			got = &lines[i]
		}
	}
	if got == nil {
		t.Fatalf("no add line at new 13 in %+v", lines)
	}
	if got.Text != "	added2 := 13" {
		t.Fatalf("add text = %q", got.Text)
	}
	// The deletion must be present with OldLine set, NewLine 0.
	sawDel := false
	for _, l := range lines {
		if l.Kind == KindDel {
			sawDel = true
			if l.NewLine != 0 || l.OldLine != 12 {
				t.Fatalf("del line numbering wrong: %+v", l)
			}
		}
	}
	if !sawDel {
		t.Fatal("expected a deletion line in the hunk")
	}
	// First context line: old 10 / new 10.
	if lines[0].Kind != KindContext || lines[0].OldLine != 10 || lines[0].NewLine != 10 {
		t.Fatalf("first line = %+v, want context 10/10", lines[0])
	}
}

func TestHunkForLineContextLineMatch(t *testing.T) {
	// New line 10 is a context line ("ctx := 10").
	lines, found := HunkForLine(sampleDiff, 10)
	if !found || len(lines) == 0 {
		t.Fatalf("expected hunk for context new line 10")
	}
}

func TestHunkForLineNotFound(t *testing.T) {
	if _, found := HunkForLine(sampleDiff, 9999); found {
		t.Fatal("expected no hunk for line outside any hunk")
	}
	if _, found := HunkForLine("", 1); found {
		t.Fatal("empty diff has no hunks")
	}
}
