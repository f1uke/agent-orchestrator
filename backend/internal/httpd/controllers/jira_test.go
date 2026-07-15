package controllers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	jiraadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/jira"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/jira/adf"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	jirasvc "github.com/aoagents/agent-orchestrator/backend/internal/service/jira"
)

type stubJira struct {
	res jirasvc.Result
	err error

	transitions []jiraadapter.Transition
	transErr    error
	gotTransKey string
	moveRes     jirasvc.MoveResult
	moveErr     error
	gotMoveKey  string
	gotMoveID   string

	searchRes    []jiraadapter.IssueSummary
	searchErr    error
	gotProject   string
	gotText      string
	gotAssignee  string
	gotTypes     []string
	gotHideDone  bool
	gotActiveSpr bool
	gotJQL       string
	projectRes   []jiraadapter.ProjectRef
	projectErr   error
	bindRes      jiraadapter.IssueSummary
	bindErr      error
	gotBindKey   string
	unlinkErr    error

	issueRes         jiraadapter.Issue
	issueErr         error
	gotIssueKey      string
	gotIssueTransKey string
	gotIssueMoveKey  string
	gotIssueMoveID   string

	me    jiraadapter.CurrentUser
	meErr error

	dlBody  string
	dlCtype string
	dlErr   error
	gotDlID string
}

func (s *stubJira) Context(context.Context, domain.SessionID) (jirasvc.Result, error) {
	return s.res, s.err
}

func (s *stubJira) DownloadAttachment(_ context.Context, _ domain.SessionID, attachmentID string) (io.ReadCloser, string, error) {
	s.gotDlID = attachmentID
	if s.dlErr != nil {
		return nil, "", s.dlErr
	}
	return io.NopCloser(strings.NewReader(s.dlBody)), s.dlCtype, nil
}

