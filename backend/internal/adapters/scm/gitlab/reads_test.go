package gitlab

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

const sentinelToken = "fake-example-token"

func readsClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewClient(ClientOptions{APIBase: srv.URL + "/api/v4", Token: StaticTokenSource(sentinelToken)})
}

type listCase struct {
	name     string
	path     string
	row      func(i int) string
	call     func(c *Client, maxPages int) (int, error)
	wantKeys map[string]string
}

func listCases() []listCase {
	return []listCase{
		{
			name: "ListMilestones",
			path: "/api/v4/projects/42/milestones",
			row: func(i int) string {
				return fmt.Sprintf(`{"id":%d,"iid":%d,"title":"m%d","description":"d","state":"active","start_date":"2026-10-12","due_date":null,"web_url":"https://gitlab.example.com/g/p/-/milestones/%d"}`, 1000+i, i, i, i)
			},
			call: func(c *Client, maxPages int) (int, error) {
				rows, err := c.ListMilestones(context.Background(), 42, ListMilestonesOptions{
					UpdatedAfter: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), State: "all", MaxPages: maxPages,
				})
				return len(rows), err
			},
			wantKeys: map[string]string{"state": "all", "updated_after": "2026-06-01T00:00:00Z"},
		},
		{
			name: "ListTags",
			path: "/api/v4/projects/42/repository/tags",
			row: func(i int) string {
				return fmt.Sprintf(`{"name":"v1.%d.0","target":"t%d","commit":{"id":"c%d","created_at":"2026-10-12T10:00:00.000+07:00","web_url":"https://gitlab.example.com/g/p/-/commit/c%d"}}`, i, i, i, i)
			},
			call: func(c *Client, maxPages int) (int, error) {
				rows, err := c.ListTags(context.Background(), 42, ListTagsOptions{MaxPages: maxPages, OrderBy: "updated"})
				return len(rows), err
			},
			wantKeys: map[string]string{"order_by": "updated"},
		},
		{
			name: "ListPipelineJobs",
			path: "/api/v4/projects/42/pipelines/900/jobs",
			row: func(i int) string {
				return fmt.Sprintf(`{"name":"job%d","status":"success","allow_failure":false,"stage":"test"}`, i)
			},
			call: func(c *Client, maxPages int) (int, error) {
				rows, err := c.ListPipelineJobs(context.Background(), 42, 900, maxPages)
				return len(rows), err
			},
			wantKeys: map[string]string{"include_retried": "false"},
		},
	}
}

func pageOf(row func(int) string, n int) string {
	rows := make([]string, n)
	for i := range rows {
		rows[i] = row(i)
	}
	return "[" + strings.Join(rows, ",") + "]"
}

func TestListReadsPageUntilAShortPage(t *testing.T) {
	for _, tc := range listCases() {
		t.Run(tc.name, func(t *testing.T) {
			var pages []string
			c := readsClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.path {
					t.Errorf("path = %q, want %q", r.URL.Path, tc.path)
				}
				q := r.URL.Query()
				if q.Get("per_page") != "100" {
					t.Errorf("per_page = %q", q.Get("per_page"))
				}
				for k, v := range tc.wantKeys {
					if q.Get(k) != v {
						t.Errorf("%s = %q, want %q", k, q.Get(k), v)
					}
				}
				pages = append(pages, q.Get("page"))
				n := 100
				if q.Get("page") == "2" {
					n = 7
				}
				_, _ = w.Write([]byte(pageOf(tc.row, n)))
			})
			n, err := tc.call(c, 5)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if n != 107 || strings.Join(pages, ",") != "1,2" {
				t.Errorf("rows = %d pages = %v, want 107 over pages 1,2", n, pages)
			}
		})
	}
}

func TestListReadsReturnIncompleteAtTheCapWithAFullLastPage(t *testing.T) {
	for _, tc := range listCases() {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			c := readsClient(t, func(w http.ResponseWriter, _ *http.Request) {
				requests++
				_, _ = w.Write([]byte(pageOf(tc.row, 100)))
			})
			n, err := tc.call(c, 2)
			if !errors.Is(err, ErrIncomplete) {
				t.Fatalf("err = %v, want ErrIncomplete", err)
			}
			if n != 0 || requests != 2 {
				t.Errorf("rows = %d requests = %d, want no rows after 2 requests", n, requests)
			}
		})
	}
}

