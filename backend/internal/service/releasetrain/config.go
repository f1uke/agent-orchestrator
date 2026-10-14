// Package releasetrain runs the release-train monitor: it loads the config,
// polls the sources into observation rows and reports their freshness.
package releasetrain

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"time"

	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
)

// ConfigFileName is the monitor's config file in the data dir.
const ConfigFileName = "release-train.json"

// ConfigState says whether the monitor is configured.
type ConfigState string

// Config states.
const (
	ConfigAbsent  ConfigState = "absent"
	ConfigInvalid ConfigState = "invalid"
	ConfigOK      ConfigState = "ok"
)

// ConfigStatus is the loaded config, if any, and what the file on disk says now.
// After a failed edit Config and Loc are the last good ones.
type ConfigStatus struct {
	State   ConfigState
	Message string
	Path    string
	Config  *rt.Config
	Loc     *time.Location
}

// ConfigLoader re-reads the config file when its mtime changes.
type ConfigLoader struct {
	path string

	mu     sync.Mutex
	mtime  time.Time
	status ConfigStatus
}

// NewConfigLoader returns a loader for path.
func NewConfigLoader(path string) *ConfigLoader { return &ConfigLoader{path: path} }

// Path is the config file the loader reads.
func (l *ConfigLoader) Path() string { return l.path }

// Load returns the current config status, reading the file only when it changed.
func (l *ConfigLoader) Load() ConfigStatus {
	l.mu.Lock()
	defer l.mu.Unlock()
	info, err := os.Stat(l.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		l.mtime = time.Time{}
		l.status = ConfigStatus{State: ConfigAbsent, Path: l.path,
			Message: fmt.Sprintf("no config at %s; run `ao train config init` to write a template, then fill in your ids", l.path)}
		return l.status
	case err != nil:
		return l.invalid(err.Error())
	case l.status.State != "" && l.status.State != ConfigAbsent && info.ModTime().Equal(l.mtime):
		return l.status
	}
	l.mtime = info.ModTime()
	b, err := os.ReadFile(l.path)
	if err != nil {
		return l.invalid(err.Error())
	}
	var cfg rt.Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return l.invalid("not valid JSON: " + err.Error())
	}
	loc, err := cfg.Validate()
	if err != nil {
		return l.invalid(err.Error())
	}
	l.status = ConfigStatus{State: ConfigOK, Path: l.path, Config: &cfg, Loc: loc}
	return l.status
}

func (l *ConfigLoader) invalid(msg string) ConfigStatus {
	l.status = ConfigStatus{State: ConfigInvalid, Path: l.path, Message: msg, Config: l.status.Config, Loc: l.status.Loc}
	return l.status
}
