package controllers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
	rtsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain"
)

// ReleaseTrainsService backs ReleaseTrainsController.
type ReleaseTrainsService interface {
	Report(ctx context.Context) (rtsvc.Report, error)
	// InitConfig returns rtsvc.ErrConfigExists when the config file is already there.
	InitConfig() (string, error)
	// Board returns the serialized ReleaseTrainBoardResponse and its ETag, or
	// rtsvc.ErrNotModified when ifNoneMatch names it.
	Board(ctx context.Context, ifNoneMatch string) ([]byte, string, error)
	// Refresh returns rtsvc.ErrUnknownSource for a source no poller has.
	Refresh(source string) error
	// SetRule returns rtsvc.ErrUnknownRule for an id no rule has.
	SetRule(ctx context.Context, id rt.RuleID, enabled *bool) (rt.RuleInfo, error)
	// Hide returns rtsvc.ErrUnknownHit for a hit not on the current Board.
	Hide(ctx context.Context, id rt.HitID, reason string) (rt.Hide, error)
	// Unhide returns rtsvc.ErrUnknownHit for a hit that is not hidden.
	Unhide(ctx context.Context, id rt.HitID) error
	UnhideStale(ctx context.Context) ([]rt.HitID, error)
	Plans(ctx context.Context) ([]rt.LlbPlan, error)
	// PutPlan returns rt.ErrInvalidPlan, *rt.PlanVersionError or *rt.BranchTakenError.
	PutPlan(ctx context.Context, p rt.LlbPlan, ifVersion int) (rt.LlbPlan, error)
	// Avatar returns rtsvc.ErrNoAvatar until the account's avatar is downloaded, rtsvc.ErrBadAccountID for an id no file can have.
	Avatar(accountID string) ([]byte, error)
}

// ReleaseTrainAccountIDParam is the path of GET /api/v1/release-trains/avatars/{accountId}.
type ReleaseTrainAccountIDParam struct {
	AccountID string `path:"accountId" description:"Jira account id, percent-encoded (its colon may be %3A)."`
}

// ReleaseTrainBoardResponse is the body of GET /api/v1/release-trains/board.
type ReleaseTrainBoardResponse = rtsvc.BoardResponse

// ReleaseTrainRefreshRequest is the body of POST /api/v1/release-trains/refresh.
type ReleaseTrainRefreshRequest struct {
	Source string `json:"source,omitempty" enum:"gitlab,jira" description:"Refresh one source; every source when absent."`
}

// ReleaseTrainRefreshResponse is the body of a 202 from POST /api/v1/release-trains/refresh.
type ReleaseTrainRefreshResponse struct {
	Source string `json:"source,omitempty"`
}

// ReleaseTrainsController owns the /release-trains routes.
type ReleaseTrainsController struct {
	Svc ReleaseTrainsService
}

// ReleaseTrainConfigDTO is the config file's state.
type ReleaseTrainConfigDTO struct {
	State   string `json:"state" enum:"absent,invalid,ok"`
	Message string `json:"message,omitempty" description:"Why the file is invalid, or how to create it when absent."`
	Path    string `json:"path"`
}

// ReleaseTrainResourceDTO is one observed resource's freshness.
type ReleaseTrainResourceDTO struct {
	Kind          string     `json:"kind"`
	Scope         string     `json:"scope"`
	App           string     `json:"app,omitempty"`
	Mode          string     `json:"mode" enum:"snapshot,incremental"`
	CompleteAt    *time.Time `json:"completeAt,omitempty" description:"When the last complete fetch finished; absent until one has."`
	LastAttemptAt *time.Time `json:"lastAttemptAt,omitempty"`
	ErrorClass    string     `json:"errorClass,omitempty" enum:"auth,rate_limited,unavailable,not_found,decode,truncated,unconfigured"`
	ErrorMessage  string     `json:"errorMessage,omitempty" description:"Sanitized: no query strings, at most 300 bytes."`
	NextAttemptAt *time.Time `json:"nextAttemptAt,omitempty"`
	Backfilling   bool       `json:"backfilling" description:"An incremental resource that has a cursor but has not caught up once."`
}

// ReleaseTrainPassDTO is what the last poll pass did.
type ReleaseTrainPassDTO struct {
	At       *time.Time     `json:"at,omitempty"`
	Requests map[string]int `json:"requests" description:"HTTP requests sent per host (gitlab, jira) in the last pass."`
}

// ReleaseTrainSourcesResponse is the body of GET /api/v1/release-trains/sources.
type ReleaseTrainSourcesResponse struct {
	Config    ReleaseTrainConfigDTO     `json:"config"`
	Resources []ReleaseTrainResourceDTO `json:"resources"`
	LastPass  ReleaseTrainPassDTO       `json:"lastPass"`
}

