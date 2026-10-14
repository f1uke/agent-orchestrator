package releasetrain

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sync/atomic"
	"time"
	"unicode/utf8"

	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
)

// Source plans and fetches one system's resources. It owns its fetch policy,
// not freshness, backoff or storage.
type Source interface {
	// System names the host for request budgets and pauses: "gitlab" or "jira".
	System() string
	Plan(view rt.PlanView) []rt.ResourceSpec
	// Fetch reads one resource. An incremental resource returns one page per call.
	Fetch(ctx context.Context, spec rt.ResourceSpec, view rt.PlanView, cursor []byte) (Batch, error)
	Credentials(ctx context.Context) (bool, error)
}

// Batch is what one Fetch read.
type Batch struct {
	Items []rt.Item
	// Complete is true for a whole snapshot, or for the page that caught an incremental resource up.
	Complete bool
	Cursor   []byte
}

// FetchError is a source's classification of a failed read.
type FetchError struct {
	Class      rt.ErrorClass
	RetryAfter time.Duration
	Err        error
}

func (e *FetchError) Error() string { return string(e.Class) + ": " + e.Err.Error() }

func (e *FetchError) Unwrap() error { return e.Err }

func classify(err error) *FetchError {
	var fe *FetchError
	if errors.As(err, &fe) {
		return fe
	}
	return &FetchError{Class: rt.ErrUnavailable, Err: err}
}

var errIncompleteBatch = errors.New("the source returned an incomplete snapshot")

const maxErrorMessage = 300

var queryRE = regexp.MustCompile(`\?[^\s"']*`)

func sanitize(msg string) string {
	msg = queryRE.ReplaceAllString(msg, "")
	if len(msg) <= maxErrorMessage {
		return msg
	}
	cut := maxErrorMessage
	for cut > 0 && !utf8.RuneStart(msg[cut]) {
		cut--
	}
	return msg[:cut]
}

type requestCounterKey struct{}

// CountRequest adds one HTTP request to the pass's per-host budget in ctx.
func CountRequest(ctx context.Context) {
	if c, ok := ctx.Value(requestCounterKey{}).(*atomic.Int64); ok {
		c.Add(1)
	}
}

func withRequestCounter(ctx context.Context, c *atomic.Int64) context.Context {
	return context.WithValue(ctx, requestCounterKey{}, c)
}

// CountingTransport counts every request it sends against the pass's budget.
type CountingTransport struct{ Base http.RoundTripper }

// RoundTrip implements http.RoundTripper.
func (t CountingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	CountRequest(req.Context())
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

func specKey(s rt.ResourceSpec) string { return fmt.Sprintf("%s %s", s.Kind, s.Scope) }
