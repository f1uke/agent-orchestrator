package domain

import "testing"

func TestTrackerProviderGitLabConstant(t *testing.T) {
	if TrackerProviderGitLab != "gitlab" {
		t.Fatalf("TrackerProviderGitLab = %q, want gitlab", TrackerProviderGitLab)
	}
}
