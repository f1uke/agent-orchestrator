package jira

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/atlassian"
)

const sentinelToken = "fake-example-token"

func readsClient(t *testing.T, h http.HandlerFunc) (*Client, string) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewClient(WithHTTPDoer(srv.Client().Do), WithConfigSource(func() (atlassian.Config, error) {
		return atlassian.Config{BaseURL: srv.URL, Email: "alex@example.com", Token: sentinelToken}, nil
	})), srv.URL
}

type pagedCase struct {
	name string
	path string
	// page writes page n (0-based) of a list; last says whether it is the final one.
	page func(w io.Writer, n int, full, last bool)
	// pageIndex reads which page a request asks for.
	pageIndex func(r *http.Request) int
	call      func(c *Client, maxPages int) (int, error)
}

func offsetIndex(size int) func(r *http.Request) int {
	return func(r *http.Request) int {
		start, _ := strconv.Atoi(r.URL.Query().Get("startAt"))
		return start / size
	}
}

func offsetPage(size int, row func(i int) string) func(w io.Writer, n int, full, last bool) {
	return func(w io.Writer, n int, full, last bool) {
		count := size
		if !full {
			count = 3
		}
		rows := make([]string, count)
		for i := range rows {
			rows[i] = row(n*size + i)
		}
		fmt.Fprintf(w, `{"startAt":%d,"maxResults":%d,"isLast":%t,"values":[%s]}`, n*size, size, last, strings.Join(rows, ","))
	}
}

func pagedCases() []pagedCase {
	return []pagedCase{
		{
			name:      "ProjectVersions",
			path:      "/rest/api/3/project/DEMO/version",
			pageIndex: offsetIndex(50),
			page: offsetPage(50, func(i int) string {
				return fmt.Sprintf(`{"id":"%d","name":"App 1.%d.0","released":false}`, 10000+i, i)
			}),
			call: func(c *Client, maxPages int) (int, error) {
				rows, err := c.ProjectVersions(context.Background(), "DEMO", maxPages)
				return len(rows), err
			},
		},
		{
			name:      "BoardSprints",
			path:      "/rest/agile/1.0/board/7/sprint",
			pageIndex: offsetIndex(50),
			page: offsetPage(50, func(i int) string {
				return fmt.Sprintf(`{"id":%d,"name":"DEMO Sprint %d","state":"closed"}`, 100+i, i)
			}),
			call: func(c *Client, maxPages int) (int, error) {
				rows, err := c.BoardSprints(context.Background(), 7, []string{"active", "closed"}, maxPages)
				return len(rows), err
			},
		},
		{
			name: "SearchAllFields",
			path: "/rest/api/3/search/jql",
			pageIndex: func(r *http.Request) int {
				n, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Query().Get("nextPageToken"), "T"))
				return n
			},
			page: func(w io.Writer, n int, full, last bool) {
				count := searchPageSize
				if !full {
					count = 3
				}
				next := ""
				if !last {
					next = "T" + strconv.Itoa(n+1)
				}
				_, _ = io.WriteString(w, issuesJSON(keysN(fmt.Sprintf("DEMO%d", n), count), next))
			},
			call: func(c *Client, maxPages int) (int, error) {
				rows, err := c.SearchAllFields(context.Background(), `project = DEMO`, nil, maxPages)
				return len(rows), err
			},
		},
	}
}

func TestPagedReadsFollowPagesToTheLast(t *testing.T) {
	for _, tc := range pagedCases() {
		t.Run(tc.name, func(t *testing.T) {
			var seen []int
			c, _ := readsClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.path {
					t.Errorf("path = %q, want %q", r.URL.Path, tc.path)
				}
				n := tc.pageIndex(r)
				seen = append(seen, n)
				tc.page(w, n, n < 2, n == 2)
			})
			got, err := tc.call(c, 10)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if len(seen) != 3 || seen[2] != 2 {
				t.Errorf("pages = %v, want 0,1,2", seen)
			}
			if got <= 3 {
				t.Errorf("rows = %d, want rows from every page", got)
			}
		})
	}
}

