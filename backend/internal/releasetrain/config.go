package releasetrain

import (
	"fmt"
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
	Timezone   string   `json:"timezone"`
	AgeOutDays int      `json:"ageOutDays"`
	Families   []Family `json:"families"`
	Apps       []App    `json:"apps"`
}

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

func (c Config) app(id AppID) App {
	for _, a := range c.Apps {
		if a.ID == id {
			return a
		}
	}
	return App{}
}

func (c Config) familyOf(id AppID) (Family, bool) {
	for _, f := range c.Families {
		for _, a := range f.Apps {
			if a == id {
				return f, true
			}
		}
	}
	return Family{}, false
}
