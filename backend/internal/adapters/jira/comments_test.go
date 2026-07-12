package jira

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAddAttachment_UploadsMultipart(t *testing.T) {
	var gotMethod, gotPath, gotXsrf, gotAuth, gotCT, gotFilename, gotFileBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotXsrf = r.Header.Get("X-Atlassian-Token")
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Errorf("FormFile(file): %v", err)
		} else {
			gotFilename = header.Filename
			b, _ := io.ReadAll(file)
			gotFileBody = string(b)
		}
		_, _ = io.WriteString(w, `[{"id":"10101","filename":"shot.png","mimeType":"image/png","content":"https://acme.atlassian.net/secure/attachment/10101/shot.png"}]`)
	}))
	defer srv.Close()

	c := NewClient(WithHTTPDoer(srv.Client().Do), WithConfigSource(staticConfig(srv.URL)))
	att, err := c.AddAttachment(context.Background(), "DEMO-101", "shot.png", "image/png", strings.NewReader("PNGBYTES"))
	if err != nil {
		t.Fatalf("AddAttachment: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/rest/api/3/issue/DEMO-101/attachments" {
		t.Errorf("method/path = %q %q", gotMethod, gotPath)
	}
	if gotXsrf != "no-check" {
		t.Errorf("X-Atlassian-Token = %q, want no-check", gotXsrf)
	}
	if !strings.HasPrefix(gotAuth, "Basic ") {
		t.Errorf("auth = %q, want basic", gotAuth)
	}
	if !strings.HasPrefix(gotCT, "multipart/form-data") {
		t.Errorf("content-type = %q, want multipart/form-data", gotCT)
	}
	if gotFilename != "shot.png" || gotFileBody != "PNGBYTES" {
		t.Errorf("uploaded file = %q / %q", gotFilename, gotFileBody)
	}
	if att.ID != "10101" || att.Filename != "shot.png" || att.MimeType != "image/png" ||
		att.ContentURL != "https://acme.atlassian.net/secure/attachment/10101/shot.png" {
		t.Errorf("attachment = %+v", att)
	}
}

func TestAddAttachment_BadKeyNeverCallsHTTP(t *testing.T) {
	c := NewClient(WithHTTPDoer(func(*http.Request) (*http.Response, error) {
		t.Fatal("HTTP must not be called for a malformed key")
		return nil, nil
	}), WithConfigSource(staticConfig("http://x")))
	if _, err := c.AddAttachment(context.Background(), "not a key", "f.png", "image/png", strings.NewReader("x")); !errors.Is(err, ErrBadKey) {
		t.Errorf("err = %v, want ErrBadKey", err)
	}
}

func TestAddComment_PostsADFBody(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = io.WriteString(w, `{"id":"90001","self":"`+srvSelf(r)+`/rest/api/3/issue/10/comment/90001"}`)
	}))
	defer srv.Close()

	c := NewClient(WithHTTPDoer(srv.Client().Do), WithConfigSource(staticConfig(srv.URL)))
	doc := map[string]any{"type": "doc", "version": 1, "content": []any{
		map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "hi"}}},
	}}
	cm, err := c.AddComment(context.Background(), "DEMO-101", doc)
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/rest/api/3/issue/DEMO-101/comment" {
		t.Errorf("method/path = %q %q", gotMethod, gotPath)
	}
	// The ADF doc is wrapped under "body".
	if !strings.Contains(gotBody, `"body"`) || !strings.Contains(gotBody, `"doc"`) || !strings.Contains(gotBody, `"text":"hi"`) {
		t.Errorf("body = %q", gotBody)
	}
	if cm.ID != "90001" {
		t.Errorf("comment id = %q", cm.ID)
	}
	if !strings.Contains(cm.URL, "/browse/DEMO-101") || !strings.Contains(cm.URL, "focusedCommentId=90001") {
		t.Errorf("comment url = %q", cm.URL)
	}
}

func TestAddComment_StatusErrorsMapToSentinels(t *testing.T) {
	cases := []struct {
		code int
		body string
		want error
	}{
		{http.StatusBadRequest, `{"errorMessages":["INVALID_INPUT: media not found"]}`, ErrBadRequest},
		{http.StatusUnauthorized, `{"errorMessages":["auth"]}`, ErrAuthFailed},
		{http.StatusForbidden, `{"errorMessages":["no write scope"]}`, ErrAuthFailed},
		{http.StatusNotFound, ``, ErrNotFound},
		{http.StatusInternalServerError, `boom`, ErrUnavailable},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.code)
			_, _ = io.WriteString(w, tc.body)
		}))
		c := NewClient(WithHTTPDoer(srv.Client().Do), WithConfigSource(staticConfig(srv.URL)))
		_, err := c.AddComment(context.Background(), "DEMO-1", map[string]any{"type": "doc"})
		if !errors.Is(err, tc.want) {
			t.Errorf("status %d: err = %v, want %v", tc.code, err, tc.want)
		}
		srv.Close()
	}
}

// srvSelf returns the scheme://host of the request so the fake comment self URL
// points back at the test server (browseURL derives the browse base from it).
func srvSelf(r *http.Request) string {
	return "http://" + r.Host
}
