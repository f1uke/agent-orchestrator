package domain

import "testing"

func TestGitConventionValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     GitConventionConfig
		wantErr bool
	}{
		{"empty ok", GitConventionConfig{}, false},
		{"gitflow no prefix ok", GitConventionConfig{Workflow: GitWorkflowGitflow}, false},
		{"gitflow with prefix ok", GitConventionConfig{Workflow: GitWorkflowGitflow, BranchPrefix: "feature/"}, false},
		{"custom with prefix ok", GitConventionConfig{Workflow: GitWorkflowCustom, BranchPrefix: "feat/"}, false},
		{"custom without prefix rejected", GitConventionConfig{Workflow: GitWorkflowCustom}, true},
		{"unknown workflow rejected", GitConventionConfig{Workflow: "trunk"}, true},
		{"prefix with leading space rejected", GitConventionConfig{Workflow: GitWorkflowCustom, BranchPrefix: " feat/"}, true},
		{"prefix with trailing space rejected", GitConventionConfig{Workflow: GitWorkflowCustom, BranchPrefix: "feat/ "}, true},
		{"prefix with traversal rejected", GitConventionConfig{Workflow: GitWorkflowCustom, BranchPrefix: "../feat/"}, true},
		{"prefix with leading slash rejected", GitConventionConfig{Workflow: GitWorkflowCustom, BranchPrefix: "/feat/"}, true},
		{"prefix with backslash rejected", GitConventionConfig{Workflow: GitWorkflowCustom, BranchPrefix: `feat\`}, true},
		{"prefix with inner space rejected", GitConventionConfig{Workflow: GitWorkflowCustom, BranchPrefix: "my feat/"}, true},
		{"nested prefix ok", GitConventionConfig{Workflow: GitWorkflowCustom, BranchPrefix: "team/feat/"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.cfg.Validate(); (err != nil) != tt.wantErr {
				t.Fatalf("Validate() err = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestGitConventionWithDefaults(t *testing.T) {
	// gitflow with no prefix gets the default feature/ prefix.
	got := GitConventionConfig{Workflow: GitWorkflowGitflow}.WithDefaults()
	if got.BranchPrefix != DefaultGitflowPrefix {
		t.Fatalf("gitflow default prefix = %q, want %q", got.BranchPrefix, DefaultGitflowPrefix)
	}

	// A set prefix is preserved.
	got = GitConventionConfig{Workflow: GitWorkflowGitflow, BranchPrefix: "story/"}.WithDefaults()
	if got.BranchPrefix != "story/" {
		t.Fatalf("gitflow prefix overwritten = %q, want story/", got.BranchPrefix)
	}

	// none is left completely untouched so an empty config stays zero.
	got = GitConventionConfig{}.WithDefaults()
	if !got.IsZero() {
		t.Fatalf("none WithDefaults not zero: %#v", got)
	}

	// custom is not defaulted (prefix is required, validation enforces it).
	got = GitConventionConfig{Workflow: GitWorkflowCustom, BranchPrefix: "feat/"}.WithDefaults()
	if got.BranchPrefix != "feat/" {
		t.Fatalf("custom prefix changed = %q, want feat/", got.BranchPrefix)
	}
}

func TestGitConventionNormalizedBranchPrefix(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"feat", "feat/"},
		{"feat/", "feat/"},
		{"feat//", "feat/"},
		{"  feat/  ", "feat/"},
		{"team/feat", "team/feat/"},
		{"/", ""},
	}
	for _, c := range cases {
		got := GitConventionConfig{BranchPrefix: c.in}.NormalizedBranchPrefix()
		if got != c.want {
			t.Fatalf("NormalizedBranchPrefix(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestGitConventionActive(t *testing.T) {
	if (GitConventionConfig{}).Active() {
		t.Fatal("none should be inactive")
	}
	if !(GitConventionConfig{Workflow: GitWorkflowGitflow}).Active() {
		t.Fatal("gitflow should be active")
	}
	if !(GitConventionConfig{Workflow: GitWorkflowCustom, BranchPrefix: "feat/"}).Active() {
		t.Fatal("custom should be active")
	}
}

// A git convention must participate in ProjectConfig zero/validate/defaults so an
// otherwise-empty config still persists as NULL and a bad convention is rejected.
func TestProjectConfigGitConvention(t *testing.T) {
	if !(ProjectConfig{GitConvention: GitConventionConfig{}}).IsZero() {
		t.Fatal("config with an empty convention should be zero")
	}
	if (ProjectConfig{GitConvention: GitConventionConfig{Workflow: GitWorkflowGitflow}}).IsZero() {
		t.Fatal("config with a set convention should not be zero")
	}
	if err := (ProjectConfig{GitConvention: GitConventionConfig{Workflow: GitWorkflowCustom}}).Validate(); err == nil {
		t.Fatal("custom convention without a prefix should fail ProjectConfig.Validate")
	}
	got := (ProjectConfig{GitConvention: GitConventionConfig{Workflow: GitWorkflowGitflow}}).WithDefaults()
	if got.GitConvention.BranchPrefix != DefaultGitflowPrefix {
		t.Fatalf("ProjectConfig.WithDefaults did not default the convention prefix: %#v", got.GitConvention)
	}
}
