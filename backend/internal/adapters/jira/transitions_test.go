package jira

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/atlassian"
)

// staticConfig points the client at a test server with a fixed identity.
func staticConfig(baseURL string) atlassian.ConfigSource {
	return func() (atlassian.Config, error) {
		return atlassian.Config{BaseURL: baseURL, Email: "alex@example.com", Token: "tok-123"}, nil
	}
}

func TestTransitions_ParsesLive(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"transitions":[
			{"id":"11","name":"Start Testing","to":{"name":"In Progress","statusCategory":{"key":"indeterminate","colorName":"yellow"}}},
			{"id":"21","name":"Abandoned","to":{"name":"Abandoned","statusCategory":{"key":"done","colorName":"green"}}}
		]}`)
	}))
	defer srv.Close()

	c := NewClient(WithHTTPDoer(srv.Client().Do), WithConfigSource(staticConfig(srv.URL)))
	ts, err := c.Transitions(context.Background(), "DEMO-101")
	if err != nil {
		t.Fatalf("Transitions: %v", err)
	}
	if gotPath != "/rest/api/3/issue/DEMO-101/transitions" {
		t.Errorf("path = %q", gotPath)
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("alex@example.com:tok-123"))
	if gotAuth != wantAuth {
		t.Errorf("auth = %q, want %q", gotAuth, wantAuth)
	}
	if len(ts) != 2 {
		t.Fatalf("transitions = %+v", ts)
	}
	if ts[0].ID != "11" || ts[0].Name != "Start Testing" || ts[0].To != "In Progress" || ts[0].ToCategory != "indeterminate" {
		t.Errorf("transition[0] = %+v", ts[0])
	}
}

func TestTransitions_BadKeyNeverCallsHTTP(t *testing.T) {
	c := NewClient(WithHTTPDoer(func(*http.Request) (*http.Response, error) {
		t.Fatal("HTTP must not be called for a malformed key")
		return nil, nil
	}), WithConfigSource(staticConfig("http://x")))
	if _, err := c.Transitions(context.Background(), "not a key"); !errors.Is(err, ErrBadKey) {
		t.Errorf("err = %v, want ErrBadKey", err)
	}
}

func TestMove_PostsTransitionID(t *testing.T) {
	var gotMethod, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := NewClient(WithHTTPDoer(srv.Client().Do), WithConfigSource(staticConfig(srv.URL)))
	if err := c.Move(context.Background(), "DEMO-101", "11"); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q", gotMethod)
	}
	// The body carries ONLY the transition id — no comment, no field edit.
	if !strings.Contains(gotBody, `"transition"`) || !strings.Contains(gotBody, `"id":"11"`) {
		t.Errorf("body = %q", gotBody)
	}
	if strings.Contains(gotBody, "comment") || strings.Contains(gotBody, "fields") {
		t.Errorf("move body must not carry comment/fields: %q", gotBody)
	}
}

func TestMove_NonNumericIDNeverCallsHTTP(t *testing.T) {
	c := NewClient(WithHTTPDoer(func(*http.Request) (*http.Response, error) {
		t.Fatal("HTTP must not be called for a non-numeric transition id")
		return nil, nil
	}), WithConfigSource(staticConfig("http://x")))
	if err := c.Move(context.Background(), "DEMO-1", "In Progress"); !errors.Is(err, ErrBadTransition) {
		t.Errorf("err = %v, want ErrBadTransition", err)
	}
}

func TestMove_StatusErrorsMapToSentinels(t *testing.T) {
	cases := []struct {
		code int
		body string
		want error
	}{
		{http.StatusBadRequest, `{"errorMessages":["A required field is missing"]}`, ErrBadTransition},
		{http.StatusUnauthorized, `{"errorMessages":["auth"]}`, ErrAuthFailed},
		{http.StatusForbidden, `{"errorMessages":["forbidden"]}`, ErrAuthFailed},
		{http.StatusNotFound, ``, ErrNotFound},
		{http.StatusInternalServerError, `boom`, ErrUnavailable},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.code)
			_, _ = io.WriteString(w, tc.body)
		}))
		c := NewClient(WithHTTPDoer(srv.Client().Do), WithConfigSource(staticConfig(srv.URL)))
		err := c.Move(context.Background(), "DEMO-1", "11")
		if !errors.Is(err, tc.want) {
			t.Errorf("status %d: err = %v, want %v", tc.code, err, tc.want)
		}
		srv.Close()
	}
}

func TestBadRequestSurfacesJiraMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"errorMessages":["Field 'resolution' is required"]}`)
	}))
	defer srv.Close()
	c := NewClient(WithHTTPDoer(srv.Client().Do), WithConfigSource(staticConfig(srv.URL)))
	err := c.Move(context.Background(), "DEMO-1", "11")
	if err == nil || !strings.Contains(err.Error(), "resolution") {
		t.Errorf("err should surface the Jira validator message, got %v", err)
	}
}
