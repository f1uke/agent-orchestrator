package gitlab

import (
	"net/url"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ParseRepository normalizes a GitLab remote/origin URL into a
// provider-neutral repository key. It accepts SSH
// (git@host:group/sub/proj.git) and HTTPS
// (https://host/group/sub/proj(.git)) forms. GitLab supports arbitrarily
// nested groups, so the full path (minus the trailing ".git") becomes
// Repo, the last path segment becomes Name, and everything before it
// becomes Owner (e.g. "group/sub/proj" -> Owner "group/sub", Name
// "proj"). Remotes whose host does not match this provider's configured
// Host return ok=false so a composite dispatcher can try the next SCM
// provider instead of misclaiming the remote.
func (p *Provider) ParseRepository(remote string) (ports.SCMRepo, bool) {
	raw := strings.TrimSpace(remote)
	if raw == "" {
		return ports.SCMRepo{}, false
	}

	var host, pathPart string
	if strings.HasPrefix(raw, "git@") {
		rest := strings.TrimPrefix(raw, "git@")
		parts := strings.SplitN(rest, ":", 2)
		if len(parts) != 2 {
			return ports.SCMRepo{}, false
		}
		host = parts[0]
		pathPart = parts[1]
	} else {
		u, err := url.Parse(raw)
		if err != nil {
			return ports.SCMRepo{}, false
		}
		host = u.Host
		pathPart = u.Path
	}

	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" || !strings.EqualFold(host, p.host) {
		return ports.SCMRepo{}, false
	}

	pathPart = strings.Trim(pathPart, "/")
	pathPart = strings.TrimSuffix(pathPart, ".git")
	if pathPart == "" {
		return ports.SCMRepo{}, false
	}

	segments := strings.Split(pathPart, "/")
	if len(segments) < 2 {
		return ports.SCMRepo{}, false
	}
	name := segments[len(segments)-1]
	owner := strings.Join(segments[:len(segments)-1], "/")
	if owner == "" || name == "" {
		return ports.SCMRepo{}, false
	}

	return ports.SCMRepo{
		Provider: "gitlab",
		Host:     host,
		Owner:    owner,
		Name:     name,
		Repo:     pathPart,
	}, true
}
