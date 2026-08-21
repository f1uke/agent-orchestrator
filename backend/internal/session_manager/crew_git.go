package sessionmanager

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// "qa COMMITS ONLY TO TEST PATHS" - ENFORCED, NOT ASKED FOR.
//
// Two agents share one worktree, which means one git index and one branch ref.
// The collisions that produces are noise rather than damage - git fails one side
// of a racing `index.lock` - but ONE of them is real: a `git add -A` by either
// member sweeps up the other's half-finished work and commits it under the wrong
// name. `git log` is supposed to say who wrote a test, because that matters when
// it later fails.
//
// The design's rule (qa commits test paths, explicitly) was tidiness while only
// one member could be awake. With both awake it is load-bearing, and a prompt is
// not where load-bearing rules live - both real crew runs showed the prompt
// being overridden by a brief. So it is a git hook, and git refuses the commit.
//
// WHY THE ENVIRONMENT AND NOT THE REPOSITORY. Hooks live in the COMMON git dir,
// which every worktree of a repository shares - writing `.git/hooks/pre-commit`
// would police the human's own checkout and every other AO worktree of that
// repo, which is not AO's business. `core.hooksPath` injected through
// GIT_CONFIG_* is scoped to the processes of ONE session: it touches no file in
// the repository, dev never sees it, and a solo session never has it.
//
// WHAT IT DOES NOT DO, said plainly: `git commit --no-verify` skips every hook,
// and that is git's design rather than a gap in this one. What this buys is that
// the rule is enforced by default and has to be deliberately stepped around,
// instead of being a sentence in a prompt that a brief can talk over.

// crewGitEnv returns the git configuration injected into ONE crew member's
// environment. Only qa gets any: dev's scope is the whole tree, and a solo
// session is not a crew member at all, so both take the empty map and nothing
// about them changes.
func crewGitEnv(role domain.CrewRole, dataDir string) map[string]string {
	if role != domain.CrewRoleQA || dataDir == "" {
		return nil
	}
	dir, err := installQAHooks(dataDir)
	if err != nil {
		// Fail OPEN, and only here: a hook that could not be written must not stop
		// qa working. The rule falls back to what it was before - the prompt - and
		// the failure is visible in the daemon log.
		return nil
	}
	// GIT_CONFIG_COUNT/KEY/VALUE is `git -c` for every git this session runs,
	// including the ones the agent runs itself. AO's env is applied after the
	// project's, so a project that sets GIT_CONFIG_COUNT for its own reasons is
	// overridden here - deliberately, exactly as it is for AO_SESSION_ID.
	return map[string]string{
		"GIT_CONFIG_COUNT":   "1",
		"GIT_CONFIG_KEY_0":   "core.hooksPath",
		"GIT_CONFIG_VALUE_0": dir,
	}
}

// installQAHooks writes the hooks directory under the data dir and returns it.
// Idempotent: the file is rewritten every time, so an AO upgrade that changes
// the rule reaches sessions without anyone cleaning anything up.
func installQAHooks(dataDir string) (string, error) {
	dir := filepath.Join(dataDir, "githooks", "qa")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("crew hooks: %w", err)
	}
	path := filepath.Join(dir, "pre-commit")
	// 0700, and the execute bit is the point: git runs this, so it has to be
	// executable. Owner-only is as tight as a runnable hook gets.
	if err := os.WriteFile(path, []byte(qaPreCommitHook), 0o700); err != nil { //nolint:gosec // a hook git must execute cannot be 0600
		return "", fmt.Errorf("crew hooks: %w", err)
	}
	return dir, nil
}

// qaPreCommitHook refuses a commit that stages anything outside a test path, and
// then hands over to the project's own pre-commit hook if it has one - so a repo
// with husky or a formatter hook keeps it.
//
// The refusal names the offending paths and says what to do instead, because the
// most likely way to reach it is `git add -A` sweeping up dev's work, and the fix
// for that is to name the test files.
const qaPreCommitHook = `#!/bin/sh
# Installed by Agent Orchestrator for the qa member of a crew. Do not edit: it is
# rewritten from backend/internal/session_manager/crew_git.go on every launch.
#
# You share this worktree with dev. Your commits are yours - test files - and
# dev's are dev's. This refuses a commit that stages anything else, because the
# usual way it happens is a wide ` + "`git add`" + ` sweeping up work in progress that
# belongs to the other agent.
set -e

offenders=""
for path in $(git diff --cached --name-only); do
	case "$path" in
	*_test.go|*_test.py|test_*.py|*_test.rb|*_spec.rb|*_test.exs|*_test.dart|*_test.cc|*_test.cpp) ;;
	*.test.ts|*.test.tsx|*.test.js|*.test.jsx|*.test.mjs) ;;
	*.spec.ts|*.spec.tsx|*.spec.js|*.spec.jsx|*.spec.mjs) ;;
	*Test.swift|*Tests.swift|*Spec.swift|*Test.java|*Tests.kt|*Test.kt) ;;
	test/*|tests/*|__tests__/*|testdata/*|e2e/*|maestro/*|spec/*) ;;
	*/test/*|*/tests/*|*/__tests__/*|*/testdata/*|*/e2e/*|*/maestro/*|*/spec/*) ;;
	*UITests/*|*UITest/*|*.feature|*.snap|*.golden) ;;
	*)
		offenders="$offenders  $path
" ;;
	esac
done

if [ -n "$offenders" ]; then
	printf '%s\n' "AO: refused - you are qa on this task, and these are not test paths:" >&2
	printf '%s' "$offenders" >&2
	printf '%s\n' "" >&2
	printf '%s\n' "You and dev write into ONE worktree. Commit the test files by name" >&2
	printf '%s\n' "(git commit <paths>) rather than with a wide 'git add', and leave the rest" >&2
	printf '%s\n' "to dev - a commit that sweeps up its work in progress lands under your name" >&2
	printf '%s\n' "and breaks its build. If a change outside a test path is genuinely yours," >&2
	printf '%s\n' "tell dev with: ao send --crew dev --about <sha> --message '...'" >&2
	exit 1
fi

# Hand over to the project's own hook, if it has one - this replaced its hooks
# path for this session, and taking a repository's formatter or linter away from
# one agent and not the other would be its own kind of mess.
project_hooks="$(git rev-parse --git-common-dir)/hooks/pre-commit"
if [ -x "$project_hooks" ]; then
	exec "$project_hooks" "$@"
fi
exit 0
`
