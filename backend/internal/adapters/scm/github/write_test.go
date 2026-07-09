package github

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	scmobserve "github.com/aoagents/agent-orchestrator/backend/internal/observe/scm"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Compile-time proof that Provider satisfies the shared write-capability
// contract. Kept in the test file (not write.go) so the github adapter does
// not gain a production import edge to internal/observe/scm; runtime
// routing (a later task) type-asserts instead.
var _ scmobserve.ReviewThreadWriter = (*Provider)(nil)

// decodedGraphQLBody is the shape doGraphQL POSTs: {"query": ..., "variables": {...}}.
type decodedGraphQLBody struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

func TestReplyToThread_PostsMutationAndParsesComment(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodPost, "/graphql", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		var decoded decodedGraphQLBody
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if !strings.Contains(decoded.Query, "addPullRequestReviewThreadReply") {
			t.Fatalf("query = %q, want it to contain addPullRequestReviewThreadReply", decoded.Query)
		}
		if decoded.Variables["threadId"] != "PRRT_x" || decoded.Variables["body"] != "looks good" {
			t.Fatalf("variables = %#v, want threadId=PRRT_x body=looks good", decoded.Variables)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"addPullRequestReviewThreadReply": map[string]any{
					"comment": map[string]any{
						"id":   "PRRC_1",
						"body": "looks good",
						"url":  "https://gh/c1",
						"author": map[string]any{
							"login":      "me",
							"__typename": "User",
						},
					},
				},
			},
		})
	})
	p := newProviderForTest(t, f)

	ref := ports.SCMPRRef{Repo: ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "octocat", Name: "hello", Repo: "octocat/hello"}, Number: 42}
	comment, err := p.ReplyToThread(ctx(), ref, "PRRT_x", "looks good")
	if err != nil {
		t.Fatalf("ReplyToThread: %v", err)
	}
	if comment.ID != "PRRC_1" {
		t.Errorf("ID = %q, want PRRC_1", comment.ID)
	}
	if comment.Author != "me" {
		t.Errorf("Author = %q, want me", comment.Author)
	}
	if comment.Body != "looks good" {
		t.Errorf("Body = %q, want %q", comment.Body, "looks good")
	}
	if comment.URL != "https://gh/c1" {
		t.Errorf("URL = %q, want https://gh/c1", comment.URL)
	}
	if comment.IsBot {
		t.Errorf("IsBot = true, want false")
	}
}

func TestResolveThread_PostsMutation(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodPost, "/graphql", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		var decoded decodedGraphQLBody
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if !strings.Contains(decoded.Query, "resolveReviewThread") {
			t.Fatalf("query = %q, want it to contain resolveReviewThread", decoded.Query)
		}
		if decoded.Variables["threadId"] != "PRRT_x" {
			t.Fatalf("variables = %#v, want threadId=PRRT_x", decoded.Variables)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"resolveReviewThread": map[string]any{
					"thread": map[string]any{
						"id":         "PRRT_x",
						"isResolved": true,
					},
				},
			},
		})
	})
	p := newProviderForTest(t, f)

	ref := ports.SCMPRRef{Repo: ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "octocat", Name: "hello", Repo: "octocat/hello"}, Number: 42}
	if err := p.ResolveThread(ctx(), ref, "PRRT_x"); err != nil {
		t.Fatalf("ResolveThread: %v", err)
	}
}

func TestReplyToThread_AuthFailedMapsToForbidden(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodPost, "/graphql", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
	})
	p := newProviderForTest(t, f)

	ref := ports.SCMPRRef{Repo: ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "octocat", Name: "hello", Repo: "octocat/hello"}, Number: 42}
	_, err := p.ReplyToThread(ctx(), ref, "PRRT_x", "looks good")
	if !errors.Is(err, ports.ErrSCMForbidden) {
		t.Fatalf("err = %v, want wraps ports.ErrSCMForbidden", err)
	}
}

func TestResolveThread_NotFoundMapsToSCMNotFound(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodPost, "/graphql", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"errors": []map[string]any{
				{"message": "Could not resolve to a node"},
			},
		})
	})
	p := newProviderForTest(t, f)

	ref := ports.SCMPRRef{Repo: ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "octocat", Name: "hello", Repo: "octocat/hello"}, Number: 42}
	err := p.ResolveThread(ctx(), ref, "PRRT_x")
	if !errors.Is(err, ports.ErrSCMNotFound) {
		t.Fatalf("err = %v, want wraps ports.ErrSCMNotFound", err)
	}
}
