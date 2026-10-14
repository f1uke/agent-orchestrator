package jira

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/atlassian"
)

// maxAvatarBytes caps one avatar download.
const maxAvatarBytes = 1 << 20

// AvatarImage is a downloaded avatar.
type AvatarImage struct {
	Bytes       []byte
	ContentType string
}

// Avatar downloads a user avatar from the configured site, with its auth, or
// from Atlassian's avatar CDN, without it. Any other host is refused, before
// the request and again after redirects.
func (c *Client) Avatar(ctx context.Context, rawURL string) (AvatarImage, error) {
	cfg, err := c.config()
	if err != nil {
		return AvatarImage{}, err
	}
	site, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return AvatarImage{}, fmt.Errorf("%w: site URL: %w", ErrUnavailable, err)
	}
	u, err := url.Parse(rawURL)
	if err != nil || !allowedAvatarURL(u, site) {
		return AvatarImage{}, fmt.Errorf("%w: avatar host not allowed", ErrBadRequest)
	}
	req, err := atlassian.NewRequest(ctx, cfg, http.MethodGet, u.String(), nil)
	if err != nil {
		return AvatarImage{}, err
	}
	req.Header.Set("Accept", "image/*")
	if u.Host != site.Host {
		req.Header.Del("Authorization")
	}
	resp, err := c.httpDo(req)
	if err != nil {
		return AvatarImage{}, transportError("avatar", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.Request != nil && !allowedAvatarURL(resp.Request.URL, site) {
		return AvatarImage{}, fmt.Errorf("%w: avatar redirected to a host that is not allowed", ErrBadRequest)
	}
	if err := readStatusError(resp, "avatar", errBadRequest); err != nil {
		return AvatarImage{}, err
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxAvatarBytes+1))
	if err != nil {
		return AvatarImage{}, transportError("avatar", err)
	}
	if len(b) > maxAvatarBytes {
		return AvatarImage{}, fmt.Errorf("%w: avatar larger than %d bytes", ErrUnavailable, maxAvatarBytes)
	}
	return AvatarImage{Bytes: b, ContentType: resp.Header.Get("Content-Type")}, nil
}

func allowedAvatarURL(u, site *url.URL) bool {
	if u == nil || u.Host == "" || !allowedAvatarHost(u.Host, site.Host) {
		return false
	}
	if u.Host == site.Host {
		return u.Scheme == site.Scheme
	}
	return u.Scheme == "https"
}

// allowedAvatarHost is the configured site or Atlassian's avatar CDN
// (avatar-management--avatars.<region>.prod.public.atl-paas.net).
func allowedAvatarHost(host, siteHost string) bool {
	if host == siteHost {
		return true
	}
	return strings.HasPrefix(host, "avatar-management--avatars.") && strings.HasSuffix(host, ".public.atl-paas.net")
}
