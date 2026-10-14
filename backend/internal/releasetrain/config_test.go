package releasetrain

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestConfigValidateResolvesTimezone(t *testing.T) {
	loc, err := fixtureConfig().Validate()
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if loc.String() != "Asia/Bangkok" {
		t.Fatalf("loc = %s, want Asia/Bangkok", loc)
	}
}

func TestConfigValidateRejects(t *testing.T) {
	for _, tc := range []struct {
		name  string
		edit  func(*Config)
		field string
	}{
		{"unknown timezone", func(c *Config) { c.Timezone = "Mars/Olympus" }, "timezone"},
		{"empty timezone", func(c *Config) { c.Timezone = "" }, "timezone"},
		{"negative age out", func(c *Config) { c.AgeOutDays = -1 }, "ageOutDays"},
		{"duplicate app", func(c *Config) { c.Apps = append(c.Apps, c.Apps[0]) }, "apps"},
		{"app without project", func(c *Config) { c.Apps[1].GitLabProject = 0 }, "apps"},
		{"app without jira prefix", func(c *Config) { c.Apps[1].JiraVersionPrefix = "" }, "apps"},
		{"bad platform", func(c *Config) { c.Apps[0].Platform = "web" }, "apps"},
		{"family names unknown app", func(c *Config) { c.Families[0].Apps = append(c.Families[0].Apps, "zz") }, "families"},
		{"app in two families", func(c *Config) { c.Families[1].Apps = append(c.Families[1].Apps, "di") }, "families"},
		{"family without train label", func(c *Config) { c.Families[0].TrainLabel = "" }, "families"},
		{"app in no family", func(c *Config) { c.Families[1].Apps = []AppID{"pi"} }, "families"},
		{"missing history start", func(c *Config) { c.HistoryFrom = Day{} }, "historyFrom"},
		{"fix writes on", func(c *Config) { c.FixWrites = "on" }, "fixWrites"},
		{"missing gitlab host", func(c *Config) { c.GitLabHost = "" }, "gitlabHost"},
		{"missing jira project", func(c *Config) { c.Jira.Project = "" }, "jira.project"},
		{"tech request project without summary", func(c *Config) { c.Jira.ForceUpdateSummary = "" }, "jira.forceUpdateSummary"},
		{"confluence space without page title", func(c *Config) { c.Confluence = &ConfluenceConfig{Space: "DOCS"} }, "confluence"},
		{"duplicate board", func(c *Config) { c.Boards = append(c.Boards, c.Boards[0]) }, "boards"},
		{"board without key", func(c *Config) { c.Boards[0].Key = "" }, "boards"},
		{"bad subtask pattern", func(c *Config) { c.Mobile.SubtaskPattern = "(" }, "mobile.subtaskPattern"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fixtureConfig()
			tc.edit(&c)
			_, err := c.Validate()
			var ce *ConfigError
			if !errors.As(err, &ce) {
				t.Fatalf("Validate err = %v, want *ConfigError", err)
			}
			if ce.Field != tc.field {
				t.Fatalf("field = %q, want %q (%v)", ce.Field, tc.field, err)
			}
		})
	}
}

func TestExampleConfigDecodesAndValidates(t *testing.T) {
	var c Config
	if err := json.Unmarshal([]byte(ExampleConfigJSON), &c); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if c.Apps[0].GitLabProject != 1001 || c.Apps[0].JiraVersionPrefix != "iOS" || c.Families[0].TrainLabel != "Release Train" ||
		c.AgeOutDays != 7 || c.HistoryFrom.String() != "2026-04-01" || c.FixWrites != "off" || c.Jira.TechRequestProject != "DEMOREQ" ||
		c.Confluence == nil || c.Confluence.TrainPageTitle == "" || len(c.Boards) != 2 || !c.Boards[0].OwnsSprintBranch ||
		c.Mobile.SubtaskPattern == "" || len(c.Mobile.Projects) != 1 {
		t.Fatalf("decoded = %+v", c)
	}
}

func TestConfigWithoutOptionalBlocksValidates(t *testing.T) {
	c := fixtureConfig()
	c.Confluence, c.Boards, c.Mobile = nil, nil, MobileConfig{}
	c.Jira.TechRequestProject, c.Jira.ForceUpdateSummary = "", ""
	if _, err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}