func TestPagedReadsReturnIncompleteAtTheCap(t *testing.T) {
	for _, tc := range pagedCases() {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			c, _ := readsClient(t, func(w http.ResponseWriter, r *http.Request) {
				requests++
				tc.page(w, tc.pageIndex(r), true, false)
			})
			got, err := tc.call(c, 2)
			if !errors.Is(err, ErrIncomplete) {
				t.Fatalf("err = %v, want ErrIncomplete", err)
			}
			if got != 0 || requests != 2 {
				t.Errorf("rows = %d requests = %d, want none after 2 requests", got, requests)
			}
		})
	}
}

func TestPagedReadsLastPageAtTheCapIsComplete(t *testing.T) {
	for _, tc := range pagedCases() {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := readsClient(t, func(w http.ResponseWriter, r *http.Request) {
				n := tc.pageIndex(r)
				tc.page(w, n, true, n == 1)
			})
			if _, err := tc.call(c, 2); err != nil {
				t.Errorf("err = %v, want nil", err)
			}
		})
	}
}

func TestPagedReadsFailingPage2IsIncompleteNotPartial(t *testing.T) {
	for _, tc := range pagedCases() {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := readsClient(t, func(w http.ResponseWriter, r *http.Request) {
				n := tc.pageIndex(r)
				if n == 1 {
					w.Header().Set("Retry-After", "30")
					w.WriteHeader(http.StatusTooManyRequests)
					return
				}
				tc.page(w, n, true, false)
			})
			got, err := tc.call(c, 10)
			if !errors.Is(err, ErrIncomplete) {
				t.Fatalf("err = %v, want ErrIncomplete", err)
			}
			var rl *RateLimitError
			if !errors.As(err, &rl) || rl.RetryAfter != 30*time.Second {
				t.Errorf("err = %v, want page 2's 429 inside", err)
			}
			if got != 0 {
				t.Errorf("rows = %d, want none", got)
			}
		})
	}
}

type readCase struct {
	name string
	call func(c *Client) error
}

func allJiraReads() []readCase {
	out := []readCase{
		{"VersionRelatedWork", func(c *Client) error { _, err := c.VersionRelatedWork(context.Background(), "10001"); return err }},
		{"Board", func(c *Client) error { _, err := c.Board(context.Background(), 7); return err }},
		{"User", func(c *Client) error { _, err := c.User(context.Background(), "acc-1"); return err }},
	}
	for _, pc := range pagedCases() {
		call := pc.call
		out = append(out, readCase{pc.name, func(c *Client) error { _, err := call(c, 5); return err }})
	}
	return out
}

func TestReads429CarryRetryAfterInBothForms(t *testing.T) {
	for _, header := range []string{"30", time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)} {
		for _, tc := range allJiraReads() {
			t.Run(tc.name, func(t *testing.T) {
				c, _ := readsClient(t, func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Retry-After", header)
					w.WriteHeader(http.StatusTooManyRequests)
					_, _ = io.WriteString(w, `{"errorMessages":["Rate limit exceeded"]}`)
				})
				err := tc.call(c)
				var rl *RateLimitError
				if !errors.As(err, &rl) || !errors.Is(err, ErrRateLimited) {
					t.Fatalf("err = %v, want *RateLimitError", err)
				}
				if rl.RetryAfter < 20*time.Second || rl.RetryAfter > 30*time.Second {
					t.Errorf("Retry-After %q: RetryAfter = %v", header, rl.RetryAfter)
				}
				if errors.Is(err, ErrIncomplete) {
					t.Errorf("a first-page 429 is not an incomplete read")
				}
			})
		}
	}
}

func TestReadsMap401ToAuthFailed(t *testing.T) {
	for _, tc := range allJiraReads() {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := readsClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
			})
			if err := tc.call(c); !errors.Is(err, ErrAuthFailed) {
				t.Errorf("err = %v, want ErrAuthFailed", err)
			}
		})
	}
}

