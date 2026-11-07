package transportsettings

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
	if got := st.WorkerTransport(); got != domain.TransportChat {
		t.Fatalf("worker default = %q, want chat", got)
	}
	if got := st.OrchestratorTransport(); got != domain.TransportChat {
		t.Fatalf("orchestrator default = %q, want chat", got)
	}
}

func TestSetWorker_PersistsAcrossAStoreOverTheSameDir(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStore(dir)
	if err := st.SetWorker(domain.TransportTerminal); err != nil {
		t.Fatal(err)
	}
	if got := st.WorkerTransport(); got != domain.TransportTerminal {
		t.Fatalf("in-memory = %q, want terminal", got)
	}
	reopened, _ := NewStore(dir)
	if got := reopened.WorkerTransport(); got != domain.TransportTerminal {
		t.Fatalf("after reopening the data dir = %q, want terminal", got)
	}
}

func TestSettingOneKindKeepsTheOther(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStore(dir)
	if err := st.SetWorker(domain.TransportTerminal); err != nil {
		t.Fatal(err)
	}
	if err := st.SetOrchestrator(domain.TransportChat); err != nil {
		t.Fatal(err)
	}
	if got := st.WorkerTransport(); got != domain.TransportTerminal {
		t.Fatalf("setting the orchestrator changed the worker to %q", got)
	}

	if err := st.SetOrchestrator(domain.TransportTerminal); err != nil {
		t.Fatal(err)
	}
	if err := st.SetWorker(domain.TransportChat); err != nil {
		t.Fatal(err)
	}
	reopened, _ := NewStore(dir)
	if got := reopened.OrchestratorTransport(); got != domain.TransportTerminal {
		t.Fatalf("setting the worker changed the stored orchestrator to %q", got)
	}
	if got := reopened.WorkerTransport(); got != domain.TransportChat {
		t.Fatalf("stored worker = %q, want chat", got)
	}
}

func TestSet_RefusesAValueThatIsNotATransportAndKeepsTheOldOne(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStore(dir)
	if err := st.SetWorker(domain.TransportTerminal); err != nil {
		t.Fatal(err)
	}
	if err := st.SetOrchestrator(domain.TransportTerminal); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []domain.Transport{"", "tui", "CHAT"} {
		if err := st.SetWorker(bad); err == nil {
			t.Fatalf("SetWorker(%q) = nil, want an error", bad)
		}
		if err := st.SetOrchestrator(bad); err == nil {
			t.Fatalf("SetOrchestrator(%q) = nil, want an error", bad)
		}
	}
	reopened, _ := NewStore(dir)
	if got := reopened.WorkerTransport(); got != domain.TransportTerminal {
		t.Fatalf("a refused SetWorker changed the stored value to %q", got)
	}
	if got := reopened.OrchestratorTransport(); got != domain.TransportTerminal {
		t.Fatalf("a refused SetOrchestrator changed the stored value to %q", got)
	}
}

func TestNewStore_FileFromBeforeOrchestratorsHadASettingKeepsTheWorkerValue(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte(`{"workerTransport":"terminal"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.WorkerTransport(); got != domain.TransportTerminal {
		t.Fatalf("worker = %q, want the stored terminal", got)
	}
	if got := st.OrchestratorTransport(); got != domain.TransportChat {
		t.Fatalf("orchestrator = %q, want the chat default for a file with no orchestratorTransport", got)
	}
}

func TestNewStore_CorruptOrInvalidFileDegradesToTheDefault(t *testing.T) {
	for name, body := range map[string]string{
		"corrupt":              "{not json",
		"invalid worker":       `{"workerTransport":"tui"}`,
		"empty worker":         `{"workerTransport":""}`,
		"invalid orchestrator": `{"workerTransport":"terminal","orchestratorTransport":"tui"}`,
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
			if st.WorkerTransport() != domain.TransportChat || st.OrchestratorTransport() != domain.TransportChat {
				t.Fatalf("%s file gave %+v, want the chat defaults", name, st.Get())
			}
		})
	}
}

func TestNewStore_EmptyDirErrors(t *testing.T) {
	if _, err := NewStore(""); err == nil {
		t.Fatal("want error for empty dir")
	}
}
