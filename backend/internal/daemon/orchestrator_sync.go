package daemon

import "time"

// orchestratorSyncIntervalDefault is how often each live orchestrator's worktree
// is fast-forwarded to its project's default branch.
//
// 15 minutes is chosen against the cost on both sides. Too slow and the
// orchestrator answers from code that moved hours ago — the bug being fixed.
// Too fast and every tick is a `git fetch` per live orchestrator against the
// remote, for a tree nobody may be reading. Fifteen minutes keeps the worst-case
// staleness under a coffee break while leaving the fetch rate negligible, and
// the sync is a no-op the moment the branch is already current.
const orchestratorSyncIntervalDefault = 15 * time.Minute
