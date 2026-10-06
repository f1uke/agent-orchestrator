package jira

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Attachment downloads are megabytes over whatever link the user is on; the 15s
// budget that suits a JSON read aborts a screen recording mid-download.
func TestTransferClientGetsALongerBudgetThanReads(t *testing.T) {
	if defaultTransferHTTPClient.Timeout <= defaultHTTPClient.Timeout {
		t.Fatalf("transfer timeout = %s, want more than the read timeout %s",
			defaultTransferHTTPClient.Timeout, defaultHTTPClient.Timeout)
	}
}

// One injected doer must still capture every call, transfers included, or a test
// server silently stops seeing the downloads it is asserting on.
func TestWithHTTPDoerAlsoCapturesTransfers(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "PNGBYTES")
	}))
	defer srv.Close()
	doer := func(req *http.Request) (*http.Response, error) {
		calls++
		return srv.Client().Do(req)
	}

	c := NewClient(WithHTTPDoer(doer), WithConfigSource(staticConfig(srv.URL)))
	rc, _, err := c.DownloadAttachment(context.Background(), "173517")
	if err != nil {
		t.Fatalf("DownloadAttachment: %v", err)
	}
	_ = rc.Close()
	if calls != 1 {
		t.Fatalf("injected doer calls = %d, want 1", calls)
	}
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

func TestDownloadAttachment_StatusErrorsMapToSentinels(t *testing.T) {
	cases := []struct {
		code int
		body string
		want error
	}{
		{http.StatusBadRequest, `{"errorMessages":["bad id"]}`, ErrBadRequest},
		{http.StatusUnauthorized, `{"errorMessages":["auth"]}`, ErrAuthFailed},
		{http.StatusForbidden, `{"errorMessages":["no read scope"]}`, ErrAuthFailed},
		{http.StatusNotFound, ``, ErrNotFound},
		{http.StatusInternalServerError, `boom`, ErrUnavailable},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.code)
			_, _ = io.WriteString(w, tc.body)
		}))
		c := NewClient(WithHTTPDoer(srv.Client().Do), WithConfigSource(staticConfig(srv.URL)))
		_, _, err := c.DownloadAttachment(context.Background(), "999")
		if !errors.Is(err, tc.want) {
			t.Errorf("status %d: err = %v, want %v", tc.code, err, tc.want)
		}
		srv.Close()
	}
}
