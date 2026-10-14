package releasetrain

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// AppID is an app's config id.
type AppID string

// FamilyID is a family's config id.
type FamilyID string

// Platform is the store platform an app ships to.
type Platform string

// Platforms an app can ship to.
const (
	PlatformIOS     Platform = "ios"
	PlatformAndroid Platform = "android"
)

// Config is the monitor's configuration, read from release-train.json.
type Config struct {
	Timezone    string            `json:"timezone"`
	HistoryFrom Day               `json:"historyFrom"`
	AgeOutDays  int               `json:"ageOutDays"`
	FixWrites   string            `json:"fixWrites"`
	GitLabHost  string            `json:"gitlabHost"`
	Jira        JiraConfig        `json:"jira"`
	Confluence  *ConfluenceConfig `json:"confluence,omitempty"`
	Families    []Family          `json:"families"`
	Apps        []App             `json:"apps"`
	Boards      []BoardConfig     `json:"boards,omitempty"`
	Mobile      MobileConfig      `json:"mobile"`
}

// JiraConfig names the Jira projects the monitor reads.
type JiraConfig struct {
	Project            string `json:"project"`
	TechRequestProject string `json:"techRequestProject,omitempty"`
	ForceUpdateSummary string `json:"forceUpdateSummary,omitempty"`
}

// ConfluenceConfig names where the train pages live and the table cells that
// hold their freeze and publish dates. A page belongs to the family whose
// trainLabel ends its title before "#<train>".
type ConfluenceConfig struct {
	Space          string `json:"space"`
	TrainPageTitle string `json:"trainPageTitle"`
	FreezeCell     string `json:"freezeCell"`
	PublishCell    string `json:"publishCell"`
}

// BoardConfig is one Jira agile board whose sprint the monitor follows.
type BoardConfig struct {
	ID               int    `json:"id"`
	Key              string `json:"key"`
	OwnsSprintBranch bool   `json:"ownsSprintBranch"`
}

// MobileConfig says which Jira projects and subtasks touch the mobile apps.
type MobileConfig struct {
	Projects       []string `json:"projects,omitempty"`
	SubtaskPattern string   `json:"subtaskPattern,omitempty"`
}

// FixWritesOff is the only fixWrites value accepted: every fix except L3 is a dry run.
const FixWritesOff = "off"

// Family is a set of apps that ride the same trains under one train label.
type Family struct {
	ID         FamilyID `json:"id"`
	Label      string   `json:"label"`
	TrainLabel string   `json:"trainLabel"`
	Apps       []AppID  `json:"apps"`
}

// App is one mobile app with its GitLab project and Jira version prefix.
type App struct {
	ID                AppID    `json:"id"`
	Name              string   `json:"name"`
	Platform          Platform `json:"platform"`
	GitLabProject     int      `json:"gitlabProject"`
	JiraVersionPrefix string   `json:"jiraVersionPrefix"`
}

// ConfigError names the config field that failed validation.
type ConfigError struct{ Field, Reason string }

func (e *ConfigError) Error() string { return e.Field + ": " + e.Reason }

func configErr(field, format string, args ...any) *ConfigError {
	return &ConfigError{Field: field, Reason: fmt.Sprintf(format, args...)}
}

// Validate checks the config and resolves its timezone.
func (c Config) Validate() (*time.Location, error) {
	if c.Timezone == "" {
		return nil, configErr("timezone", "required")
	}
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return nil, configErr("timezone", "unknown timezone %q", c.Timezone)
	}
	if c.AgeOutDays < 0 {
		return nil, configErr("ageOutDays", "must not be negative")
	}
	if err := c.validateSources(); err != nil {
		return nil, err
	}
	apps := map[AppID]bool{}
	for _, a := range c.Apps {
		switch {
		case a.ID == "" || apps[a.ID]:
			return nil, configErr("apps", "missing or duplicate id %q", a.ID)
		case a.Platform != PlatformIOS && a.Platform != PlatformAndroid:
			return nil, configErr("apps", "%s: platform %q is not ios or android", a.ID, a.Platform)
		case a.GitLabProject <= 0:
			return nil, configErr("apps", "%s: gitlabProject required", a.ID)
		case a.JiraVersionPrefix == "":
			return nil, configErr("apps", "%s: jiraVersionPrefix required", a.ID)
		}
		apps[a.ID] = true
	}
	inFamily := map[AppID]FamilyID{}
	for _, f := range c.Families {
		if f.ID == "" || f.TrainLabel == "" {
			return nil, configErr("families", "family %q needs an id and a trainLabel", f.ID)
		}
		for _, a := range f.Apps {
			if !apps[a] {
				return nil, configErr("families", "%s names unknown app %q", f.ID, a)
			}
			if other, ok := inFamily[a]; ok {
				return nil, configErr("families", "app %q is in both %s and %s", a, other, f.ID)
			}
			inFamily[a] = f.ID
		}
	}
	for _, a := range c.Apps {
		if _, ok := inFamily[a.ID]; !ok {
			return nil, configErr("families", "app %q is in no family", a.ID)
		}
	}
	return loc, nil
}

func (c Config) validateSources() error {
	switch {
	case c.HistoryFrom == Day{}:
		return configErr("historyFrom", "required, as YYYY-MM-DD")
	case c.FixWrites != FixWritesOff:
		return configErr("fixWrites", "only %q is supported", FixWritesOff)
	case strings.TrimSpace(c.GitLabHost) == "" || strings.ContainsAny(c.GitLabHost, "/: "):
		return configErr("gitlabHost", "a host name such as gitlab.example.com is required")
	case c.Jira.Project == "":
		return configErr("jira.project", "required")
	case c.Jira.TechRequestProject != "" && c.Jira.ForceUpdateSummary == "":
		return configErr("jira.forceUpdateSummary", "required when techRequestProject is set")
	case c.Confluence != nil && (c.Confluence.Space == "" || c.Confluence.TrainPageTitle == "" || c.Confluence.FreezeCell == "" || c.Confluence.PublishCell == ""):
		return configErr("confluence", "needs a space, a trainPageTitle, a freezeCell and a publishCell")
	case c.Confluence != nil && strings.ContainsAny(c.Confluence.Space+c.Confluence.TrainPageTitle, `"\`):
		return configErr("confluence", "space and trainPageTitle must not contain quotes or backslashes")
	}
	boards := map[int]bool{}
	for _, b := range c.Boards {
		if b.ID <= 0 || b.Key == "" || boards[b.ID] {
			return configErr("boards", "board %d needs a unique positive id and a key", b.ID)
		}
		boards[b.ID] = true
	}
	if _, err := regexp.Compile(c.Mobile.SubtaskPattern); err != nil {
		return configErr("mobile.subtaskPattern", "%v", err)
	}
	return nil
}

func (c Config) app(id AppID) App {
	for _, a := range c.Apps {
		if a.ID == id {
			return a
		}
	}
	return App{}
}

func (c Config) familyOf(id AppID) Family {
	for _, f := range c.Families {
		for _, a := range f.Apps {
			if a == id {
				return f
			}
		}
	}
	return Family{}
}
