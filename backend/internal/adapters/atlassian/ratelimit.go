package atlassian

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ErrRateLimited is the class of a 429; *RateLimitError carries the wait.
var ErrRateLimited = errors.New("jira: rate limited")

// RateLimitError is a 429 from an Atlassian site. RetryAfter is zero when the
// server named no usable wait.
type RateLimitError struct {
	RetryAfter time.Duration
	Message    string
}

func (e *RateLimitError) Error() string {
	if e.Message == "" {
		return ErrRateLimited.Error()
	}
	return ErrRateLimited.Error() + ": " + e.Message
}

// Is matches ErrRateLimited.
func (e *RateLimitError) Is(target error) bool { return target == ErrRateLimited }

// ParseRetryAfter reads a Retry-After header as delta seconds or an HTTP date.
// A missing, malformed or past value is zero.
func ParseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if sec, err := strconv.Atoi(v); err == nil {
		if sec < 0 {
			return 0
		}
		return time.Duration(sec) * time.Second
	}
	if at, err := http.ParseTime(v); err == nil && at.After(now) {
		return at.Sub(now)
	}
	return 0
}