func TestListReadsAtTheCapWithAShortLastPageAreComplete(t *testing.T) {
	for _, tc := range listCases() {
		t.Run(tc.name, func(t *testing.T) {
			c := readsClient(t, func(w http.ResponseWriter, r *http.Request) {
				n := 100
				if r.URL.Query().Get("page") == "2" {
					n = 99
				}
				_, _ = w.Write([]byte(pageOf(tc.row, n)))
			})
			n, err := tc.call(c, 2)
			if err != nil || n != 199 {
				t.Errorf("rows = %d err = %v, want 199 and nil", n, err)
			}
		})
	}
}

func TestListReadsFailingPage2IsAnErrorNotAPartial(t *testing.T) {
	for _, tc := range listCases() {
		t.Run(tc.name, func(t *testing.T) {
			c := readsClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("page") == "2" {
					w.Header().Set("Retry-After", "30")
					w.WriteHeader(http.StatusTooManyRequests)
					return
				}
				_, _ = w.Write([]byte(pageOf(tc.row, 100)))
			})
			n, err := tc.call(c, 5)
			var rl *RateLimitError
			if !errors.As(err, &rl) || rl.RetryAfter != 30*time.Second {
				t.Fatalf("err = %v, want page 2's 429", err)
			}
			if n != 0 {
				t.Errorf("rows = %d, want none", n)
			}
		})
	}
}

type singleCase struct {
	name string
	call func(c *Client) error
}

func allReads() []singleCase {
	out := []singleCase{
		{"Compare", func(c *Client) error { _, err := c.Compare(context.Background(), 42, "v1.0.0", "main"); return err }},
		{"ListMergeRequestsPage", func(c *Client) error {
			_, err := c.ListMergeRequestsPage(context.Background(), 42, ListMRsOptions{UpdatedAfter: time.Now(), Page: 1})
			return err
		}},
		{"MergeRequestDetail", func(c *Client) error { _, err := c.MergeRequestDetail(context.Background(), 42, 7); return err }},
		{"CurrentUser", func(c *Client) error { _, err := c.CurrentUser(context.Background()); return err }},
	}
	for _, lc := range listCases() {
		call := lc.call
		out = append(out, singleCase{lc.name, func(c *Client) error { _, err := call(c, 5); return err }})
	}
	return out
}

func TestReads429CarryRetryAfterInBothForms(t *testing.T) {
	for _, header := range []string{"30", time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)} {
		for _, tc := range allReads() {
			t.Run(tc.name, func(t *testing.T) {
				c := readsClient(t, func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Retry-After", header)
					w.WriteHeader(http.StatusTooManyRequests)
					_, _ = w.Write([]byte(`{"message":"429 Too Many Requests"}`))
				})
				err := tc.call(c)
				var rl *RateLimitError
				if !errors.As(err, &rl) {
					t.Fatalf("err = %v, want *RateLimitError", err)
				}
				if rl.RetryAfter < 20*time.Second || rl.RetryAfter > 30*time.Second {
					t.Errorf("Retry-After %q: RetryAfter = %v, want about 30s", header, rl.RetryAfter)
				}
			})
		}
	}
}

func TestReadsMap401ToAuthFailedWithoutLeakingTheToken(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusInternalServerError} {
		for _, tc := range allReads() {
			t.Run(tc.name+"/"+strconv.Itoa(code), func(t *testing.T) {
				c := readsClient(t, func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(code)
					_, _ = w.Write([]byte(`{"message":"401 Unauthorized"}`))
				})
				err := tc.call(c)
				if err == nil {
					t.Fatal("err = nil")
				}
				if code == http.StatusUnauthorized && !errors.Is(err, ErrAuthFailed) {
					t.Errorf("err = %v, want ErrAuthFailed", err)
				}
				if strings.Contains(err.Error(), sentinelToken) {
					t.Errorf("error text leaks the token: %v", err)
				}
			})
		}
	}
}

func TestReadsTransportErrorDoesNotLeakTheToken(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()
	c := NewClient(ClientOptions{APIBase: base + "/api/v4", Token: StaticTokenSource(sentinelToken)})
	for _, tc := range allReads() {
		err := tc.call(c)
		if err == nil || strings.Contains(err.Error(), sentinelToken) {
			t.Errorf("%s: err = %v, want a transport error without the token", tc.name, err)
		}
	}
}