func TestReadsErrorTextCarriesNoTokenOrQuery(t *testing.T) {
	basic := base64.StdEncoding.EncodeToString([]byte("alex@example.com:" + sentinelToken))
	for _, code := range []int{http.StatusUnauthorized, http.StatusInternalServerError, http.StatusTooManyRequests, http.StatusBadRequest} {
		for _, tc := range allJiraReads() {
			t.Run(tc.name+"/"+strconv.Itoa(code), func(t *testing.T) {
				c, _ := readsClient(t, func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(code)
					_, _ = io.WriteString(w, strings.Repeat("x", 2000))
				})
				err := tc.call(c)
				if err == nil {
					t.Fatal("err = nil")
				}
				msg := err.Error()
				if strings.Contains(msg, sentinelToken) || strings.Contains(msg, basic) {
					t.Errorf("error text leaks the credential: %q", msg)
				}
				if len(msg) > 400 {
					t.Errorf("error text is %d bytes, want the server message capped", len(msg))
				}
			})
		}
	}
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()
	c := NewClient(WithConfigSource(func() (atlassian.Config, error) {
		return atlassian.Config{BaseURL: base, Email: "alex@example.com", Token: sentinelToken}, nil
	}))
	for _, tc := range allJiraReads() {
		err := tc.call(c)
		if !errors.Is(err, ErrUnavailable) {
			t.Errorf("%s transport: err = %v, want ErrUnavailable", tc.name, err)
		}
		if err != nil && (strings.Contains(err.Error(), sentinelToken) || strings.Contains(err.Error(), "?")) {
			t.Errorf("%s transport: err = %q, want no token and no query string", tc.name, err)
		}
	}
}

