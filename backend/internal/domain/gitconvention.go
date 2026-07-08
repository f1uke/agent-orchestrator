package domain

import (
	"fmt"
	"strings"
)

// GitWorkflow selects a project's branching model. The empty value ("none") is
// the default and leaves every legacy behavior unchanged.
type GitWorkflow string

const (
	// GitWorkflowNone applies no convention: branches are auto-named exactly as
	// before and no convention text is injected into prompts.
	GitWorkflowNone GitWorkflow = ""
	// GitWorkflowGitflow follows gitflow: the branch type (feature/bugfix/hotfix)
	// is inferred per task and BranchPrefix is only the default/primary prefix.
	GitWorkflowGitflow GitWorkflow = "gitflow"
	// GitWorkflowCustom forces a single fixed BranchPrefix on every branch.
	GitWorkflowCustom GitWorkflow = "custom"
)

// DefaultGitflowPrefix is the primary prefix a gitflow project uses when it
// configures none. It is also the prefix the orchestrator is told to default to.
const DefaultGitflowPrefix = "feature/"

// GitConventionConfig is a project's git branching convention. It feeds three
// consumers: the orchestrator system prompt (so it spawns workers with the right
// --branch), the worker system prompt (so it keeps sibling branches on-convention),
// and spawn-time auto-naming (so an omitted --branch still gets the prefix). Base
// branch and PR target are the project's existing DefaultBranch — this type adds
// only the branch prefix.
type GitConventionConfig struct {
	// Workflow is the branching model. Empty ("none") is the unchanged default.
	Workflow GitWorkflow `json:"workflow,omitempty" enum:"gitflow,custom"`
	// BranchPrefix is prepended to branch names. For custom it is required and
	// fixed; for gitflow it is the default type prefix (feature/). Ignored for none.
	BranchPrefix string `json:"branchPrefix,omitempty"`
}

// IsZero reports whether no convention is configured, so ProjectConfig.IsZero can
// keep persisting an otherwise-empty config as SQL NULL.
func (c GitConventionConfig) IsZero() bool {
	return c == GitConventionConfig{}
}

// Active reports whether a convention is configured (workflow is gitflow or
// custom). Callers use it to decide whether to prefix branches or inject prompt
// text.
func (c GitConventionConfig) Active() bool {
	return c.Workflow == GitWorkflowGitflow || c.Workflow == GitWorkflowCustom
}

// WithDefaults fills the gitflow prefix when it is left blank. none and custom are
// left untouched: none must stay zero, and custom's prefix is required (Validate
// enforces it) so there is nothing to default.
func (c GitConventionConfig) WithDefaults() GitConventionConfig {
	if c.Workflow == GitWorkflowGitflow && strings.TrimSpace(c.BranchPrefix) == "" {
		c.BranchPrefix = DefaultGitflowPrefix
	}
	return c
}

// NormalizedBranchPrefix trims the configured prefix and guarantees exactly one
// trailing slash (e.g. "feat" and "feat//" both become "feat/"). It returns "" when
// no usable prefix is set.
func (c GitConventionConfig) NormalizedBranchPrefix() string {
	p := strings.Trim(strings.TrimSpace(c.BranchPrefix), "/")
	if p == "" {
		return ""
	}
	return p + "/"
}

// Validate rejects an unknown workflow, a custom workflow without a prefix, and a
// prefix that is not a safe branch fragment, so a bad convention is refused when it
// is set rather than surfacing at spawn.
func (c GitConventionConfig) Validate() error {
	switch c.Workflow {
	case GitWorkflowNone, GitWorkflowGitflow, GitWorkflowCustom:
	default:
		return fmt.Errorf("gitConvention.workflow: unknown workflow %q", c.Workflow)
	}
	if c.Workflow == GitWorkflowCustom && strings.TrimSpace(c.BranchPrefix) == "" {
		return fmt.Errorf("gitConvention.branchPrefix: required when workflow is custom")
	}
	return validateBranchPrefix(c.BranchPrefix)
}

// validateBranchPrefix refuses a prefix that could produce an unsafe or invalid
// git ref: surrounding/inner whitespace, a ".." traversal, a leading slash, a
// backslash, or any character outside a conservative branch-name set.
func validateBranchPrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	if strings.TrimSpace(prefix) != prefix {
		return fmt.Errorf("gitConvention.branchPrefix: must not have leading or trailing whitespace")
	}
	if strings.Contains(prefix, "..") || strings.HasPrefix(prefix, "/") || strings.Contains(prefix, `\`) {
		return fmt.Errorf("gitConvention.branchPrefix: must not contain '..', a leading slash, or a backslash")
	}
	for _, r := range prefix {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '/' || r == '-' || r == '_' || r == '.':
		default:
			return fmt.Errorf("gitConvention.branchPrefix: contains invalid character %q", r)
		}
	}
	return nil
}