// ReleaseTrainConfigInitResponse is the body of POST /api/v1/release-trains/config.
type ReleaseTrainConfigInitResponse struct {
	Path string `json:"path"`
}

// Register mounts the release-train routes.
func (c *ReleaseTrainsController) Register(r chi.Router) {
	r.Get("/release-trains/sources", c.sources)
	r.Post("/release-trains/config", c.initConfig)
	r.Get("/release-trains/board", c.board)
	r.Post("/release-trains/refresh", c.refresh)
	r.Put("/release-trains/rules/{ruleId}", c.setRule)
	r.Put("/release-trains/hides/{hitId}", c.hide)
	r.Delete("/release-trains/hides/{hitId}", c.unhide)
	r.Delete("/release-trains/hides", c.unhideStale)
	r.Get("/release-trains/llb-plans", c.plans)
	r.Put("/release-trains/llb-plans/{planId}", c.putPlan)
	r.Get("/release-trains/avatars/{accountId}", c.avatar)
}

// avatarCacheSeconds matches the jira.users budget: a face changes at most once a day.
const avatarCacheSeconds = 24 * 60 * 60

func (c *ReleaseTrainsController) avatar(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/release-trains/avatars/{accountId}")
		return
	}
	id, err := url.PathUnescape(chi.URLParam(r, "accountId"))
	if err != nil {
		id = ""
	}
	b, err := c.Svc.Avatar(id)
	if errors.Is(err, rtsvc.ErrBadAccountID) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "BAD_ACCOUNT_ID", "not a Jira account id", nil)
		return
	}
	if err != nil && !errors.Is(err, rtsvc.ErrNoAvatar) {
		envelope.WriteError(w, r, err)
		return
	}
	ct := http.DetectContentType(b)
	if err != nil || !strings.HasPrefix(ct, "image/") {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "RELEASE_AVATAR_NOT_FOUND",
			"no avatar downloaded for this account yet", nil)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", fmt.Sprintf("private, max-age=%d", avatarCacheSeconds))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b) //nolint:gosec // G705: sniffed as a raster image above and served with nosniff
}

func (c *ReleaseTrainsController) board(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/release-trains/board")
		return
	}
	body, etag, err := c.Svc.Board(r.Context(), r.Header.Get("If-None-Match"))
	switch {
	case errors.Is(err, rtsvc.ErrNotModified):
		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusNotModified)
	case err != nil:
		envelope.WriteError(w, r, err)
	default:
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body) //nolint:gosec // G705: our own json.Marshal output, served as application/json
	}
}

func (c *ReleaseTrainsController) refresh(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/release-trains/refresh")
		return
	}
	var req ReleaseTrainRefreshRequest
	if err := decodeJSONStrict(r, &req); err != nil && !errors.Is(err, io.EOF) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_BODY", "Invalid request body", nil)
		return
	}
	if err := c.Svc.Refresh(req.Source); err != nil {
		if errors.Is(err, rtsvc.ErrUnknownSource) {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "UNKNOWN_SOURCE",
				"source must be gitlab, jira or absent", map[string]any{"source": req.Source})
			return
		}
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusAccepted, ReleaseTrainRefreshResponse(req))
}

func (c *ReleaseTrainsController) sources(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/release-trains/sources")
		return
	}
	rep, err := c.Svc.Report(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	out := ReleaseTrainSourcesResponse{
		Config:    ReleaseTrainConfigDTO{State: string(rep.Config.State), Message: rep.Config.Message, Path: rep.Config.Path},
		Resources: make([]ReleaseTrainResourceDTO, 0, len(rep.Resources)),
		LastPass:  ReleaseTrainPassDTO{At: rep.LastPass.At, Requests: rep.LastPass.Requests},
	}
	if out.LastPass.Requests == nil {
		out.LastPass.Requests = map[string]int{}
	}
	for _, res := range rep.Resources {
		out.Resources = append(out.Resources, ReleaseTrainResourceDTO{
			Kind: string(res.Kind), Scope: string(res.Scope), App: string(res.App), Mode: string(res.Mode),
			CompleteAt: res.CompleteAt, LastAttemptAt: res.LastAttemptAt, ErrorClass: string(res.ErrorClass),
			ErrorMessage: res.ErrorMessage, NextAttemptAt: res.NextAttemptAt, Backfilling: res.Backfilling,
		})
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func (c *ReleaseTrainsController) initConfig(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/release-trains/config")
		return
	}
	path, err := c.Svc.InitConfig()
	switch {
	case errors.Is(err, rtsvc.ErrConfigExists):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "RELEASE_TRAIN_CONFIG_EXISTS",
			"the config file already exists; edit it instead: "+path, map[string]any{"path": path})
	case err != nil:
		envelope.WriteError(w, r, err)
	default:
		envelope.WriteJSON(w, http.StatusCreated, ReleaseTrainConfigInitResponse{Path: path})
	}
}
