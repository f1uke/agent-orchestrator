package workertransport

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestNewStore_AbsentFileIsTheBuiltInDefault(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Transport(); got != domain.TransportChat {
		t.Fatalf("default = %q, want chat", got)
	}
}

func TestSet_PersistsAcrossAStoreOverTheSameDir(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStore(dir)
	if err := st.Set(Settings{WorkerTransport: domain.TransportTerminal}); err != nil {
		t.Fatal(err)
	}
	if got := st.Transport(); got != domain.TransportTerminal {
		t.Fatalf("in-memory = %q, want terminal", got)
	}
	reopened, _ := NewStore(dir)
	if got := reopened.Transport(); got != domain.TransportTerminal {
		t.Fatalf("after reopening the data dir = %q, want terminal", got)
	}
}

func TestSet_RefusesAValueThatIsNotATransportAndKeepsTheOldOne(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStore(dir)
	if err := st.Set(Settings{WorkerTransport: domain.TransportTerminal}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []domain.Transport{"", "tui", "CHAT"} {
		if err := st.Set(Settings{WorkerTransport: bad}); err == nil {
			t.Fatalf("Set(%q) = nil, want an error", bad)
		}
	}
	reopened, _ := NewStore(dir)
	if got := reopened.Transport(); got != domain.TransportTerminal {
		t.Fatalf("a refused Set changed the stored value to %q", got)
	}
}

func TestNewStore_CorruptOrInvalidFileDegradesToTheDefault(t *testing.T) {
	for name, body := range map[string]string{
		"corrupt": "{not json",
		"invalid": `{"workerTransport":"tui"}`,
		"empty":   `{"workerTransport":""}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, fileName), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			st, err := NewStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			if got := st.Transport(); got != domain.TransportChat {
				t.Fatalf("%s file gave %q, want the chat default", name, got)
			}
		})
	}
}

func TestNewStore_EmptyDirErrors(t *testing.T) {
	if _, err := NewStore(""); err == nil {
		t.Fatal("want error for empty dir")
	}
}
