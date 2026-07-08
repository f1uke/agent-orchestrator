package promptoverrides

import (
	"os"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/prompts"
)

func TestNewStore_AbsentFile_NoOverrides(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Get().Base) != 0 {
		t.Fatalf("want no overrides, got %+v", st.Get())
	}
}

func TestSetBase_PersistsAndReloads(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStore(dir)
	if err := st.SetBase(prompts.KindWorker, "custom worker"); err != nil {
		t.Fatal(err)
	}
	if got := st.Get().Base[prompts.KindWorker]; got != "custom worker" {
		t.Fatalf("in-memory = %q", got)
	}
	st2, _ := NewStore(dir)
	if got := st2.Get().Base[prompts.KindWorker]; got != "custom worker" {
		t.Fatalf("reloaded = %q", got)
	}
}

func TestClearBase_RemovesOverride(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStore(dir)
	_ = st.SetBase(prompts.KindOrchestrator, "x")
	if err := st.ClearBase(prompts.KindOrchestrator); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.Get().Base[prompts.KindOrchestrator]; ok {
		t.Fatal("override should be gone")
	}
}

func TestSetBase_UnknownKindRejected(t *testing.T) {
	st, _ := NewStore(t.TempDir())
	if err := st.SetBase(prompts.Kind("bogus"), "x"); err == nil {
		t.Fatal("want error for unknown kind")
	}
}

func TestGet_ReturnsCopy(t *testing.T) {
	st, _ := NewStore(t.TempDir())
	_ = st.SetBase(prompts.KindWorker, "a")
	got := st.Get()
	got.Base[prompts.KindWorker] = "mutated"
	if st.Get().Base[prompts.KindWorker] != "a" {
		t.Fatal("Get must return a copy callers cannot mutate")
	}
}

// TestSetBase_DiskWriteFailure_LeavesInMemoryStateUnchanged proves the store
// persists before mutating in-memory state: if the disk write fails, Get()
// must still reflect the value that was on disk before the failed call.
func TestSetBase_DiskWriteFailure_LeavesInMemoryStateUnchanged(t *testing.T) {
	dir := t.TempDir()
	st, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetBase(prompts.KindWorker, "first value"); err != nil {
		t.Fatal(err)
	}

	// Make the directory unwritable so the temp-file write inside SetBase
	// fails. Restore permissions in cleanup so t.TempDir() can clean up.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if err := st.SetBase(prompts.KindWorker, "second value"); err == nil {
		t.Fatal("want error when disk write fails")
	}
	if got := st.Get().Base[prompts.KindWorker]; got != "first value" {
		t.Fatalf("in-memory state changed despite failed persist: got %q, want %q", got, "first value")
	}

	if err := st.ClearBase(prompts.KindWorker); err == nil {
		t.Fatal("want error when disk write fails")
	}
	if got, ok := st.Get().Base[prompts.KindWorker]; !ok || got != "first value" {
		t.Fatalf("in-memory state changed despite failed persist: got %q, ok=%v", got, ok)
	}
}
