package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestReplyToThread_PostsNoteAndParses(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/grp%2Fproj/merge_requests/7/discussions/disc1/notes", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		var decoded map[string]string
		if err := json.NewDecoder(r.Body).Decode(&decoded); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if decoded["body"] != "thanks" {
			t.Fatalf("body = %#v, want {body: thanks}", decoded)
		}
		_, _ = w.Write([]byte(`{"id":42,"body":"thanks","author":{"username":"me"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	p := newTestProvider(t, srv.URL)

	ref := ports.SCMPRRef{Repo: ports.SCMRepo{Provider: "gitlab", Repo: "grp/proj"}, Number: 7}
	obs, err := p.ReplyToThread(context.Background(), ref, "disc1", "thanks")
	if err != nil {
		t.Fatalf("ReplyToThread: %v", err)
	}
	if obs.ID != "42" {
		t.Errorf("ID = %q, want 42", obs.ID)
	}
	if obs.Author != "me" {
		t.Errorf("Author = %q, want me", obs.Author)
	}
	if obs.Body != "thanks" {
		t.Errorf("Body = %q, want thanks", obs.Body)
	}
	if obs.IsBot {
		t.Errorf("IsBot = true, want false")
	}
}

func TestResolveThread_PutsResolved(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/grp%2Fproj/merge_requests/7/discussions/disc1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("method = %s, want PUT", r.Method)
		}
		if r.URL.Query().Get("resolved") != "true" {
			t.Fatalf("resolved query = %q, want true", r.URL.Query().Get("resolved"))
		}
		_, _ = w.Write([]byte(`{"id":"disc1"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	p := newTestProvider(t, srv.URL)

	ref := ports.SCMPRRef{Repo: ports.SCMRepo{Provider: "gitlab", Repo: "grp/proj"}, Number: 7}
	if err := p.ResolveThread(context.Background(), ref, "disc1"); err != nil {
		t.Fatalf("ResolveThread: %v", err)
	}
}

func TestReplyToThread_AuthFailedMapsToForbidden(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/grp%2Fproj/merge_requests/7/discussions/disc1/notes", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"401 Unauthorized"}`, http.StatusUnauthorized)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	p := newTestProvider(t, srv.URL)

	ref := ports.SCMPRRef{Repo: ports.SCMRepo{Provider: "gitlab", Repo: "grp/proj"}, Number: 7}
	_, err := p.ReplyToThread(context.Background(), ref, "disc1", "thanks")
	if !errors.Is(err, ports.ErrSCMForbidden) {
		t.Fatalf("err = %v, want wraps ports.ErrSCMForbidden", err)
	}
}

func TestResolveThread_NotFoundMapsToSCMNotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/grp%2Fproj/merge_requests/7/discussions/disc1", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"404 Discussion Not Found"}`, http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	p := newTestProvider(t, srv.URL)

	ref := ports.SCMPRRef{Repo: ports.SCMRepo{Provider: "gitlab", Repo: "grp/proj"}, Number: 7}
	err := p.ResolveThread(context.Background(), ref, "disc1")
	if !errors.Is(err, ports.ErrSCMNotFound) {
		t.Fatalf("err = %v, want wraps ports.ErrSCMNotFound", err)
	}
}
