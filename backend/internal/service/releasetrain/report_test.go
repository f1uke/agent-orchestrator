package releasetrain

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
)

func TestReportShowsConfigResourcesBackfillingAndTheLastPass(t *testing.T) {
	gl := &fakeSource{system: "gitlab", creds: true, specs: []rt.ResourceSpec{tagSpec("p:1"), mrSpec("p:1")}}
	gl.fetch = func(spec rt.ResourceSpec, cursor []byte) (Batch, error) {
		if spec.Kind == rt.KindMRs.Name {
			return Batch{Cursor: []byte(`{"page":2}`)}, nil
		}
		return Batch{}, &FetchError{Class: rt.ErrUnavailable, Err: errors.New("gitlab scm: 502 bad gateway")}
	}
	h := newHarness(t, gl)
	p := h.poller()
	h.poll(p)

	r, err := p.Report(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.Config.State != ConfigOK || r.Config.Path != filepath.Join(h.dataDir, ConfigFileName) {
		t.Fatalf("config = %+v", r.Config)
	}
	byScope := map[rt.KindName]ResourceReport{}
	for _, res := range r.Resources {
		byScope[res.Kind] = res
	}
	mrs, tags := byScope[rt.KindMRs.Name], byScope[rt.KindTags.Name]
	if !mrs.Backfilling || mrs.CompleteAt != nil || mrs.Mode != rt.Incremental || mrs.App != "di" {
		t.Fatalf("mrs = %+v", mrs)
	}
	if tags.Backfilling || tags.ErrorClass != rt.ErrUnavailable || tags.ErrorMessage != "gitlab scm: 502 bad gateway" || tags.NextAttemptAt == nil || !tags.LastAttemptAt.Equal(t0) {
		t.Fatalf("tags = %+v", tags)
	}
	if r.LastPass.At == nil || !r.LastPass.At.Equal(t0) || r.LastPass.Requests["gitlab"] != 4 {
		t.Fatalf("last pass = %+v", r.LastPass)
	}
}

func TestReportWithoutConfigSaysHowToCreateIt(t *testing.T) {
	h := newHarness(t)
	if err := os.Remove(filepath.Join(h.dataDir, ConfigFileName)); err != nil {
		t.Fatal(err)
	}
	r, err := h.poller().Report(context.Background())
	if err != nil || r.Config.State != ConfigAbsent || r.Config.Message == "" || len(r.Resources) != 0 {
		t.Fatalf("report = %+v, %v", r, err)
	}
}

func TestWriteExampleConfigWritesOnceAndRefusesToOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), ConfigFileName)
	if err := WriteExampleConfig(path); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != rt.ExampleConfigJSON {
		t.Fatalf("written = %q, %v", b, err)
	}
	if err := os.WriteFile(path, []byte(`{"edited":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteExampleConfig(path); !errors.Is(err, ErrConfigExists) {
		t.Fatalf("second write err = %v, want ErrConfigExists", err)
	}
	if b, _ := os.ReadFile(path); string(b) != `{"edited":true}` {
		t.Fatalf("the human's edit was overwritten: %q", b)
	}
}
