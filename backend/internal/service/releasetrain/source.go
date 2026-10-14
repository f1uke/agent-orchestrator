package releasetrain

import (
	"context"
	"encoding/json"
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
	Credentials(ctx context.Context, view rt.PlanView) (bool, error)
}

// SettledBudget is how often a settled version's per-entity reads refetch.
const SettledBudget = 6 * time.Hour

// IsDecodeError reports whether err is a response the source could not parse.
func IsDecodeError(err error) bool {
	var syntax *json.SyntaxError
	var typ *json.UnmarshalTypeError
	var parse *time.ParseError
	return errors.As(err, &syntax) || errors.As(err, &typ) || errors.As(err, &parse)
}

// Job is one planned resource and the read that fetches it.
type Job struct {
	Spec  rt.ResourceSpec
	Fetch func(ctx context.Context, cursor []byte) (Batch, error)
}

// FetchJob runs the job planned for spec.
func FetchJob(ctx context.Context, jobs []Job, spec rt.ResourceSpec, cursor []byte) (Batch, error) {
	for _, j := range jobs {
		if j.Spec.Kind == spec.Kind && j.Spec.Scope == spec.Scope {
			return j.Fetch(ctx, cursor)
		}
	}
	return Batch{}, &FetchError{Class: rt.ErrNotFound, Err: fmt.Errorf("%s %s is no longer planned", spec.Kind, spec.Scope)}
}

// Specs lists the jobs' specs.
func Specs(jobs []Job) []rt.ResourceSpec {
	out := make([]rt.ResourceSpec, len(jobs))
	for i, j := range jobs {
		out[i] = j.Spec
	}
	return out
}

// SnapshotOf encodes a complete snapshot of facts of kind k.
func SnapshotOf[T rt.Keyed](k rt.Kind[T], facts []T) (Batch, error) {
	items, err := encodeAll(k, facts)
	return Batch{Items: items, Complete: true}, err
}

func encodeAll[T rt.Keyed](k rt.Kind[T], facts []T) ([]rt.Item, error) {
	items := make([]rt.Item, 0, len(facts))
	for _, f := range facts {
		it, err := rt.EncodeItem(k, f, nil)
		if err != nil {
			return nil, &FetchError{Class: rt.ErrDecode, Err: err}
		}
		items = append(items, it)
	}
	return items, nil
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

// ErrNotARead is a request the monitor refused to send because it was not a GET.
var ErrNotARead = errors.New("release trains: only GET requests may leave the monitor")

// CountingTransport counts each request against the pass's budget and refuses any method but GET.
type CountingTransport struct{ Base http.RoundTripper }

// RoundTrip implements http.RoundTripper.
func (t CountingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, fmt.Errorf("%w: %s %s", ErrNotARead, req.Method, req.URL.Path)
	}
	CountRequest(req.Context())
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

func specKey(s rt.ResourceSpec) string { return fmt.Sprintf("%s %s", s.Kind, s.Scope) }
