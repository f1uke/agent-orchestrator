package gitlab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestFetchFailedCheckLogTail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/jobs/11/trace") {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		lines := make([]string, 0, 30)
		for i := 1; i <= 30; i++ {
			lines = append(lines, "line"+strconv.Itoa(i))
		}
		_, _ = w.Write([]byte(strings.Join(lines, "\n")))
	}))
	defer srv.Close()
	p := newTestProvider(t, srv.URL)
	tail, err := p.FetchFailedCheckLogTail(context.Background(), ports.SCMRepo{Repo: "group/proj"}, ports.SCMCheckObservation{ProviderID: "11"})
	if err != nil {
		t.Fatalf("log tail: %v", err)
	}
	if strings.Contains(tail, "line10") || !strings.Contains(tail, "line30") || !strings.Contains(tail, "line11") {
		t.Fatalf("tail should be last 20 lines: %q", tail)
	}
}

func TestFetchFailedCheckLogTailEmptyProviderID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request to %s; empty ProviderID should not call out", r.URL.Path)
	}))
	defer srv.Close()
	p := newTestProvider(t, srv.URL)
	tail, err := p.FetchFailedCheckLogTail(context.Background(), ports.SCMRepo{Repo: "group/proj"}, ports.SCMCheckObservation{})
	if err != nil {
		t.Fatalf("log tail: %v", err)
	}
	if tail != "" {
		t.Fatalf("expected empty tail, got %q", tail)
	}
}
