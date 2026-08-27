package controllers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
)

// cancelWatchingService blocks in SearchWorkspace until its context is done and
// then reports why — the only way to OBSERVE, rather than assume, that a client
// walking away reaches the scan.
type cancelWatchingService struct {
	*fakeSessionService
	once    sync.Once
	started chan struct{}
	ended   chan error
}

func (c *cancelWatchingService) SearchWorkspace(
	ctx context.Context, _ domain.SessionID, _ sessionsvc.SearchQuery,
) (sessionsvc.SearchResult, error) {
	c.once.Do(func() { close(c.started) })
	select {
	case <-ctx.Done():
		c.ended <- ctx.Err()
	case <-time.After(5 * time.Second):
		c.ended <- nil
	}
	return sessionsvc.SearchResult{Available: true}, nil
}

// TestSearchWorkspace_ClientGoingAwayCancelsTheScan is the load-bearing test for
// ⌘⇧F's cancellation decision, and it exists because that decision is a
// deliberate departure from a rule this project recorded twice.
//
// #258 and #259 both concluded "never send $/cancelRequest" — about LANGUAGE
// SERVERS, where cancelling discards an in-progress type-check the next request
// must redo. A content search holds no such state, and the measurement is
// lopsided: over a real 6,940-file project a full search costs 792 ms of CPU
// across the scanning pool, while one abandoned at 10 ms costs 1 ms. Typing a
// word behind a debounce starts several searches, so NOT cancelling them spends
// CPU-seconds on answers nobody will read.
//
// All of which is worth nothing if the abort never ARRIVES. The renderer's
// AbortController closes the connection; this asserts the other half — that a
// client disconnect cancels the context the handler hands the service. A handler
// that passed context.Background(), or middleware that detached the request
// context, would leave the whole chain silently inert while every other test in
// this file still passed.
func TestSearchWorkspace_ClientGoingAwayCancelsTheScan(t *testing.T) {
	watcher := &cancelWatchingService{
		fakeSessionService: newFakeSessionService(),
		started:            make(chan struct{}),
		ended:              make(chan error, 1),
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(
		httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Sessions: watcher}, httpd.ControlDeps{}),
	)
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/v1/sessions/ao-1/workspace/search?q=self", nil)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		resp, reqErr := http.DefaultClient.Do(req)
		if reqErr == nil {
			_ = resp.Body.Close()
		}
	}()

	select {
	case <-watcher.started:
	case <-time.After(3 * time.Second):
		t.Fatal("the handler never reached the service")
	}
	// The reader typed another character: the renderer aborts the fetch, which
	// closes the connection.
	cancel()

	select {
	case cause := <-watcher.ended:
		if cause == nil {
			t.Fatal("the scan ran on after the client went away — cancellation never arrived")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the service's context was never cancelled")
	}
}
