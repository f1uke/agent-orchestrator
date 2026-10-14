package releasetrain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
)

func writeConfig(t *testing.T, path, body string, mtime time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestConfigLoaderAbsentFileMeansNotConfigured(t *testing.T) {
	l := NewConfigLoader(filepath.Join(t.TempDir(), "release-train.json"))
	st := l.Load()
	if st.State != ConfigAbsent || st.Config != nil || !strings.Contains(st.Message, "ao train config init") {
		t.Fatalf("status = %+v", st)
	}
}

func TestConfigLoaderKeepsTheLastGoodConfigWhenAnEditFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release-train.json")
	t0 := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	writeConfig(t, path, rt.ExampleConfigJSON, t0)
	l := NewConfigLoader(path)

	st := l.Load()
	if st.State != ConfigOK || st.Config == nil || st.Loc.String() != "Asia/Bangkok" || st.Config.Jira.Project != "DEMO" {
		t.Fatalf("first load = %+v", st)
	}

	writeConfig(t, path, strings.Replace(rt.ExampleConfigJSON, "Asia/Bangkok", "Mars/Olympus", 1), t0.Add(time.Second))
	st = l.Load()
	if st.State != ConfigInvalid || !strings.Contains(st.Message, "timezone") {
		t.Fatalf("after a bad edit = %+v", st)
	}
	if st.Config == nil || st.Config.Timezone != "Asia/Bangkok" || st.Loc.String() != "Asia/Bangkok" {
		t.Fatalf("a bad edit dropped the last good config: %+v", st.Config)
	}

	writeConfig(t, path, "{not json", t0.Add(2*time.Second))
	if st = l.Load(); st.State != ConfigInvalid || st.Config == nil {
		t.Fatalf("broken JSON = %+v", st)
	}
}

func TestConfigLoaderRereadsOnlyWhenTheMtimeChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release-train.json")
	t0 := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	writeConfig(t, path, rt.ExampleConfigJSON, t0)
	l := NewConfigLoader(path)
	if st := l.Load(); st.Config.Jira.Project != "DEMO" {
		t.Fatalf("first load = %+v", st)
	}

	writeConfig(t, path, strings.Replace(rt.ExampleConfigJSON, `"project": "DEMO"`, `"project": "SAMPLE"`, 1), t0)
	if st := l.Load(); st.Config.Jira.Project != "DEMO" {
		t.Fatalf("re-read with the same mtime: %s", st.Config.Jira.Project)
	}
	if err := os.Chtimes(path, t0.Add(time.Second), t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if st := l.Load(); st.Config.Jira.Project != "SAMPLE" {
		t.Fatalf("no re-read after the mtime moved: %s", st.Config.Jira.Project)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if st := l.Load(); st.State != ConfigAbsent || st.Config != nil {
		t.Fatalf("after delete = %+v", st)
	}
}
