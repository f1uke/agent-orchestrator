package releasetrain

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
)

func TestCountingTransportCountsGETsAndRefusesEverythingElse(t *testing.T) {
	var reached atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached.Add(1) }))
	defer srv.Close()
	client := &http.Client{Transport: CountingTransport{}}
	var used atomic.Int64
	ctx := withRequestCounter(context.Background(), &used)

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		req, _ := http.NewRequestWithContext(ctx, m, srv.URL, strings.NewReader("{}"))
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
		if !errors.Is(err, ErrNotARead) {
			t.Errorf("%s err = %v, want ErrNotARead", m, err)
		}
	}
	if reached.Load() != 1 || used.Load() != 1 {
		t.Fatalf("server saw %d requests, budget counted %d; want only the GET", reached.Load(), used.Load())
	}
}

func TestFetchJobRunsThePlannedJobForTheSpec(t *testing.T) {
	tags := rt.SpecFor(rt.KindTags, rt.ProjectScope(1), "di")
	jobs := []Job{{Spec: tags, Fetch: func(context.Context, []byte) (Batch, error) {
		return SnapshotOf(rt.KindTags, []rt.TagFact{{Project: 1, Name: "1.0.0"}})
	}}}
	b, err := FetchJob(context.Background(), jobs, tags, nil)
	if err != nil || !b.Complete || len(b.Items) != 1 || b.Items[0].Key != "1.0.0" {
		t.Fatalf("batch = %+v, %v", b, err)
	}
	_, err = FetchJob(context.Background(), jobs, rt.SpecFor(rt.KindTags, rt.ProjectScope(2), "da"), nil)
	var fe *FetchError
	if !errors.As(err, &fe) || fe.Class != rt.ErrNotFound {
		t.Fatalf("unplanned spec err = %v", err)
	}
}
