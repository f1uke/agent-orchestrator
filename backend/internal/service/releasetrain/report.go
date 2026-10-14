package releasetrain

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
)

// Report is what `ao train sources` shows.
type Report struct {
	Config    ConfigReport
	Resources []ResourceReport
	LastPass  PassStats
}

// ConfigReport is the config file's state.
type ConfigReport struct {
	State   ConfigState `json:"state" enum:"absent,invalid,ok"`
	Message string      `json:"message,omitempty" description:"Why the file is invalid, or how to create it when absent."`
	Path    string      `json:"path"`
}

// ResourceReport is one resource's freshness.
type ResourceReport struct {
	Kind          rt.KindName
	Scope         rt.Scope
	App           rt.AppID
	Mode          rt.SyncMode
	CompleteAt    *time.Time
	LastAttemptAt *time.Time
	ErrorClass    rt.ErrorClass
	ErrorMessage  string
	NextAttemptAt *time.Time
	Backfilling   bool
}

// Report reads the config file's state and the store.
func (p *Poller) Report(ctx context.Context) (Report, error) {
	cfg := p.d.Config.Load()
	r := Report{Config: ConfigReport{State: cfg.State, Message: cfg.Message, Path: cfg.Path}, LastPass: p.LastPass()}
	_, rows, err := p.d.Store.LoadReleaseSnapshot(ctx)
	if err != nil {
		return Report{}, err
	}
	r.Resources = make([]ResourceReport, 0, len(rows))
	for _, row := range rows {
		r.Resources = append(r.Resources, ResourceReport{
			Kind: row.Kind, Scope: row.Scope, App: row.App, Mode: row.Mode,
			CompleteAt: row.CompleteAt, LastAttemptAt: row.LastAttemptAt,
			ErrorClass: row.ErrorClass, ErrorMessage: sanitize(row.ErrorMessage), NextAttemptAt: row.NextAttemptAt,
			Backfilling: row.Mode == rt.Incremental && row.CompleteAt == nil && len(row.Cursor) > 0,
		})
	}
	return r, nil
}

// ErrConfigExists is a template write refused because the config file is already there.
var ErrConfigExists = errors.New("release trains: config file already exists")

// WriteExampleConfig writes the template config to path, never over an existing file.
func WriteExampleConfig(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // path is the daemon's own data-dir file
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%w: %s", ErrConfigExists, path)
	}
	if err != nil {
		return err
	}
	if _, err := f.WriteString(rt.ExampleConfigJSON); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