func TestProjectVersionsQueryAndMapping(t *testing.T) {
	var q map[string][]string
	c, base := readsClient(t, func(w http.ResponseWriter, r *http.Request) {
		q = r.URL.Query()
		_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"isLast":true,"values":[{
			"id":"10001","name":"App iOS 6.7.0","description":"Release train #21","released":true,"releaseDate":"2026-10-19",
			"approvers":[{"accountId":"acc-1","status":"APPROVED"},{"accountId":"acc-2","status":"PENDING"}],
			"issuesStatusForFixVersion":{"unmapped":1,"toDo":2,"inProgress":3,"done":4}},
			{"id":"10002","name":"App Android 6.7.0","released":false}]}`)
	})
	vs, err := c.ProjectVersions(context.Background(), "DEMO", 0)
	if err != nil {
		t.Fatal(err)
	}
	if q["expand"][0] != "approvers,issuesstatus" || q["startAt"][0] != "0" || q["maxResults"][0] != "50" {
		t.Errorf("query = %v", q)
	}
	v := vs[0]
	if v.ID != "10001" || v.Name != "App iOS 6.7.0" || v.Description != "Release train #21" || !v.Released || v.ReleaseDate != "2026-10-19" {
		t.Errorf("version = %+v", v)
	}
	if len(v.Approvers) != 2 || v.Approvers[0] != (VersionApprover{AccountID: "acc-1", Status: "APPROVED"}) {
		t.Errorf("approvers = %+v", v.Approvers)
	}
	if v.IssueCounts != (IssueStatusCounts{Unmapped: 1, ToDo: 2, InProgress: 3, Done: 4}) {
		t.Errorf("counts = %+v", v.IssueCounts)
	}
	if v.URL != base+"/projects/DEMO/versions/10001" {
		t.Errorf("url = %q", v.URL)
	}
	if vs[1].ReleaseDate != "" || vs[1].Approvers != nil {
		t.Errorf("bare version = %+v", vs[1])
	}
}

func TestBadIdentifiersAreRefusedBeforeHTTP(t *testing.T) {
	c := NewClient(WithHTTPDoer(func(*http.Request) (*http.Response, error) {
		t.Fatal("HTTP must not be called")
		return nil, nil
	}), WithConfigSource(staticConfig("http://x")))
	ctx := context.Background()
	if _, err := c.ProjectVersions(ctx, "demo/../x", 1); !errors.Is(err, ErrBadRequest) {
		t.Errorf("ProjectVersions err = %v", err)
	}
	if _, err := c.VersionRelatedWork(ctx, "1/2"); !errors.Is(err, ErrBadRequest) {
		t.Errorf("VersionRelatedWork err = %v", err)
	}
	if _, err := c.User(ctx, " "); !errors.Is(err, ErrBadRequest) {
		t.Errorf("User err = %v", err)
	}
	if _, err := c.SearchAllFields(ctx, " ", nil, 1); !errors.Is(err, ErrBadQuery) {
		t.Errorf("SearchAllFields err = %v", err)
	}
}

func TestVersionRelatedWork(t *testing.T) {
	c, _ := readsClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/3/version/10001/relatedwork" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, `[{"relatedWorkId":"r1","category":"Development","title":"Scan","url":"https://example.atlassian.net/wiki/x"},
			{"relatedWorkId":"r2","category":"Communication","title":"Notes","url":"https://example.com/n"}]`)
	})
	rw, err := c.VersionRelatedWork(context.Background(), "10001")
	if err != nil {
		t.Fatal(err)
	}
	if len(rw) != 2 || rw[0] != (RelatedWorkLink{Category: "Development", Title: "Scan", URL: "https://example.atlassian.net/wiki/x"}) {
		t.Errorf("related work = %+v", rw)
	}
}

func TestSearchAllFieldsMapsRowsFromTheRequestedFields(t *testing.T) {
	var q map[string][]string
	c, base := readsClient(t, func(w http.ResponseWriter, r *http.Request) {
		q = r.URL.Query()
		_, _ = io.WriteString(w, `{"issues":[{"key":"DEMO-7","fields":{
			"summary":"Pay with card","status":{"name":"Ready to Release","statusCategory":{"key":"done"}},
			"issuetype":{"name":"Sub-task","subtask":true},
			"assignee":{"accountId":"acc-1","displayName":"Alex Rivera"},
			"fixVersions":[{"id":"10001","name":"App iOS 6.7.0"},{"id":"10002","name":"App Android 6.7.0"}],
			"parent":{"key":"DEMO-1","fields":{"summary":"Payments","issuetype":{"name":"Epic"}}},
			"subtasks":[{"key":"DEMO-8"},{"key":"DEMO-9"}],
			"labels":["mobile"],"duedate":"2026-10-20","created":"2026-10-01T10:00:00.000+0700"}},
			{"key":"DEMO-10","fields":{"summary":"Bare","assignee":null,"duedate":null}}],"isLast":true}`)
	})
	rows, err := c.SearchAllFields(context.Background(), `fixVersion = 10001`, []string{"summary", "status"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if q["fields"][0] != "summary,status" || q["maxResults"][0] != "100" || q["jql"][0] != "fixVersion = 10001" {
		t.Errorf("query = %v", q)
	}
	r := rows[0]
	if r.Key != "DEMO-7" || r.Summary != "Pay with card" || r.Status != "Ready to Release" || r.StatusCategory != "done" ||
		r.IssueType != "Sub-task" || !r.Subtask || r.ParentKey != "DEMO-1" || r.ParentType != "Epic" || r.DueDate != "2026-10-20" || r.URL != base+"/browse/DEMO-7" {
		t.Errorf("row = %+v", r)
	}
	if r.Assignee == nil || *r.Assignee != (Assignee{AccountID: "acc-1", DisplayName: "Alex Rivera"}) {
		t.Errorf("assignee = %+v", r.Assignee)
	}
	if strings.Join(r.FixVersionIDs, ",") != "10001,10002" || strings.Join(r.SubtaskKeys, ",") != "DEMO-8,DEMO-9" || strings.Join(r.Labels, ",") != "mobile" {
		t.Errorf("lists = %+v", r)
	}
	if !r.Created.Equal(time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC)) {
		t.Errorf("created = %v", r.Created)
	}
	if rows[1].Assignee != nil || rows[1].DueDate != "" || !rows[1].Created.IsZero() {
		t.Errorf("bare row = %+v", rows[1])
	}
}

func TestSearchAllFieldsDefaultsToTheRowFields(t *testing.T) {
	var fields string
	c, _ := readsClient(t, func(w http.ResponseWriter, r *http.Request) {
		fields = r.URL.Query().Get("fields")
		_, _ = io.WriteString(w, `{"issues":[]}`)
	})
	if _, err := c.SearchAllFields(context.Background(), "project = DEMO", nil, 1); err != nil {
		t.Fatal(err)
	}
	if fields != strings.Join(IssueRowFields, ",") {
		t.Errorf("fields = %q", fields)
	}
}

func TestSearchAllFieldsBadJQLIsErrBadQuery(t *testing.T) {
	c, _ := readsClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"errorMessages":["Field 'nope' does not exist"]}`)
	})
	_, err := c.SearchAllFields(context.Background(), "nope = 1", nil, 1)
	if !errors.Is(err, ErrBadQuery) || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("err = %v", err)
	}
}

