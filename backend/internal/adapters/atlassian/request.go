package atlassian

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
)

// NewRequest builds an authenticated Atlassian Cloud REST request: Accept JSON,
// Content-Type JSON when there is a body, and basic auth from cfg. rawURL is
// fully built by the caller (query already encoded).
func NewRequest(ctx context.Context, cfg Config, method, rawURL string, body []byte) (*http.Request, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rdr)
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %w", ErrUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(cfg.Email+":"+cfg.Token)))
	return req, nil
}
