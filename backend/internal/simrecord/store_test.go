package simrecord_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/simflow"
	"github.com/aoagents/agent-orchestrator/backend/internal/simrecord"
)

const sessionID = "project-1"

func recordedAt(sec int) time.Time {
	return time.Date(2026, 8, 18, 4, 57, sec, 711_000_000, time.UTC)
}

// emitted builds a real flow so the counts a listing reports come from the
// emitter, never from a string a test made up to agree with itself.
func emitted(t *testing.T, steps []simflow.Step) string {
	t.Helper()
	body, err := simflow.Emit(steps, simflow.EmitOptions{Device: "d", Runtime: "r", RecordedAt: "t"})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	return body
}

func clean(seq int64) simflow.Step {
	return simflow.Step{Seq: seq, Kind: simflow.StepTap, Plain: "Home",
		Choice: simflow.Choice{Rung: simflow.RungText, Text: "Home", Ambiguity: 1}}
}

func guessed(seq int64) simflow.Step {
	return simflow.Step{Seq: seq, Kind: simflow.StepTap, Plain: "Buy",
		Choice: simflow.Choice{Rung: simflow.RungTextIndex, Text: "Buy", Index: 1, Ambiguity: 3}}
}

func TestWriteAndList_ReportsWhatTheFlowSaysAboutItself(t *testing.T) {
	dataDir := t.TempDir()

	flow, err := simrecord.Write(dataDir, sessionID, "Login to Portfolio", recordedAt(22),
		emitted(t, []simflow.Step{clean(1), guessed(2), clean(3)}))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if flow.FileName != "login-to-portfolio-20260818-045722.711Z.yaml" {
		t.Errorf("FileName = %q", flow.FileName)
	}
	if !filepath.IsAbs(flow.Path) {
		t.Errorf("Path = %q, want an absolute path a worker can act on", flow.Path)
	}
	if got, want := flow.Path, filepath.Join(simrecord.FlowsDir(dataDir, sessionID), flow.FileName); got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
	if flow.Steps != 3 || flow.Review != 1 || !flow.Known {
		t.Errorf("counts = %d steps / %d review / known=%v, want 3 / 1 / true", flow.Steps, flow.Review, flow.Known)
	}

	listed, err := simrecord.List(dataDir, sessionID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("List returned %d flows, want 1", len(listed))
	}
	if listed[0] != flow {
		t.Errorf("List = %+v, want %+v", listed[0], flow)
	}
	if listed[0].Name != "login-to-portfolio" {
		t.Errorf("Name = %q, want the name read back out of the file name", listed[0].Name)
	}
}

