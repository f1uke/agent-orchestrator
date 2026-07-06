// Package gitlab implements the ports.Tracker outbound port for GitLab
// Issues via the REST v4 API. v1 is read-only:
//
//   - Get returns a normalized snapshot of one issue (spawn-bootstrap reads
//     it to hydrate the agent prompt).
//   - List returns a filtered slice of issues in a project.
//   - Preflight performs a single GET /user against GitLab to verify the
//     token is accepted.
//
// # Native ID shape
//
// TrackerID.Native for GitLab is "group/sub/proj#<iid>" — the project's
// full path (which may itself contain "/" for nested groups) followed by
// the last "#" and the issue's internal ID (iid, not the global id).
// parseID splits on the LAST "#" so nested-group paths (which never
// contain "#") round-trip correctly.
//
// The project path is percent-encoded as a single path segment
// (url.PathEscape turns "group/sub/proj" into "group%2Fsub%2Fproj") per
// GitLab's REST convention for addressing projects by path instead of
// numeric id. Building the request URL naively via net/url — assigning the
// pre-escaped segment straight to url.URL.Path — causes url.URL.String()
// to re-escape it, corrupting "%2F" into "%252F" on the wire. This adapter
// avoids that by building the request URL as a raw string (APIBase + path)
// rather than round-tripping the escaped segment through url.URL.Path.
//
// # Reverse state mapping
//
// GitLab issues only have two native states: opened and closed (no native
// in-progress/review state without relying on board columns, which are a
// premium feature and out of scope for v1).
//
//   - opened -> open
//   - closed -> done
//
// # Out of scope
//
//   - No Comment, no Transition (write-side work deferred).
//   - No pagination beyond GitLab's default page size in v1.
//   - No webhook receiver, no polling goroutine.
//   - No richer per-provider metadata on Issue (milestones, boards,
//     weight); the port only carries fields all v1 providers can fill.
package gitlab