func TestBoardSprintsQueryAndMapping(t *testing.T) {
	var q map[string][]string
	c, _ := readsClient(t, func(w http.ResponseWriter, r *http.Request) {
		q = r.URL.Query()
		_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"isLast":true,"values":[
			{"id":101,"name":"DEMO 2026-21","state":"active","startDate":"2026-10-12T03:00:00.000Z","endDate":"2026-10-26T03:00:00.000Z"},
			{"id":102,"name":"DEMO 2026-22","state":"future"}]}`)
	})
	sp, err := c.BoardSprints(context.Background(), 7, []string{"active", "future"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if q["state"][0] != "active,future" || q["maxResults"][0] != "50" {
		t.Errorf("query = %v", q)
	}
	if sp[0].ID != 101 || sp[0].Name != "DEMO 2026-21" || sp[0].State != "active" || sp[0].StartDate == nil ||
		!sp[0].StartDate.Equal(time.Date(2026, 10, 12, 3, 0, 0, 0, time.UTC)) || sp[0].EndDate == nil {
		t.Errorf("sprint = %+v", sp[0])
	}
	if sp[1].StartDate != nil || sp[1].EndDate != nil {
		t.Errorf("future sprint = %+v", sp[1])
	}
}

func TestBoardSprintsWithoutStatesSendsNoFilter(t *testing.T) {
	var has bool
	c, _ := readsClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, has = r.URL.Query()["state"]
		_, _ = io.WriteString(w, `{"isLast":true,"values":[]}`)
	})
	if _, err := c.BoardSprints(context.Background(), 7, nil, 1); err != nil || has {
		t.Errorf("err = %v, state sent = %v", err, has)
	}
}

func TestBoardAndUser(t *testing.T) {
	c, _ := readsClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/agile/1.0/board/7":
			_, _ = io.WriteString(w, `{"id":7,"name":"DEMO board","type":"scrum"}`)
		case "/rest/api/3/user":
			if r.URL.Query().Get("accountId") != "acc:1" {
				t.Errorf("accountId = %q", r.URL.Query().Get("accountId"))
			}
			_, _ = io.WriteString(w, `{"accountId":"acc:1","displayName":"Alex Rivera","avatarUrls":{"16x16":"s","48x48":"https://example.com/a/48"}}`)
		default:
			t.Errorf("path = %q", r.URL.Path)
		}
	})
	b, err := c.Board(context.Background(), 7)
	if err != nil || b.Name != "DEMO board" {
		t.Errorf("board = %+v err = %v", b, err)
	}
	u, err := c.User(context.Background(), "acc:1")
	if err != nil || u != (UserProfile{AccountID: "acc:1", DisplayName: "Alex Rivera", AvatarURL: "https://example.com/a/48"}) {
		t.Errorf("user = %+v err = %v", u, err)
	}
}
