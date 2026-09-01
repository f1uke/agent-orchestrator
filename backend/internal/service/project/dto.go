package project

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

// GetResult is the discriminated result returned by Service.Get.
type GetResult struct {
	Status   string
	Project  *Project
	Degraded *Degraded
}

// AddInput is the body shape for POST /api/v1/projects.
type AddInput struct {
	Path        string                `json:"path"`
	ProjectID   *string               `json:"projectId,omitempty"`
	Name        *string               `json:"name,omitempty"`
	Config      *domain.ProjectConfig `json:"config,omitempty"`
	AsWorkspace bool                  `json:"asWorkspace,omitempty"`
}

// SetConfigInput is the body shape for PUT /api/v1/projects/{id}/config.
//
// It carries two write modes, and MergeFields is what tells them apart:
//
//   - empty MergeFields: Config REPLACES the project's stored config wholesale,
//     and a zero-value Config clears it. This is the whole-object edit - the
//     Settings screen, `--config-json`, `--clear` - where the caller has just
//     read the config and is sending back every key it means to keep.
//   - non-empty MergeFields: only the named fields are taken from Config and
//     written onto the STORED config; every other key survives untouched. This
//     is the partial edit - one `ao project set-config` field flag - where the
//     caller has no idea what else is stored and must not be able to erase it.
//
// The mask, rather than the values, is what distinguishes "the caller asked for
// this field to be false/empty" from "the caller never mentioned this field".
// Without it a partial write cannot express `--web-ui=false`, and with only the
// values it could not express anything else either: a config built from flags
// alone is indistinguishable from one that deliberately clears eighteen keys.
type SetConfigInput struct {
	Config domain.ProjectConfig `json:"config"`
	// MergeFields are dotted JSON field names ("defaultBranch",
	// "gitConvention.branchPrefix", "worker.agent"), spelled exactly as the
	// config is on the wire. An unrecognized name is refused; see
	// domain.MergeConfigFields.
	MergeFields []string `json:"mergeFields,omitempty"`
}

// RemoveResult reports what DELETE /api/v1/projects/{id} actually did.
type RemoveResult struct {
	ProjectID         domain.ProjectID `json:"projectId"`
	RemovedStorageDir bool             `json:"removedStorageDir"`
}
