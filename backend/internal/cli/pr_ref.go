package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// invalidPRRefMessage is the shared usage message when a --claim-pr value is
// neither a github.com pull URL, a GitLab merge-request URL, nor a bare number.
const invalidPRRefMessage = "PR reference must be a github.com PR URL, a GitLab merge-request URL, or a number"

func (c *commandContext) resolvePRRef(ctx context.Context, ref string, project projectDetails) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", usageError{errors.New(invalidPRRefMessage)}
	}
	// A GitLab merge-request URL carries its own host, project path, and IID, so
	// it resolves with no project repo lookup. Detect it before the GitHub paths,
	// which only understand a bare number or a github.com pull URL.
	if isGitLabMRRef(ref) {
		normalized, err := normalizeGitLabMRURL(ref)
		if err != nil {
			return "", usageError{errors.New(invalidPRRefMessage)}
		}
		return normalized, nil
	}
	if isNumericPRRef(ref) {
		repo := strings.TrimSpace(project.Repo)
		if repo == "" {
			// The daemon must not shell out to external CLIs from its loopback API;
			// when the durable project record lacks repo_origin_url, the thin CLI
			// does the one-off gh lookup from the registered project checkout and
			// sends the daemon a normalized URL.
			out, err := c.deps.CommandOutputInDir(ctx, project.Path, "gh", "repo", "view", "--json", "url", "-q", ".url")
			if err != nil || strings.TrimSpace(string(out)) == "" {
				return "", usageError{errors.New("gh not available; pass the full PR or GitLab MR URL")}
			}
			repo = strings.TrimSpace(string(out))
		}
		owner, name, err := cliGitHubRepoFromURL(repo)
		if err != nil {
			return "", usageError{errors.New(invalidPRRefMessage)}
		}
		n, _ := strconv.Atoi(strings.TrimPrefix(ref, "#"))
		return fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, name, n), nil
	}
	owner, name, n, err := cliParseGitHubPRURL(ref)
	if err != nil || owner == "" || name == "" || n <= 0 {
		return "", usageError{errors.New(invalidPRRefMessage)}
	}
	return fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, name, n), nil
}

// gitlabMRPathMarker separates a GitLab project path from the merge-request
// segment in a web URL, e.g. ".../group/proj/-/merge_requests/123". The daemon's
// session service applies the same normalization; the CLI mirror keeps a bad
// value from reaching the daemon and normalizes what it forwards.
const gitlabMRPathMarker = "/-/merge_requests/"

// isGitLabMRRef reports whether ref looks like a GitLab merge-request URL. It is
// host-agnostic (Finnomena runs self-hosted GitLab, not gitlab.com), keying only
// on the "/-/merge_requests/" path marker.
func isGitLabMRRef(ref string) bool {
	u, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return false
	}
	return strings.Contains(u.Path, gitlabMRPathMarker)
}

// normalizeGitLabMRURL validates and canonicalizes a GitLab MR URL to
// "https://<host>/<namespace>/<project>/-/merge_requests/<iid>", dropping any
// trailing sub-tab, trailing slash, or query. The project path must have at
// least two segments (namespace/project).
func normalizeGitLabMRURL(ref string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(u.Scheme, "https") && !strings.EqualFold(u.Scheme, "http") {
		return "", errors.New("not https")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", errors.New("no host")
	}
	before, after, found := strings.Cut(strings.Trim(u.Path, "/"), gitlabMRPathMarker)
	if !found {
		return "", errors.New("not a merge request url")
	}
	projectPath := strings.Trim(before, "/")
	if projectPath == "" || len(strings.Split(projectPath, "/")) < 2 {
		return "", errors.New("missing namespace/project")
	}
	iidField, _, _ := strings.Cut(after, "/")
	iid, err := strconv.Atoi(iidField)
	if err != nil || iid <= 0 {
		return "", errors.New("missing merge request iid")
	}
	return "https://" + host + "/" + projectPath + gitlabMRPathMarker + strconv.Itoa(iid), nil
}

func isNumericPRRef(ref string) bool {
	ref = strings.TrimPrefix(strings.TrimSpace(ref), "#")
	n, err := strconv.Atoi(ref)
	return err == nil && n > 0
}

func cliParseGitHubPRURL(raw string) (string, string, int, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", 0, err
	}
	if !strings.EqualFold(u.Scheme, "https") || !strings.EqualFold(u.Hostname(), "github.com") {
		return "", "", 0, errors.New("not github")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 4 || parts[2] != "pull" {
		return "", "", 0, errors.New("not pr")
	}
	n, err := strconv.Atoi(parts[3])
	if err != nil || n <= 0 {
		return "", "", 0, errors.New("bad number")
	}
	return parts[0], strings.TrimSuffix(parts[1], ".git"), n, nil
}

func cliGitHubRepoFromURL(raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "git@github.com:") {
		parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(raw, "git@github.com:"), ".git"), "/")
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			return parts[0], parts[1], nil
		}
		return "", "", errors.New("bad repo")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", err
	}
	if !strings.EqualFold(u.Hostname(), "github.com") {
		return "", "", errors.New("not github")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", errors.New("bad repo")
	}
	return parts[0], strings.TrimSuffix(parts[1], ".git"), nil
}
