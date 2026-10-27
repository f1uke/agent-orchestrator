package specgen

import (
	"net/http"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

func init() {
	for name, clean := range map[string]string{
		"ControllersReleaseWebStatusResponse":  "ReleaseWebStatusResponse",
		"ControllersReleaseWebCopyKeyResponse": "ReleaseWebCopyKeyResponse",
	} {
		schemaNames[name] = clean
	}
}

// releaseWebOperations declares the /release-web operations. Must stay 1:1
// with the routes ReleaseWebController.Register mounts (enforced by the parity test).
func releaseWebOperations() []operation {
	status := func(method, path, id, summary string) operation {
		return operation{
			method: method, path: path, id: id, tag: "release-web", summary: summary,
			resps: []respUnit{
				{http.StatusOK, controllers.ReleaseWebStatusResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		}
	}
	return []operation{
		status(http.MethodGet, "/api/v1/release-web", "getReleaseWebStatus",
			"Show whether the team web runs, where, how many are signed in, and the key generation; never the key"),
		status(http.MethodPost, "/api/v1/release-web/start", "startReleaseWeb",
			"Start the team web as its own detached process, creating a key when none exists"),
		status(http.MethodPost, "/api/v1/release-web/stop", "stopReleaseWeb",
			"Stop the team web"),
		status(http.MethodPost, "/api/v1/release-web/rotate-key", "rotateReleaseWebKey",
			"Replace the shared key; every signed-in session ends"),
		{
			method: http.MethodPost, path: "/api/v1/release-web/copy-key", id: "copyReleaseWebKey", tag: "release-web",
			summary: "Put the shared key on the clipboard of the machine the daemon runs on; the key never crosses HTTP",
			resps: []respUnit{
				{http.StatusOK, controllers.ReleaseWebCopyKeyResponse{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
	}
}
