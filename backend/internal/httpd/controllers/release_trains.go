package controllers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	rtsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain"
)

// ReleaseTrainsService is the controller-facing contract over the release-train monitor.
type ReleaseTrainsService interface {
	Report(ctx context.Context) (rtsvc.Report, error)
	// InitConfig writes the template config and returns its path, or rtsvc.ErrConfigExists.
	InitConfig() (string, error)
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
