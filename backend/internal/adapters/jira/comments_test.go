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

// Evidence transfers are megabytes over whatever link the user is on; the 15s
// budget that suits a JSON read aborts a screen recording mid-upload, which
// fails the whole Post-to-Jira (or silently drops the file's inline preview).
func TestTransferClientGetsALongerBudgetThanReads(t *testing.T) {
	if defaultTransferHTTPClient.Timeout <= defaultHTTPClient.Timeout {
		t.Fatalf("transfer timeout = %s, want more than the read timeout %s",
			defaultTransferHTTPClient.Timeout, defaultHTTPClient.Timeout)
	}
}

// One injected doer must still capture every call, transfers included, or a test
// server silently stops seeing the uploads it is asserting on.
func TestWithHTTPDoerAlsoCapturesTransfers(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `[{"id":"1","filename":"a.png","mimeType":"image/png","content":"https://x/1/a.png"}]`)
	}))
	defer srv.Close()
	doer := func(req *http.Request) (*http.Response, error) {
		calls++
		return srv.Client().Do(req)
	}

	c := NewClient(WithHTTPDoer(doer), WithConfigSource(staticConfig(srv.URL)))
	if _, err := c.AddAttachment(context.Background(), "DEMO-101", "a.png", "image/png", strings.NewReader("x")); err != nil {
		t.Fatalf("AddAttachment: %v", err)
	}
	if calls != 1 {
		t.Fatalf("injected doer calls = %d, want 1", calls)
	}
}

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

func TestResolveMediaID_ParsesMediaUUID(t *testing.T) {
	const uuid = "12345678-1234-1234-1234-123456789abc"

	t.Run("location header (redirect=false)", func(t *testing.T) {
		var gotPath, gotQuery, gotMethod string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath, gotQuery, gotMethod = r.URL.Path, r.URL.RawQuery, r.Method
			w.Header().Set("Location", "https://api.media.atlassian.com/file/"+uuid+"/binary?token=abc")
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()
		c := NewClient(WithHTTPDoer(srv.Client().Do), WithConfigSource(staticConfig(srv.URL)))
		got, err := c.ResolveMediaID(context.Background(), "10101")
		if err != nil {
			t.Fatalf("ResolveMediaID: %v", err)
		}
		if got != uuid {
			t.Errorf("media id = %q, want %q", got, uuid)
		}
		if gotMethod != http.MethodGet || gotPath != "/rest/api/3/attachment/content/10101" {
			t.Errorf("method/path = %q %q", gotMethod, gotPath)
		}
		// Must NOT pass redirect=false — that returns the raw bytes, not the URL.
		if gotQuery != "" {
			t.Errorf("query = %q, want empty (no redirect=false)", gotQuery)
		}
	})

	t.Run("followed redirect (final URL)", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/file/") {
				w.WriteHeader(http.StatusOK)
				return
			}
			http.Redirect(w, r, "/file/"+uuid+"/binary?token=abc", http.StatusFound)
		}))
		defer srv.Close()
		c := NewClient(WithHTTPDoer(srv.Client().Do), WithConfigSource(staticConfig(srv.URL)))
		got, err := c.ResolveMediaID(context.Background(), "10101")
		if err != nil {
			t.Fatalf("ResolveMediaID: %v", err)
		}
		if got != uuid {
			t.Errorf("media id = %q, want %q", got, uuid)
		}
	})

	t.Run("body carries the url", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"url":"https://api.media.atlassian.com/file/`+uuid+`/binary?token=abc"}`)
		}))
		defer srv.Close()
		c := NewClient(WithHTTPDoer(srv.Client().Do), WithConfigSource(staticConfig(srv.URL)))
		got, err := c.ResolveMediaID(context.Background(), "10101")
		if err != nil {
			t.Fatalf("ResolveMediaID: %v", err)
		}
		if got != uuid {
			t.Errorf("media id = %q, want %q", got, uuid)
		}
	})

	t.Run("not found maps to sentinel", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()
		c := NewClient(WithHTTPDoer(srv.Client().Do), WithConfigSource(staticConfig(srv.URL)))
		if _, err := c.ResolveMediaID(context.Background(), "10101"); !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("no id in response is unavailable", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"nothing":"useful"}`)
		}))
		defer srv.Close()
		c := NewClient(WithHTTPDoer(srv.Client().Do), WithConfigSource(staticConfig(srv.URL)))
		if _, err := c.ResolveMediaID(context.Background(), "10101"); !errors.Is(err, ErrUnavailable) {
			t.Errorf("err = %v, want ErrUnavailable", err)
		}
	})
}

func TestDownloadAttachment_FollowsRedirectAndStreams(t *testing.T) {
	media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("PNGBYTES"))
	}))
	defer media.Close()

	var gotPath string
	jira := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.URL.Path != "/rest/api/3/attachment/content/173517" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Redirect(w, r, media.URL+"/file/uuid/binary?token=abc", http.StatusSeeOther)
	}))
	defer jira.Close()

	c := NewClient(WithHTTPDoer(jira.Client().Do), WithConfigSource(staticConfig(jira.URL)))
	rc, ctype, err := c.DownloadAttachment(context.Background(), "173517")
	if err != nil {
		t.Fatalf("DownloadAttachment: %v", err)
	}
	defer func() { _ = rc.Close() }()
	body, _ := io.ReadAll(rc)
	if string(body) != "PNGBYTES" {
		t.Errorf("body = %q, want PNGBYTES", body)
	}
	if ctype != "image/png" {
		t.Errorf("content-type = %q, want image/png", ctype)
	}
	if gotPath != "/rest/api/3/attachment/content/173517" {
		t.Errorf("path = %q", gotPath)
	}
}

func TestDownloadAttachment_EmptyIDRejectedBeforeHTTP(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := NewClient(WithHTTPDoer(srv.Client().Do), WithConfigSource(staticConfig(srv.URL)))
	if _, _, err := c.DownloadAttachment(context.Background(), "  "); !errors.Is(err, ErrBadRequest) {
		t.Errorf("err = %v, want ErrBadRequest", err)
	}
	if called {
		t.Error("HTTP called for empty id")
	}
}

func TestDownloadAttachment_StatusErrorMapsToSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"errorMessages":["gone"]}`, http.StatusNotFound)
	}))
	defer srv.Close()
	c := NewClient(WithHTTPDoer(srv.Client().Do), WithConfigSource(staticConfig(srv.URL)))
	if _, _, err := c.DownloadAttachment(context.Background(), "999"); err == nil {
		t.Fatal("want error for 404")
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