func TestListMilestonesMapsFields(t *testing.T) {
	c := readsClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id":1001,"iid":3,"title":"Release 6.7.0","description":"train #21","state":"closed",
			"start_date":"2026-10-12","due_date":"2026-10-19","web_url":"https://gitlab.example.com/g/p/-/milestones/3"},
			{"id":1002,"iid":4,"title":"Hotfix","description":null,"state":"active","start_date":null,"due_date":null,"web_url":"u"}]`))
	})
	ms, err := c.ListMilestones(context.Background(), 42, ListMilestonesOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := Milestone{ID: 1001, IID: 3, Title: "Release 6.7.0", Description: "train #21", State: "closed",
		StartDate: "2026-10-12", DueDate: "2026-10-19", WebURL: "https://gitlab.example.com/g/p/-/milestones/3"}
	if len(ms) != 2 || ms[0] != want {
		t.Fatalf("milestones = %+v", ms)
	}
	if ms[1].StartDate != "" || ms[1].DueDate != "" || ms[1].Description != "" {
		t.Errorf("null dates and description = %+v, want empty", ms[1])
	}
}

func TestListMilestonesOmitsUnsetFilters(t *testing.T) {
	var raw string
	c := readsClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw = r.URL.RawQuery
		_, _ = w.Write([]byte(`[]`))
	})
	if _, err := c.ListMilestones(context.Background(), 42, ListMilestonesOptions{}); err != nil {
		t.Fatal(err)
	}
	if raw != "page=1&per_page=100" {
		t.Errorf("query = %q", raw)
	}
}

func TestListTagsMapsFields(t *testing.T) {
	c := readsClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"name":"v6.7.0","target":"abc","message":"","commit":{"id":"abc","title":"t","created_at":"2026-10-12T10:00:00.000+07:00","web_url":"https://gitlab.example.com/g/p/-/commit/abc"}}]`))
	})
	tags, err := c.ListTags(context.Background(), 42, ListTagsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 1 {
		t.Fatalf("tags = %+v", tags)
	}
	tag := tags[0]
	if tag.Name != "v6.7.0" || tag.Target != "abc" || tag.Commit.ID != "abc" || tag.Commit.WebURL != "https://gitlab.example.com/g/p/-/commit/abc" {
		t.Errorf("tag = %+v", tag)
	}
	if !tag.Commit.CreatedAt.Equal(time.Date(2026, 10, 12, 3, 0, 0, 0, time.UTC)) {
		t.Errorf("commit created_at = %v", tag.Commit.CreatedAt)
	}
}

func TestCompareIsStraightAndMapsCommits(t *testing.T) {
	var q map[string][]string
	c := readsClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/projects/42/repository/compare" {
			t.Errorf("path = %q", r.URL.Path)
		}
		q = r.URL.Query()
		_, _ = w.Write([]byte(`{"commits":[
			{"id":"a1","title":"feat: one","created_at":"2026-10-13T09:00:00Z","web_url":"https://gitlab.example.com/g/p/-/commit/a1"},
			{"id":"a2","title":"fix: two","created_at":"2026-10-14T09:00:00Z","web_url":"https://gitlab.example.com/g/p/-/commit/a2"}],
			"diffs":[],"web_url":"https://gitlab.example.com/g/p/-/compare/v6.7.0...main"}`))
	})
	cmp, err := c.Compare(context.Background(), 42, "v6.7.0", "main")
	if err != nil {
		t.Fatal(err)
	}
	if q["from"][0] != "v6.7.0" || q["to"][0] != "main" || q["straight"][0] != "true" {
		t.Errorf("query = %v", q)
	}
	if len(cmp.Commits) != 2 || cmp.Commits[1].Title != "fix: two" || cmp.Commits[0].WebURL != "https://gitlab.example.com/g/p/-/commit/a1" {
		t.Errorf("commits = %+v", cmp.Commits)
	}
	if !cmp.Commits[0].CreatedAt.Equal(time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("created_at = %v", cmp.Commits[0].CreatedAt)
	}
	if cmp.WebURL != "https://gitlab.example.com/g/p/-/compare/v6.7.0...main" {
		t.Errorf("web_url = %q", cmp.WebURL)
	}
}

