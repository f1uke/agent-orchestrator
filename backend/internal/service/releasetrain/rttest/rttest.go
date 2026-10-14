// Package rttest holds httptest fakes of GitLab and Jira for the release-train tests.
package rttest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"
)

// Host is an httptest server that answers canned JSON by route key and records every request.
type Host struct {
	*httptest.Server
	mu       sync.Mutex
	bodies   map[string]string
	failures map[string]failure
	mrs      map[string][]MR
	every    map[string]bool
	requests []string
	key      func(*http.Request) string
}

type failure struct {
	status int
	header map[string]string
	after  int
}

func newHost(t *testing.T, key func(*http.Request) string) *Host {
	t.Helper()
	h := &Host{bodies: map[string]string{}, failures: map[string]failure{}, mrs: map[string][]MR{}, every: map[string]bool{}, key: key}
	h.Server = httptest.NewServer(http.HandlerFunc(h.serve))
	t.Cleanup(h.Close)
	return h
}

// Set answers route key with body.
func (h *Host) Set(key, body string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.bodies[key] = body
}

// SetEveryPage answers route key with body on every page, so a paged read never ends.
func (h *Host) SetEveryPage(key, body string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.bodies[key] = body
	h.every[key] = true
}

// Fail answers route key with status and headers until Heal.
func (h *Host) Fail(key string, status int, header map[string]string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.failures[key] = failure{status: status, header: header}
}

// FailAfter lets n requests to route key through, then fails the rest like Fail.
func (h *Host) FailAfter(key string, n, status int, header map[string]string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.failures[key] = failure{status: status, header: header, after: n}
}

// Heal removes every forced failure.
func (h *Host) Heal() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.failures = map[string]failure{}
}

// TakeRequests returns "METHOD path?query" of every request since the last call.
func (h *Host) TakeRequests() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := h.requests
	h.requests = nil
	return out
}

func (h *Host) serve(w http.ResponseWriter, r *http.Request) {
	key := h.key(r)
	h.mu.Lock()
	h.requests = append(h.requests, r.Method+" "+r.URL.RequestURI())
	f, failed := h.failures[key]
	if failed && f.after > 0 {
		f.after--
		h.failures[key] = f
		failed = false
	}
	body, ok := h.bodies[key]
	mrs, isMRList := h.mrs[r.URL.Path]
	every := h.every[key]
	h.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case failed:
		for k, v := range f.header {
			w.Header().Set(k, v)
		}
		w.WriteHeader(f.status)
		_, _ = fmt.Fprintf(w, `{"message":"%d from the fake"}`, f.status)
	case isMRList:
		_, _ = w.Write(mrPage(mrs, r))
	case !ok:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"404 Not Found"}`))
	case !every && r.URL.Query().Get("page") != "" && r.URL.Query().Get("page") != "1":
		_, _ = w.Write([]byte(`[]`))
	default:
		_, _ = w.Write([]byte(body))
	}
}

// NewGitLab returns a fake GitLab REST v4 API whose route keys are paths.
func NewGitLab(t *testing.T) *Host {
	return newHost(t, func(r *http.Request) string { return r.URL.Path })
}

// MR is one merge request on the fake GitLab.
type MR struct {
	IID         int
	Title       string
	Source      string
	Target      string
	State       string
	Author      string
	Username    string
	MilestoneID int
	CreatedAt   time.Time
	UpdatedAt   time.Time
	MergedAt    *time.Time
	WebURL      string
}

// SetMRs serves project's MR list as GitLab pages it: updated_after inclusive, updated_at ascending.
func (h *Host) SetMRs(project int, mrs []MR) {
	h.mu.Lock()
	defer h.mu.Unlock()
	sorted := append([]MR(nil), mrs...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if !sorted[i].UpdatedAt.Equal(sorted[j].UpdatedAt) {
			return sorted[i].UpdatedAt.Before(sorted[j].UpdatedAt)
		}
		return sorted[i].IID < sorted[j].IID
	})
	h.mrs[fmt.Sprintf("/projects/%d/merge_requests", project)] = sorted
}

func mrPage(mrs []MR, r *http.Request) []byte {
	q := r.URL.Query()
	var after time.Time
	if s := q.Get("updated_after"); s != "" {
		after, _ = time.Parse(time.RFC3339, s)
	}
	perPage, _ := strconv.Atoi(q.Get("per_page"))
	page, _ := strconv.Atoi(q.Get("page"))
	if perPage < 1 {
		perPage = 20
	}
	if page < 1 {
		page = 1
	}
	var match []MR
	for _, m := range mrs {
		if !m.UpdatedAt.Before(after) {
			match = append(match, m)
		}
	}
	from := min((page-1)*perPage, len(match))
	to := min(from+perPage, len(match))
	rows := make([]map[string]any, 0, to-from)
	for _, m := range match[from:to] {
		row := map[string]any{
			"iid": m.IID, "title": m.Title, "state": m.State, "source_branch": m.Source, "target_branch": m.Target,
			"author":     map[string]string{"username": m.Username, "name": m.Author},
			"created_at": m.CreatedAt, "updated_at": m.UpdatedAt, "merged_at": m.MergedAt, "web_url": m.WebURL,
		}
		if m.MilestoneID != 0 {
			row["milestone"] = map[string]int{"id": m.MilestoneID}
		}
		rows = append(rows, row)
	}
	b, _ := json.Marshal(rows)
	return b
}

// NewJira returns a fake Atlassian site serving Jira and Confluence. Route keys
// are paths, except Jira search ("/rest/api/3/search/jql?jql=<jql>"), user
// ("/rest/api/3/user?accountId=<id>") and Confluence search
// ("/wiki/rest/api/content/search?cql=<cql>", or "...?cursor=<cursor>" for a next link).
func NewJira(t *testing.T) *Host {
	return newHost(t, func(r *http.Request) string {
		q := r.URL.Query()
		switch r.URL.Path {
		case "/rest/api/3/search/jql":
			return r.URL.Path + "?jql=" + q.Get("jql")
		case "/rest/api/3/user":
			return r.URL.Path + "?accountId=" + q.Get("accountId")
		case "/wiki/rest/api/content/search":
			if c := q.Get("cursor"); c != "" {
				return r.URL.Path + "?cursor=" + c
			}
			return r.URL.Path + "?cql=" + q.Get("cql")
		}
		return r.URL.Path
	})
}
