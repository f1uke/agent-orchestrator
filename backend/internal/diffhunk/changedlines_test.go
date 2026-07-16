package diffhunk

import (
	"reflect"
	"testing"
)

func TestChangedLines_Modification(t *testing.T) {
	// A deletion immediately followed by additions is a MODIFIED block; the
	// marker spans the new-side lines that replaced the removed content.
	diff := `diff --git a/foo.go b/foo.go
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
	got := ChangedLines(diff)
	want := []LineChange{{Start: 12, End: 13, Kind: ChangeModified}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ChangedLines = %+v, want %+v", got, want)
	}
}

func TestChangedLines_PureAddition(t *testing.T) {
	// A block of only additions is ADDED, spanning the new-side lines.
	diff := `@@ -5,2 +5,4 @@
 a
+b
+c
 d
`
	got := ChangedLines(diff)
	want := []LineChange{{Start: 6, End: 7, Kind: ChangeAdded}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ChangedLines = %+v, want %+v", got, want)
	}
}

func TestChangedLines_PureDeletion(t *testing.T) {
	// A block of only deletions is a zero-height REMOVED marker anchored at the
	// new-side line that now sits where the removed content was.
	diff := `@@ -5,4 +5,2 @@
 a
-b
-c
 d
`
	got := ChangedLines(diff)
	want := []LineChange{{Start: 6, End: 6, Kind: ChangeRemoved}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ChangedLines = %+v, want %+v", got, want)
	}
}

func TestChangedLines_TrailingDeletionAtEOF(t *testing.T) {
	// A deletion at end-of-file anchors the marker at len(newLines)+1.
	diff := `@@ -1,3 +1,2 @@
 a
 b
-c
`
	got := ChangedLines(diff)
	want := []LineChange{{Start: 3, End: 3, Kind: ChangeRemoved}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ChangedLines = %+v, want %+v", got, want)
	}
}

func TestChangedLines_MultipleHunks(t *testing.T) {
	diff := `@@ -1,3 +1,4 @@
 a
+x
 b
 c
@@ -10,3 +11,2 @@
 j
-k
 l
`
	got := ChangedLines(diff)
	want := []LineChange{
		{Start: 2, End: 2, Kind: ChangeAdded},
		{Start: 12, End: 12, Kind: ChangeRemoved},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ChangedLines = %+v, want %+v", got, want)
	}
}

func TestChangedLines_Empty(t *testing.T) {
	if got := ChangedLines(""); len(got) != 0 {
		t.Fatalf("ChangedLines(\"\") = %+v, want empty", got)
	}
	if got := ChangedLines("diff --git a/x b/x\nindex 1..2\n"); len(got) != 0 {
		t.Fatalf("ChangedLines(no hunks) = %+v, want empty", got)
	}
}