func TestListMergeRequestsPageQueryAndFullness(t *testing.T) {
	var raw string
	row := func(i int) string {
		return fmt.Sprintf(`{"iid":%d,"title":"MR %d","state":"merged","source_branch":"feature/x","target_branch":"develop",
			"author":{"username":"alex","name":"Alex Rivera"},"created_at":"2026-10-01T10:00:00.000+07:00",
			"updated_at":"2026-10-02T10:00:00.000+07:00","merged_at":"2026-10-02T09:00:00.000+07:00",
			"milestone":{"id":1001},"web_url":"https://gitlab.example.com/g/p/-/merge_requests/%d"}`, i, i, i)
	}
	n := 100
	c := readsClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw = r.URL.RawQuery
		_, _ = w.Write([]byte(pageOf(row, n)))
	})
	after := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	page, err := c.ListMergeRequestsPage(context.Background(), 42, ListMRsOptions{UpdatedAfter: after, Page: 3})
	if err != nil {
		t.Fatal(err)
	}
	if raw != "order_by=updated_at&page=3&per_page=100&scope=all&sort=asc&state=all&updated_after=2026-06-01T00%3A00%3A00Z" {
		t.Errorf("query = %q", raw)
	}
	if !page.Full || len(page.Items) != 100 {
		t.Errorf("full = %v rows = %d, want a full page", page.Full, len(page.Items))
	}
	mr := page.Items[0]
	if mr.IID != 0 || mr.Author.Username != "alex" || mr.Author.Name != "Alex Rivera" || mr.MilestoneID == nil || *mr.MilestoneID != 1001 ||
		mr.MergedAt == nil || mr.SourceBranch != "feature/x" || mr.TargetBranch != "develop" || mr.State != "merged" || mr.WebURL == "" {
		t.Errorf("mr = %+v", mr)
	}
	if !mr.UpdatedAt.Equal(time.Date(2026, 10, 2, 3, 0, 0, 0, time.UTC)) {
		t.Errorf("updated_at = %v", mr.UpdatedAt)
	}

	n = 3
	page, err = c.ListMergeRequestsPage(context.Background(), 42, ListMRsOptions{UpdatedAfter: after, Page: 1})
	if err != nil || page.Full || len(page.Items) != 3 {
		t.Errorf("short page: full = %v rows = %d err = %v", page.Full, len(page.Items), err)
	}
}

func TestListMergeRequestsPageOpenMRHasNoMergedAtOrMilestone(t *testing.T) {
	c := readsClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"iid":5,"state":"opened","merged_at":null,"milestone":null,"author":{"username":"a","name":"A"},
			"created_at":"2026-10-01T10:00:00Z","updated_at":"2026-10-01T10:00:00Z"}]`))
	})
	page, err := c.ListMergeRequestsPage(context.Background(), 42, ListMRsOptions{Page: 1})
	if err != nil {
		t.Fatal(err)
	}
	if page.Items[0].MergedAt != nil || page.Items[0].MilestoneID != nil {
		t.Errorf("mr = %+v", page.Items[0])
	}
}

func TestMergeRequestDetailMapsHeadPipeline(t *testing.T) {
	c := readsClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/projects/42/merge_requests/7" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"iid":7,"detailed_merge_status":"mergeable","has_conflicts":false,
			"head_pipeline":{"id":900,"status":"running","web_url":"https://gitlab.example.com/g/p/-/pipelines/900",
			"duration":null,"created_at":"2026-10-14T08:00:00Z"}}`))
	})
	d, err := c.MergeRequestDetail(context.Background(), 42, 7)
	if err != nil {
		t.Fatal(err)
	}
	if d.DetailedMergeStatus != "mergeable" || d.HasConflicts || d.HeadPipeline == nil {
		t.Fatalf("detail = %+v", d)
	}
	p := d.HeadPipeline
	if p.ID != 900 || p.Status != "running" || p.WebURL == "" || p.Duration != nil ||
		!p.CreatedAt.Equal(time.Date(2026, 10, 14, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("pipeline = %+v", p)
	}
}

func TestMergeRequestDetailWithoutPipeline(t *testing.T) {
	c := readsClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"iid":7,"detailed_merge_status":"conflict","has_conflicts":true,"head_pipeline":null}`))
	})
	d, err := c.MergeRequestDetail(context.Background(), 42, 7)
	if err != nil || d.HeadPipeline != nil || !d.HasConflicts {
		t.Errorf("detail = %+v err = %v", d, err)
	}
}

func TestListPipelineJobsMapsFields(t *testing.T) {
	c := readsClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id":1,"name":"semgrep-sast","status":"failed","allow_failure":true,"stage":"test"}]`))
	})
	jobs, err := c.ListPipelineJobs(context.Background(), 42, 900, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0] != (Job{Name: "semgrep-sast", Status: "failed", AllowFailure: true, Stage: "test"}) {
		t.Errorf("jobs = %+v", jobs)
	}
}

func TestCurrentUser(t *testing.T) {
	c := readsClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/user" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":5,"username":"alex","name":"Alex Rivera"}`))
	})
	u, err := c.CurrentUser(context.Background())
	if err != nil || u != (User{Username: "alex", Name: "Alex Rivera"}) {
		t.Errorf("user = %+v err = %v", u, err)
	}
}
