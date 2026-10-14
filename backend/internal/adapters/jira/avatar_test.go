package jira

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestAllowedAvatarHost(t *testing.T) {
	cases := map[string]bool{
		"example.atlassian.net": true,
		"avatar-management--avatars.us-west-2.prod.public.atl-paas.net":    true,
		"avatar-management--avatars.eu-central-1.prod.public.atl-paas.net": true,
		"secure.gravatar.com":                    false,
		"evil.public.atl-paas.net":               false,
		"avatar-management--avatars.example.com": false,
		"example.atlassian.net.evil.com":         false,
	}
	for host, want := range cases {
		if got := allowedAvatarHost(host, "example.atlassian.net"); got != want {
			t.Errorf("allowedAvatarHost(%q) = %v, want %v", host, got, want)
		}
	}
}

// avatarClient serves the site at site and routes any other host through cdn,
// so the test can play both the configured site and the avatar CDN.
func avatarClient(t *testing.T, site, cdn http.HandlerFunc) (c *Client, siteURL string, sent *[]*http.Request) {
	t.Helper()
	siteSrv := httptest.NewServer(site)
	t.Cleanup(siteSrv.Close)
	cdnSrv := httptest.NewServer(cdn)
	t.Cleanup(cdnSrv.Close)
	var reqs []*http.Request
	do := func(r *http.Request) (*http.Response, error) {
		reqs = append(reqs, r)
		target := siteSrv
		if r.URL.Host != strings.TrimPrefix(siteSrv.URL, "http://") {
			target = cdnSrv
		}
		u, _ := url.Parse(target.URL + r.URL.RequestURI())
		out := r.Clone(r.Context())
		out.URL, out.Host, out.RequestURI = u, u.Host, ""
		resp, err := target.Client().Do(out)
		if resp != nil {
			resp.Request = r
		}
		return resp, err
	}
	c = NewClient(WithHTTPDoer(do), WithConfigSource(staticConfig(siteSrv.URL)))
	return c, siteSrv.URL, &reqs
}

func png(n int) []byte { return append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, n)...) }

func TestAvatarFromTheSiteSendsAuth(t *testing.T) {
	c, site, sent := avatarClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png(100))
	}, http.NotFound)
	a, err := c.Avatar(context.Background(), site+"/secure/useravatar?size=large&ownerId=acc-1")
	if err != nil {
		t.Fatal(err)
	}
	if a.ContentType != "image/png" || len(a.Bytes) != 108 {
		t.Errorf("avatar = %s, %d bytes", a.ContentType, len(a.Bytes))
	}
	if (*sent)[0].Header.Get("Authorization") == "" {
		t.Error("a site avatar request must carry the site's auth")
	}
}

func TestAvatarFromTheCDNSendsNoAuth(t *testing.T) {
	c, _, sent := avatarClient(t, http.NotFound, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png(10))
	})
	_, err := c.Avatar(context.Background(), "https://avatar-management--avatars.us-west-2.prod.public.atl-paas.net/acc-1/48")
	if err != nil {
		t.Fatal(err)
	}
	if (*sent)[0].Header.Get("Authorization") != "" {
		t.Error("the CDN must never see the site credential")
	}
}

func TestAvatarRefusesOtherHostsBeforeHTTP(t *testing.T) {
	c := NewClient(WithHTTPDoer(func(*http.Request) (*http.Response, error) {
		t.Fatal("HTTP must not be called")
		return nil, nil
	}), WithConfigSource(staticConfig("https://example.atlassian.net")))
	for _, u := range []string{
		"https://secure.gravatar.com/avatar/abc",
		"http://avatar-management--avatars.us-west-2.prod.public.atl-paas.net/a",
		"https://example.atlassian.net.evil.com/a",
		"not a url",
		"",
	} {
		if _, err := c.Avatar(context.Background(), u); !errors.Is(err, ErrBadRequest) {
			t.Errorf("Avatar(%q) err = %v, want ErrBadRequest", u, err)
		}
	}
}

func TestAvatarRefusesARedirectOffTheAllowlist(t *testing.T) {
	c := NewClient(WithHTTPDoer(func(r *http.Request) (*http.Response, error) {
		landed, _ := url.Parse("https://secure.gravatar.com/avatar/abc")
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"image/png"}},
			Body: io.NopCloser(bytes.NewReader(png(10))), Request: &http.Request{URL: landed},
		}, nil
	}), WithConfigSource(staticConfig("https://example.atlassian.net")))
	if _, err := c.Avatar(context.Background(), "https://example.atlassian.net/a"); !errors.Is(err, ErrBadRequest) {
		t.Errorf("err = %v, want ErrBadRequest", err)
	}
}

func TestAvatarCapsTheSize(t *testing.T) {
	c, site, _ := avatarClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png(maxAvatarBytes))
	}, http.NotFound)
	_, err := c.Avatar(context.Background(), site+"/a")
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "1048576") {
		t.Errorf("err = %v, want an over-cap error", err)
	}
}

func TestAvatarStatusErrors(t *testing.T) {
	cases := []struct {
		code   int
		header string
		want   error
	}{
		{http.StatusUnauthorized, "", ErrAuthFailed},
		{http.StatusNotFound, "", ErrNotFound},
		{http.StatusTooManyRequests, "30", ErrRateLimited},
		{http.StatusInternalServerError, "", ErrUnavailable},
	}
	for _, tc := range cases {
		c, site, _ := avatarClient(t, func(w http.ResponseWriter, _ *http.Request) {
			if tc.header != "" {
				w.Header().Set("Retry-After", tc.header)
			}
			w.WriteHeader(tc.code)
		}, http.NotFound)
		_, err := c.Avatar(context.Background(), site+"/a?token=x")
		if !errors.Is(err, tc.want) {
			t.Errorf("%d: err = %v, want %v", tc.code, err, tc.want)
		}
		var rl *RateLimitError
		if errors.As(err, &rl) && rl.RetryAfter != 30*time.Second {
			t.Errorf("RetryAfter = %v", rl.RetryAfter)
		}
		if err != nil && strings.Contains(err.Error(), "token=x") {
			t.Errorf("error text carries the URL query: %v", err)
		}
	}
}
