-- name: InsertCrewRun :exec
INSERT INTO crew_run (id, session_id, project_id, crew_id, role, worktree_path, kind, label, attempt,
    detector, detector_reason, gen_at_start, gen_at_end, started_at, ended_at, outcome, result,
    changed_paths, head_sha, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, NULL, '', '', '[]', '', ?, ?);

-- name: GetCrewRun :one
SELECT id, session_id, project_id, crew_id, role, worktree_path, kind, label, attempt, detector,
    detector_reason, gen_at_start, gen_at_end, started_at, ended_at, outcome, result, changed_paths,
    head_sha, created_at, updated_at
FROM crew_run WHERE id = ?;

-- name: ListCrewRunsBySession :many
SELECT id, session_id, project_id, crew_id, role, worktree_path, kind, label, attempt, detector,
    detector_reason, gen_at_start, gen_at_end, started_at, ended_at, outcome, result, changed_paths,
    head_sha, created_at, updated_at
FROM crew_run WHERE session_id = ? ORDER BY started_at DESC, rowid DESC LIMIT ?;

-- name: GetOpenCrewRunForSession :one
-- The newest still-open run. This IS the "qa is running right now" signal.
SELECT id, session_id, project_id, crew_id, role, worktree_path, kind, label, attempt, detector,
    detector_reason, gen_at_start, gen_at_end, started_at, ended_at, outcome, result, changed_paths,
    head_sha, created_at, updated_at
FROM crew_run WHERE session_id = ? AND ended_at IS NULL ORDER BY started_at DESC, rowid DESC LIMIT 1;

-- name: ListEndedCrewRunOutcomes :many
-- The session's finished runs, newest first, outcome only. The caller counts the
-- leading discards; SQLite has no window function guarantee across every build
-- AO ships on, and this list is tiny.
SELECT outcome FROM crew_run
WHERE session_id = ? AND ended_at IS NOT NULL
ORDER BY ended_at DESC, rowid DESC LIMIT ?;

-- name: EndCrewRun :execrows
UPDATE crew_run
SET gen_at_end = ?, ended_at = ?, outcome = ?, result = ?, changed_paths = ?, head_sha = ?,
    detector = ?, detector_reason = ?, updated_at = ?
WHERE id = ? AND ended_at IS NULL;

-- name: AbandonOpenCrewRuns :execrows
-- Close runs left open by a daemon that went away mid-bracket. There is no
-- watcher behind them any more, so they can only be UNCERTIFIED - never trusted.
UPDATE crew_run
SET ended_at = ?, outcome = 'uncertified', detector = 'down',
    detector_reason = 'the daemon restarted while this run was open, so nothing watched the tree',
    updated_at = ?
WHERE ended_at IS NULL;