// The review count is the number a human has to act on, so a listing that
// disagreed with the flow's own banner would send them looking in the wrong
// file. It is read from the flow, not recomputed.
func TestList_ReviewCountMatchesTheFlowHeader(t *testing.T) {
	dataDir := t.TempDir()
	body := emitted(t, []simflow.Step{clean(1), guessed(2), guessed(3), clean(4)})
	if _, err := simrecord.Write(dataDir, sessionID, "", recordedAt(22), body); err != nil {
		t.Fatalf("Write: %v", err)
	}

	listed, err := simrecord.List(dataDir, sessionID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if listed[0].Review != 2 || listed[0].Steps != 4 {
		t.Fatalf("listed %d steps / %d review, want 4 / 2", listed[0].Steps, listed[0].Review)
	}
	if !strings.Contains(body, "REVIEW REQUIRED: 2 of 4 steps") {
		t.Errorf("the flow's own banner must say the same thing:\n%s", body)
	}
}

// Newest first, because the recording a human wants is nearly always the one
// they just made - they record the same path several times before it is right.
func TestList_NewestFirst(t *testing.T) {
	dataDir := t.TempDir()
	for _, sec := range []int{22, 44, 33} {
		if _, err := simrecord.Write(dataDir, sessionID, "attempt", recordedAt(sec), emitted(t, nil)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	listed, err := simrecord.List(dataDir, sessionID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var order []int
	for _, f := range listed {
		order = append(order, f.RecordedAt.Second())
	}
	if len(order) != 3 || order[0] != 44 || order[1] != 33 || order[2] != 22 {
		t.Errorf("order = %v, want newest first", order)
	}
}

// Flows recorded before flows and screenshots were separated are still beside
// the screenshots. Nothing was moved, so a path somebody already pasted into a
// message still resolves - and the list must still find them.
func TestList_ReadsFlowsLeftBesideTheScreenshots(t *testing.T) {
	dataDir := t.TempDir()
	legacyDir := simrecord.SessionDir(dataDir, sessionID)
	if err := os.MkdirAll(legacyDir, 0o750); err != nil {
		t.Fatal(err)
	}
	legacy := "20260818-045722.711Z-97882B61-7B22-45C1-9DF8-0E52913C87DA.yaml"
	if err := os.WriteFile(filepath.Join(legacyDir, legacy), []byte(emitted(t, []simflow.Step{clean(1)})), 0o600); err != nil {
		t.Fatal(err)
	}
	// A screenshot in the same directory is not a flow.
	if err := os.WriteFile(filepath.Join(legacyDir, "20260818-045800.000Z-x.png"), []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := simrecord.Write(dataDir, sessionID, "new one", recordedAt(44), emitted(t, nil)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	listed, err := simrecord.List(dataDir, sessionID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("List returned %d flows, want the new one and the legacy one", len(listed))
	}
	var found *simrecord.Flow
	for i := range listed {
		if listed[i].FileName == legacy {
			found = &listed[i]
		}
	}
	if found == nil {
		t.Fatalf("the legacy flow is missing from %+v", listed)
	}
	if found.Name != "" {
		t.Errorf("Name = %q: the udid in an old file name is not a name", found.Name)
	}
	if !found.TimeFromFileName || found.RecordedAt.Second() != 22 {
		t.Errorf("the old file name still carries when it was recorded, got %s", found.RecordedAt)
	}
}

// A flow written before flows stated their counts must read as unmeasured. A
// list showing "0 steps" for a flow with twelve of them is a lie a human would
// act on.
func TestList_FlowWithoutCountsIsUnknownNotZero(t *testing.T) {
	dataDir := t.TempDir()
	dir := simrecord.FlowsDir(dataDir, sessionID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	body := "appId: ${APP_ID}\n---\n# recorded by ao sim at t, device d (r)\n- tapOn: \"Home\"\n"
	if err := os.WriteFile(filepath.Join(dir, "old-20260818-045722.711Z.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	listed, err := simrecord.List(dataDir, sessionID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if listed[0].Known {
		t.Errorf("counts must read as unknown, got %d steps / %d review", listed[0].Steps, listed[0].Review)
	}
}

func TestList_EmptyWhenNothingHasBeenRecorded(t *testing.T) {
	listed, err := simrecord.List(t.TempDir(), sessionID)
	if err != nil {
		t.Fatalf("a session that has recorded nothing is not an error: %v", err)
	}
	if len(listed) != 0 {
		t.Errorf("List = %+v, want empty", listed)
	}
}

func TestDelete_RemovesExactlyTheOneNamed(t *testing.T) {
	dataDir := t.TempDir()
	keep, err := simrecord.Write(dataDir, sessionID, "keep me", recordedAt(22), emitted(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	drop, err := simrecord.Write(dataDir, sessionID, "drop me", recordedAt(33), emitted(t, nil))
	if err != nil {
		t.Fatal(err)
	}

	if err := simrecord.Delete(dataDir, sessionID, drop.FileName); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	listed, err := simrecord.List(dataDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].FileName != keep.FileName {
		t.Errorf("after deleting one, list = %+v, want only %q", listed, keep.FileName)
	}
	if _, err := os.Stat(drop.Path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the deleted flow is still on disk: %v", err)
	}
}

// A recording is a path somebody played through by hand and cannot regenerate.
// A name that is not a bare file name is refused rather than cleaned up: a
// caller asking to delete "../.." is not making a typo.
func TestDelete_RefusesAnythingThatIsNotABareFlowName(t *testing.T) {
	dataDir := t.TempDir()
	if _, err := simrecord.Write(dataDir, sessionID, "keep me", recordedAt(22), emitted(t, nil)); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(dataDir, "outside.yaml")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{
		"", ".", "..", "../outside.yaml", "../../outside.yaml",
		filepath.Join("..", "outside.yaml"), `..\outside.yaml`,
		"flows", "shot.png", "/etc/passwd",
		// Drive-relative on Windows, an ordinary file name on a mac. The
		// daemon has a `native (windows-latest)` job, so the refusal has to be
		// the same on both rather than whatever filepath.Base says today.
		`C:keep-me.yaml`, `C:\Windows\x.yaml`,
	} {
		if err := simrecord.Delete(dataDir, sessionID, name); err == nil {
			t.Errorf("Delete(%q) was allowed", name)
		}
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("a file outside the session directory was touched: %v", err)
	}
	listed, err := simrecord.List(dataDir, sessionID)
	if err != nil || len(listed) != 1 {
		t.Errorf("the real flow must be untouched, got %+v (%v)", listed, err)
	}
}

// ⚠ The flows list reads the session directory too, because recordings made
// before flows and screenshots were separated still live there - beside the
// screenshots. So the extension rule is what stands between a delete request
// and somebody's `ao sim shot` capture, and this pins it with a screenshot
// that is really on disk. A mutation check found the previous version of this
// test asserting on a name that did not exist, which passes whether the rule
// is there or not.
func TestDelete_WillNotRemoveAScreenshotThatSharesTheDirectory(t *testing.T) {
	dataDir := t.TempDir()
	sessionDir := simrecord.SessionDir(dataDir, sessionID)
	if err := os.MkdirAll(sessionDir, 0o750); err != nil {
		t.Fatal(err)
	}
	shot := "20260818-045722.711Z-97882B61.png"
	if err := os.WriteFile(filepath.Join(sessionDir, shot), []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := simrecord.Delete(dataDir, sessionID, shot); !errors.Is(err, simrecord.ErrInvalidName) {
		t.Errorf("err = %v, want a refusal naming it as not a flow", err)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, shot)); err != nil {
		t.Errorf("the screenshot was deleted: %v", err)
	}
}

func TestDelete_UnknownFlowSaysSoRatherThanSucceeding(t *testing.T) {
	err := simrecord.Delete(t.TempDir(), sessionID, "never-recorded-20260818-045722.711Z.yaml")
	if !errors.Is(err, simrecord.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// The name is applied after the fact, and the timestamp it keeps is the
// RECORDING's - re-stamping at rename time would shuffle a week-old flow to
// the top of the list as though it had just been captured.
func TestRename_KeepsTheRecordingsOwnTimestamp(t *testing.T) {
	dataDir := t.TempDir()
	before, err := simrecord.Write(dataDir, sessionID, "", recordedAt(22), emitted(t, []simflow.Step{clean(1), guessed(2)}))
	if err != nil {
		t.Fatal(err)
	}

	after, err := simrecord.Rename(dataDir, sessionID, before.FileName, "Login to Portfolio")
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if after.FileName != "login-to-portfolio-20260818-045722.711Z.yaml" {
		t.Errorf("FileName = %q", after.FileName)
	}
	if !after.RecordedAt.Equal(before.RecordedAt) {
		t.Errorf("RecordedAt moved from %s to %s", before.RecordedAt, after.RecordedAt)
	}
	if after.Steps != before.Steps || after.Review != before.Review {
		t.Errorf("renaming changed the counts: %+v then %+v", before, after)
	}
	if _, err := os.Stat(before.Path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the old file is still there: %v", err)
	}
	listed, err := simrecord.List(dataDir, sessionID)
	if err != nil || len(listed) != 1 || listed[0].FileName != after.FileName {
		t.Errorf("after renaming, list = %+v (%v)", listed, err)
	}
}

func TestRename_EmptyNameGoesBackToTheTimestampAlone(t *testing.T) {
	dataDir := t.TempDir()
	named, err := simrecord.Write(dataDir, sessionID, "named", recordedAt(22), emitted(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	after, err := simrecord.Rename(dataDir, sessionID, named.FileName, "   ")
	if err != nil {
		t.Fatalf("clearing a name must work, not fail: %v", err)
	}
	if after.FileName != "20260818-045722.711Z.yaml" || after.Name != "" {
		t.Errorf("FileName = %q, Name = %q", after.FileName, after.Name)
	}
}

// Renaming a flow that has been sitting beside the screenshots moves it into
// the flows directory, which is where everything recorded from now on lives.
func TestRename_MovesALegacyFlowIntoTheFlowsDirectory(t *testing.T) {
	dataDir := t.TempDir()
	legacyDir := simrecord.SessionDir(dataDir, sessionID)
	if err := os.MkdirAll(legacyDir, 0o750); err != nil {
		t.Fatal(err)
	}
	legacy := "20260818-045722.711Z-97882B61.yaml"
	if err := os.WriteFile(filepath.Join(legacyDir, legacy), []byte(emitted(t, nil)), 0o600); err != nil {
		t.Fatal(err)
	}

	after, err := simrecord.Rename(dataDir, sessionID, legacy, "found it")
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if want := filepath.Join(simrecord.FlowsDir(dataDir, sessionID), "found-it-20260818-045722.711Z.yaml"); after.Path != want {
		t.Errorf("Path = %q, want %q", after.Path, want)
	}
	if _, err := os.Stat(filepath.Join(legacyDir, legacy)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the old file is still there: %v", err)
	}
}

func TestRename_RefusesAnythingThatIsNotABareFlowName(t *testing.T) {
	dataDir := t.TempDir()
	if _, err := simrecord.Rename(dataDir, sessionID, "../outside.yaml", "x"); err == nil {
		t.Error("renaming a path outside the session directory was allowed")
	}
}

// Flows and screenshots are different directories now, and the layout is
// asserted rather than left to be discovered by a human wondering where a file
// went.
func TestLayout_FlowsAndShotsAreSeparate(t *testing.T) {
	dataDir := filepath.Join(string(filepath.Separator), "data")
	session := simrecord.SessionDir(dataDir, sessionID)
	flows := simrecord.FlowsDir(dataDir, sessionID)
	shots := simrecord.ShotsDir(dataDir, sessionID)
	if flows == shots {
		t.Fatal("flows and screenshots must not share a directory")
	}
	if filepath.Dir(flows) != session || filepath.Dir(shots) != session {
		t.Errorf("both live under the session directory: %q, %q, %q", session, flows, shots)
	}
	if !filepath.IsAbs(flows) || !filepath.IsAbs(shots) {
		t.Errorf("paths must stay absolute: %q, %q", flows, shots)
	}
}
