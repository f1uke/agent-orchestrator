package atlassian

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	cases := map[string]time.Duration{
		"30": 30 * time.Second,
		now.Add(90 * time.Second).Format(http.TimeFormat): 90 * time.Second,
		"":    0,
		"-1":  0,
		"bad": 0,
		now.Add(-time.Minute).Format(http.TimeFormat): 0,
	}
	for in, want := range cases {
		if got := ParseRetryAfter(in, now); got != want {
			t.Errorf("ParseRetryAfter(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestRateLimitErrorMatchesErrRateLimited(t *testing.T) {
	var err error = &RateLimitError{RetryAfter: time.Second, Message: "slow down"}
	if !errors.Is(err, ErrRateLimited) {
		t.Error("errors.Is(err, ErrRateLimited) = false")
	}
	if errors.Is(err, ErrUnavailable) || errors.Is(err, ErrAuthFailed) {
		t.Error("a 429 must match no other class")
	}
	if err.Error() != "jira: rate limited: slow down" {
		t.Errorf("Error() = %q", err.Error())
	}
}
