package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// CurrentUser is the Jira account that owns the configured API token — used to
// highlight "your" issues in Browse Jira. AccountID is the opaque id the issue
// `assignee.accountId` is compared against; DisplayName is for the label.
type CurrentUser struct {
	AccountID   string
	DisplayName string
}

// Myself returns the authenticated account (GET /rest/api/3/myself) over the same
// auth seam as search/transitions. The account id is stable, so callers cache it.
func (c *Client) Myself(ctx context.Context) (CurrentUser, error) {
	cfg, err := c.config()
	if err != nil {
		return CurrentUser{}, err
	}
	req, err := newJiraRequest(ctx, cfg, http.MethodGet, cfg.baseURL+"/rest/api/3/myself", nil)
	if err != nil {
		return CurrentUser{}, err
	}
	resp, err := c.httpDo(req)
	if err != nil {
		return CurrentUser{}, fmt.Errorf("%w: myself: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := statusError(resp, "myself"); err != nil {
		return CurrentUser{}, err
	}
	var payload struct {
		AccountID   string `json:"accountId"`
		DisplayName string `json:"displayName"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return CurrentUser{}, fmt.Errorf("%w: decode myself: %w", ErrUnavailable, err)
	}
	return CurrentUser{AccountID: payload.AccountID, DisplayName: payload.DisplayName}, nil
}
