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

// gappedDiff has two hunks with a REAL numeric gap between them: the first ends
// at line 14 and the second starts at line 80, so lines 15-79 are skipped. It
// also starts at line 10, so lines 1-9 are skipped before the first hunk.
const gappedDiff = `diff --git a/report.go b/report.go
index 111..222 100644
--- a/report.go
+++ b/report.go
@@ -10,5 +10,5 @@ func first() {
 	a := 1
 	b := 2
-	c := 3
+	c := 4
 	d := 5
 	e := 6
@@ -80,5 +80,5 @@ func second() {
 	p := 1
 	q := 2
-	r := 3
+	r := 4
 	s := 5
 	t := 6
`

// hunkMarkers returns the KindHunk entries of a parse, with their index, so a
// test can assert both what the boundary says and where it sits.
func hunkMarkers(lines []Line) []struct {
	At   int
	Line Line
} {
	var out []struct {
		At   int
		Line Line
	}
	for i, l := range lines {
		if l.Kind == KindHunk {
			out = append(out, struct {
				At   int
				Line Line
			}{i, l})
		}
	}
	return out
}

func TestAllLinesMarksSkippedRegions(t *testing.T) {
	lines := AllLines(gappedDiff)
	marks := hunkMarkers(lines)
	if len(marks) != 2 {
		t.Fatalf("want 2 hunk markers (before line 10, before line 80), got %d in %+v", len(marks), lines)
	}
	if marks[0].At != 0 {
		t.Fatalf("leading marker should be the first line, got index %d", marks[0].At)
	}
	if marks[0].Line.OldLine != 10 || marks[0].Line.NewLine != 10 {
		t.Fatalf("leading marker numbering = %+v, want old/new 10", marks[0].Line)
	}
	if marks[0].Line.Text != "@@ -10,5 +10,5 @@ func first() {" {
		t.Fatalf("leading marker text = %q, want the verbatim hunk header", marks[0].Line.Text)
	}
	if marks[1].Line.OldLine != 80 || marks[1].Line.NewLine != 80 {
		t.Fatalf("second marker numbering = %+v, want old/new 80", marks[1].Line)
	}
	// The marker must sit exactly at the seam: right after the first hunk's last
	// line (new 14) and right before the second hunk's first line (new 80).
	before, after := lines[marks[1].At-1], lines[marks[1].At+1]
	if before.NewLine != 14 || after.NewLine != 80 {
		t.Fatalf("second marker seam = %+v ... %+v, want new 14 then new 80", before, after)
	}
	// Adjacent lines inside a hunk must never be separated.
	for i, l := range lines {
		if l.Kind != KindHunk {
			continue
		}
		if i > 0 && lines[i-1].Kind == KindHunk {
			t.Fatalf("two markers in a row at %d", i)
		}
	}
}

func TestAllLinesNoMarkerWhenNothingIsSkipped(t *testing.T) {
	cases := []struct {
		name string
		diff string
	}{
		{
			// Single hunk starting at line 1: nothing before it, nothing after.
			name: "single hunk from line 1",
			diff: "@@ -1,3 +1,3 @@\n a\n-b\n+B\n c\n",
		},
		{
			name: "new file",
			diff: "diff --git a/new.go b/new.go\nnew file mode 100644\n--- /dev/null\n+++ b/new.go\n@@ -0,0 +1,3 @@\n+a\n+b\n+c\n",
		},
		{
			name: "deleted file",
			diff: "diff --git a/gone.go b/gone.go\ndeleted file mode 100644\n--- a/gone.go\n+++ /dev/null\n@@ -1,3 +0,0 @@\n-a\n-b\n-c\n",
		},
		{
			// -U0 can emit hunks that continue each other with no skipped line.
			name: "hunks that continue with no gap",
			diff: "@@ -1,1 +1,1 @@\n-a\n+A\n@@ -2,1 +2,1 @@\n-b\n+B\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if marks := hunkMarkers(AllLines(tc.diff)); len(marks) != 0 {
				t.Fatalf("want no hunk marker, got %+v", marks)
			}
		})
	}
}

func TestAllLinesMarksASingleHunkThatStartsMidFile(t *testing.T) {
	// One hunk, but it starts at line 40 — lines 1-39 ARE skipped, so the reader
	// must still see a boundary.
	marks := hunkMarkers(AllLines("@@ -40,3 +40,3 @@ func mid() {\n a\n-b\n+B\n c\n"))
	if len(marks) != 1 {
		t.Fatalf("want 1 leading marker, got %+v", marks)
	}
	if marks[0].Line.NewLine != 40 {
		t.Fatalf("marker numbering = %+v, want new 40", marks[0].Line)
	}
}

func TestHunkForLineBodyCarriesNoMarkers(t *testing.T) {
	// The review-comment path renders one hunk; a boundary marker inside it would
	// be meaningless chrome (and would shift the comment's anchor index).
	lines, found := HunkForLine(gappedDiff, 80)
	if !found {
		t.Fatal("expected a hunk covering new line 80")
	}
	if marks := hunkMarkers(lines); len(marks) != 0 {
		t.Fatalf("single-hunk body must carry no markers, got %+v", marks)
	}
}