func TestServeJiraAttachment_StreamsBytes(t *testing.T) {
	svc := &stubJira{dlBody: "PNGBYTES", dlCtype: "image/png"}
	rec := serveJiraReq(t, svc, http.MethodGet, "/sessions/proj-1/jira/attachments/173517", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "PNGBYTES" {
		t.Errorf("body = %q", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("content-type = %q", ct)
	}
	if svc.gotDlID != "173517" {
		t.Errorf("attachment id = %q", svc.gotDlID)
	}
}

func TestServeJiraAttachment_NotImplementedWhenNoService(t *testing.T) {
	rec := serveJiraReq(t, nil, http.MethodGet, "/sessions/proj-1/jira/attachments/1", nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

func TestServeJiraAttachment_ErrorMapsToStatus(t *testing.T) {
	svc := &stubJira{dlErr: jiraadapter.ErrNotFound}
	rec := serveJiraReq(t, svc, http.MethodGet, "/sessions/proj-1/jira/attachments/999", nil)
	if rec.Code == http.StatusOK {
		t.Fatalf("status = %d, want a non-200 error", rec.Code)
	}
}

func TestJiraIssueDTOMapsAttachments(t *testing.T) {
	iss := jiraadapter.Issue{Key: "DEMO-1", Attachments: []jiraadapter.Attachment{
		{ID: "173517", Filename: "a.png", MimeType: "image/png"},
		{ID: "173520", Filename: "clip.mp4", MimeType: "video/mp4"},
	}}
	dto := jiraIssueDTO(iss)
	if len(dto.Attachments) != 2 {
		t.Fatalf("attachments = %d, want 2", len(dto.Attachments))
	}
	if dto.Attachments[0].ID != "173517" || dto.Attachments[0].Filename != "a.png" || dto.Attachments[0].MimeType != "image/png" {
		t.Errorf("attachment[0] = %+v", dto.Attachments[0])
	}
	if dto.Attachments[1].MimeType != "video/mp4" {
		t.Errorf("attachment[1] mime = %q", dto.Attachments[1].MimeType)
	}
}

func (s *stubJira) Transitions(_ context.Context, _ domain.SessionID, key string) ([]jiraadapter.Transition, error) {
	s.gotTransKey = key
	return s.transitions, s.transErr
}

func (s *stubJira) Move(_ context.Context, _ domain.SessionID, key, transitionID string) (jirasvc.MoveResult, error) {
	s.gotMoveKey = key
	s.gotMoveID = transitionID
	return s.moveRes, s.moveErr
}

func (s *stubJira) Search(_ context.Context, p jirasvc.SearchParams) ([]jiraadapter.IssueSummary, error) {
	s.gotProject, s.gotText, s.gotAssignee, s.gotTypes = p.Project, p.Text, p.Assignee, p.Types
	s.gotHideDone, s.gotActiveSpr, s.gotJQL = p.HideDone, p.ActiveSprint, p.JQL
	return s.searchRes, s.searchErr
}

func (s *stubJira) Projects(context.Context, string) ([]jiraadapter.ProjectRef, error) {
	return s.projectRes, s.projectErr
}

func (s *stubJira) CurrentUser(context.Context) (jiraadapter.CurrentUser, error) {
	return s.me, s.meErr
}

func (s *stubJira) SetBinding(_ context.Context, _ domain.SessionID, key string) (jiraadapter.IssueSummary, error) {
	s.gotBindKey = key
	return s.bindRes, s.bindErr
}

func (s *stubJira) Unlink(context.Context, domain.SessionID) (domain.Session, error) {
	return domain.Session{}, s.unlinkErr
}

func (s *stubJira) GetIssue(_ context.Context, key string) (jiraadapter.Issue, error) {
	s.gotIssueKey = key
	return s.issueRes, s.issueErr
}

func (s *stubJira) IssueTransitions(_ context.Context, key string) ([]jiraadapter.Transition, error) {
	s.gotIssueTransKey = key
	return s.transitions, s.transErr
}

func (s *stubJira) MoveIssue(_ context.Context, key, transitionID string) (jirasvc.MoveResult, error) {
	s.gotIssueMoveKey, s.gotIssueMoveID = key, transitionID
	return s.moveRes, s.moveErr
}

func serveJira(t *testing.T, svc JiraService) *httptest.ResponseRecorder {
	t.Helper()
	return serveJiraReq(t, svc, http.MethodGet, "/sessions/s1/jira", nil)
}

func serveJiraReq(t *testing.T, svc JiraService, method, path string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	c := &JiraController{Svc: svc}
	r := chi.NewRouter()
	c.Register(r)
	req := httptest.NewRequest(method, path, body)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestJiraGet_LinkedIssue(t *testing.T) {
	iss := jiraadapter.Issue{
		Key: "DEMO-101", URL: "https://x.atlassian.net/browse/DEMO-101",
		Type: "Story", Title: "Example issue summary",
		Status: "Ready for QA", StatusCategory: "new", StatusColor: "blue-gray",
		Priority: "Medium", Assignee: "Alex", Reporter: "Sam",
		Sprint:      &jiraadapter.Sprint{Name: "Sprint 2026-14", State: "active"},
		Description: []adf.Node{{Type: "paragraph", Content: []adf.Node{{Type: "text", Text: "hi"}}}},
		Subtasks:    []jiraadapter.Subtask{{Key: "DEMO-102", Type: "Sub-task", Status: "Pull Request"}},
	}
	rec := serveJira(t, &stubJira{res: jirasvc.Result{Linked: true, Issue: &iss}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body JiraContextResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Linked || body.Issue == nil {
		t.Fatalf("body = %+v", body)
	}
	if body.Issue.Key != "DEMO-101" || body.Issue.StatusCategory != "new" || body.Issue.Sprint == nil {
		t.Errorf("issue mapped wrong: %+v", body.Issue)
	}
	if len(body.Issue.Description) == 0 || len(body.Issue.Subtasks) != 1 {
		t.Errorf("description/subtasks not mapped: %+v", body.Issue)
	}
	if body.SessionID != "s1" {
		t.Errorf("sessionId = %q", body.SessionID)
	}
}

func TestJiraGet_NotLinked(t *testing.T) {
	rec := serveJira(t, &stubJira{res: jirasvc.Result{Linked: false}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body JiraContextResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Linked || body.Issue != nil {
		t.Errorf("expected unlinked empty, got %+v", body)
	}
}

func TestJiraGet_FetchErrorIs200(t *testing.T) {
	rec := serveJira(t, &stubJira{res: jirasvc.Result{Linked: true, FetchError: "Couldn't reach Jira."}})
	if rec.Code != http.StatusOK {
		t.Fatalf("a Jira fetch failure must still be 200, got %d", rec.Code)
	}
	var body JiraContextResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if !body.Linked || body.Issue != nil || body.FetchError == "" {
		t.Errorf("expected linked with fetchError, got %+v", body)
	}
}

func TestJiraGet_SessionNotFoundIs404(t *testing.T) {
	rec := serveJira(t, &stubJira{err: apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestJiraGet_NilServiceNotImplemented(t *testing.T) {
	rec := serveJira(t, nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

func getTransitions(t *testing.T, svc JiraService) *httptest.ResponseRecorder {
	t.Helper()
	return serveJiraReq(t, svc, http.MethodGet, "/sessions/s1/jira/transitions", nil)
}

func postMove(t *testing.T, svc JiraService, body string) *httptest.ResponseRecorder {
	t.Helper()
	return serveJiraReq(t, svc, http.MethodPost, "/sessions/s1/jira/move", strings.NewReader(body))
}

func TestJiraTransitions_ListsLive(t *testing.T) {
	rec := getTransitions(t, &stubJira{transitions: []jiraadapter.Transition{
		{ID: "11", Name: "Start Testing", To: "In Progress", ToCategory: "indeterminate"},
		{ID: "21", Name: "Abandoned", To: "Abandoned", ToCategory: "done"},
	}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body JiraTransitionsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.SessionID != "s1" || len(body.Transitions) != 2 {
		t.Fatalf("body = %+v", body)
	}
	if body.Transitions[0].ID != "11" || body.Transitions[0].Name != "Start Testing" || body.Transitions[0].To != "In Progress" {
		t.Errorf("transition mapped wrong: %+v", body.Transitions[0])
	}
}

func TestJiraTransitions_NotLinkedIs400(t *testing.T) {
	rec := getTransitions(t, &stubJira{transErr: jirasvc.ErrNotLinked})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestJiraTransitions_NilServiceNotImplemented(t *testing.T) {
	rec := getTransitions(t, nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

func TestJiraMove_AppliesAndReturnsNewStatus(t *testing.T) {
	rec := postMove(t, &stubJira{moveRes: jirasvc.MoveResult{Key: "DEMO-101", Status: "In Progress", StatusCategory: "indeterminate"}}, `{"transitionId":"11"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body JiraMoveResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Key != "DEMO-101" || body.Status != "In Progress" || body.StatusCategory != "indeterminate" {
		t.Errorf("move response = %+v", body)
	}
}

func TestJiraMove_MissingTransitionIs400(t *testing.T) {
	rec := postMove(t, &stubJira{}, `{"transitionId":""}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestJiraMove_MalformedBodyIs400(t *testing.T) {
	rec := postMove(t, &stubJira{}, `not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// The ?key= query on transitions and the issueKey body field on move must reach
// the service so a subtask can be targeted.
func TestJiraTransitions_PassesSubtaskKey(t *testing.T) {
	stub := &stubJira{}
	serveJiraReq(t, stub, http.MethodGet, "/sessions/s1/jira/transitions?key=DEMO-102", nil)
	if stub.gotTransKey != "DEMO-102" {
		t.Errorf("transitions got key %q, want DEMO-102", stub.gotTransKey)
	}
}

func TestJiraMove_PassesSubtaskKey(t *testing.T) {
	stub := &stubJira{moveRes: jirasvc.MoveResult{Key: "DEMO-102"}}
	postMove(t, stub, `{"transitionId":"21","issueKey":"DEMO-102"}`)
	if stub.gotMoveKey != "DEMO-102" || stub.gotMoveID != "21" {
		t.Errorf("move got key=%q id=%q, want DEMO-102/21", stub.gotMoveKey, stub.gotMoveID)
	}
}

func TestJiraMove_KeyNotInTreeIs400(t *testing.T) {
	rec := postMove(t, &stubJira{moveErr: jirasvc.ErrKeyNotInIssueTree}, `{"transitionId":"21","issueKey":"OTHER-9"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestJiraMove_RejectionIs400(t *testing.T) {
	rec := postMove(t, &stubJira{moveErr: jiraadapter.ErrBadTransition}, `{"transitionId":"99"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a workflow rejection must be 4xx, got %d", rec.Code)
	}
}

func TestJiraMove_NilServiceNotImplemented(t *testing.T) {
	rec := postMove(t, nil, `{"transitionId":"11"}`)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

// ---- search / projects / link / unlink ----

func TestJiraSearch_ReturnsRowsAndPassesQuery(t *testing.T) {
	stub := &stubJira{searchRes: []jiraadapter.IssueSummary{
		{Key: "DEMO-2272", Type: "Story", Title: "Example issue summary", Status: "Ready for QA", StatusCategory: "new",
			Sprint: &jiraadapter.Sprint{Name: "Sprint 2026-14", State: "active"}},
	}}
	rec := serveJiraReq(t, stub, http.MethodGet,
		"/jira/search?q=eligible&project=DEMO&assignee=acc-123&type=Story,Bug&hideDone=true&activeSprint=1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if stub.gotText != "eligible" || stub.gotProject != "DEMO" {
		t.Errorf("service got text=%q project=%q", stub.gotText, stub.gotProject)
	}
	// The assignee (accountId) and comma-separated types are threaded into the
	// service so it can push them into the server-side JQL.
	if stub.gotAssignee != "acc-123" {
		t.Errorf("service got assignee=%q, want acc-123", stub.gotAssignee)
	}
	if len(stub.gotTypes) != 2 || stub.gotTypes[0] != "Story" || stub.gotTypes[1] != "Bug" {
		t.Errorf("service got types=%v, want [Story Bug]", stub.gotTypes)
	}
	// hideDone=true and activeSprint=1 both parse to true.
	if !stub.gotHideDone || !stub.gotActiveSpr {
		t.Errorf("service got hideDone=%v activeSprint=%v, want both true", stub.gotHideDone, stub.gotActiveSpr)
	}
	var body JiraSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Issues) != 1 || body.Issues[0].Key != "DEMO-2272" || body.Issues[0].Type != "Story" {
		t.Errorf("issues = %+v", body.Issues)
	}
	// The sprint rides the search row so Browse Jira can group by it.
	if body.Issues[0].Sprint == nil || body.Issues[0].Sprint.Name != "Sprint 2026-14" {
		t.Errorf("issue sprint = %+v", body.Issues[0].Sprint)
	}
}

func TestJiraSearch_AdvancedJQLPassthrough(t *testing.T) {
	stub := &stubJira{}
	rec := serveJiraReq(t, stub, http.MethodGet,
		"/jira/search?jql="+url.QueryEscape(`project = STAR AND labels = urgent`), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if stub.gotJQL != `project = STAR AND labels = urgent` {
		t.Errorf("service got jql=%q, want the raw advanced query", stub.gotJQL)
	}
	// Structured params are absent, so the service must have received none of them.
	if stub.gotProject != "" || stub.gotAssignee != "" || len(stub.gotTypes) != 0 {
		t.Errorf("advanced mode must not carry structured params: %+v", stub)
	}
}

func TestJiraSearch_BadQueryIs400(t *testing.T) {
	rec := serveJiraReq(t, &stubJira{searchErr: jiraadapter.ErrBadQuery}, http.MethodGet, "/jira/search?q=%22", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestJiraSearch_NilServiceNotImplemented(t *testing.T) {
	rec := serveJiraReq(t, nil, http.MethodGet, "/jira/search?q=x", nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

func TestJiraIssue_ReturnsFullProjectionByKey(t *testing.T) {
	iss := jiraadapter.Issue{
		Key: "DEMO-102", Type: "Sub-task", Title: "a subtask", Status: "To Do", StatusCategory: "new",
		Parent:   &jiraadapter.ParentRef{Key: "DEMO-101", Title: "Parent story"},
		Subtasks: []jiraadapter.Subtask{},
	}
	stub := &stubJira{issueRes: iss}
	rec := serveJiraReq(t, stub, http.MethodGet, "/jira/issue?key=DEMO-102", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if stub.gotIssueKey != "DEMO-102" {
		t.Errorf("service got key=%q", stub.gotIssueKey)
	}
	var body JiraIssueResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Issue == nil || body.Issue.Key != "DEMO-102" {
		t.Fatalf("issue = %+v", body.Issue)
	}
	// The parent rides the detail projection so the breadcrumb can render + link.
	if body.Issue.Parent == nil || body.Issue.Parent.Key != "DEMO-101" || body.Issue.Parent.Title != "Parent story" {
		t.Errorf("issue parent = %+v", body.Issue.Parent)
	}
}

func TestJiraIssue_KeyRequiredIs400(t *testing.T) {
	rec := serveJiraReq(t, &stubJira{}, http.MethodGet, "/jira/issue", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestJiraIssue_NotFoundIs404(t *testing.T) {
	rec := serveJiraReq(t, &stubJira{issueErr: jiraadapter.ErrNotFound}, http.MethodGet, "/jira/issue?key=DEMO-9", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestJiraIssueTransitions_ListsByKey(t *testing.T) {
	stub := &stubJira{transitions: []jiraadapter.Transition{{ID: "11", Name: "Start", To: "In Progress"}}}
	rec := serveJiraReq(t, stub, http.MethodGet, "/jira/issue/transitions?key=DEMO-102", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if stub.gotIssueTransKey != "DEMO-102" {
		t.Errorf("service got key=%q", stub.gotIssueTransKey)
	}
	var body JiraTransitionsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Transitions) != 1 || body.Transitions[0].ID != "11" {
		t.Errorf("transitions = %+v", body.Transitions)
	}
}

func TestJiraIssueMove_AppliesByKey(t *testing.T) {
	stub := &stubJira{moveRes: jirasvc.MoveResult{Key: "DEMO-102", Status: "In Progress", StatusCategory: "indeterminate"}}
	rec := serveJiraReq(t, stub, http.MethodPost, "/jira/issue/move", strings.NewReader(`{"key":"DEMO-102","transitionId":"11"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if stub.gotIssueMoveKey != "DEMO-102" || stub.gotIssueMoveID != "11" {
		t.Errorf("service got key=%q id=%q", stub.gotIssueMoveKey, stub.gotIssueMoveID)
	}
	var body JiraMoveResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Key != "DEMO-102" || body.Status != "In Progress" {
		t.Errorf("move response = %+v", body)
	}
}

func TestJiraIssueMove_KeyAndTransitionRequired(t *testing.T) {
	// Missing key.
	rec := serveJiraReq(t, &stubJira{}, http.MethodPost, "/jira/issue/move", strings.NewReader(`{"transitionId":"11"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing key: status = %d, want 400", rec.Code)
	}
	// Missing transition id.
	rec = serveJiraReq(t, &stubJira{}, http.MethodPost, "/jira/issue/move", strings.NewReader(`{"key":"DEMO-102"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing transition: status = %d, want 400", rec.Code)
	}
}

func TestJiraProjects_ReturnsList(t *testing.T) {
	rec := serveJiraReq(t, &stubJira{projectRes: []jiraadapter.ProjectRef{{Key: "DEMO", Name: "DEMO project"}}}, http.MethodGet, "/jira/projects?q=demo", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body JiraProjectsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Projects) != 1 || body.Projects[0].Key != "DEMO" || body.Projects[0].Name != "DEMO project" {
		t.Errorf("projects = %+v", body.Projects)
	}
}

func TestJiraMyself_ReturnsAccount(t *testing.T) {
	rec := serveJiraReq(t, &stubJira{me: jiraadapter.CurrentUser{AccountID: "acc-42", DisplayName: "Fluke Sattra"}}, http.MethodGet, "/jira/myself", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body JiraMyselfResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.AccountID != "acc-42" || body.DisplayName != "Fluke Sattra" {
		t.Errorf("myself = %+v", body)
	}
}

func putLink(t *testing.T, svc JiraService, body string) *httptest.ResponseRecorder {
	t.Helper()
	return serveJiraReq(t, svc, http.MethodPut, "/sessions/s1/jira", strings.NewReader(body))
}

func TestJiraLink_BindsAndReturnsIssue(t *testing.T) {
	stub := &stubJira{bindRes: jiraadapter.IssueSummary{Key: "DEMO-2272", Title: "Example issue summary", Status: "Ready for QA", StatusCategory: "new"}}
	rec := putLink(t, stub, `{"issueKey":"DEMO-2272"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if stub.gotBindKey != "DEMO-2272" {
		t.Errorf("service got key %q", stub.gotBindKey)
	}
	var body JiraLinkResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Linked || body.Issue == nil || body.Issue.Key != "DEMO-2272" || body.SessionID != "s1" {
		t.Errorf("link response = %+v", body)
	}
}

func TestJiraLink_MissingKeyIs400(t *testing.T) {
	rec := putLink(t, &stubJira{}, `{"issueKey":"  "}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestJiraLink_MalformedBodyIs400(t *testing.T) {
	rec := putLink(t, &stubJira{}, `nope`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestJiraLink_UnknownKeyIs404(t *testing.T) {
	rec := putLink(t, &stubJira{bindErr: jiraadapter.ErrNotFound}, `{"issueKey":"DEMO-9"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestJiraLink_NilServiceNotImplemented(t *testing.T) {
	rec := putLink(t, nil, `{"issueKey":"DEMO-1"}`)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

func TestJiraUnlink_OK(t *testing.T) {
	rec := serveJiraReq(t, &stubJira{}, http.MethodDelete, "/sessions/s1/jira", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body JiraLinkResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Linked || body.SessionID != "s1" {
		t.Errorf("unlink response = %+v, want linked=false", body)
	}
}

func TestJiraUnlink_NotLinkedIs400(t *testing.T) {
	rec := serveJiraReq(t, &stubJira{unlinkErr: jirasvc.ErrNotLinked}, http.MethodDelete, "/sessions/s1/jira", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestJiraUnlink_NilServiceNotImplemented(t *testing.T) {
	rec := serveJiraReq(t, nil, http.MethodDelete, "/sessions/s1/jira", nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}
