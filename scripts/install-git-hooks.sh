#!/bin/sh
# Point git at the hooks checked in under .githooks (today: the secret-scan
# pre-commit hook). Idempotent; run by the nix dev shell and by `npm install`
# at the repo root, and safe to run by hand.
#
# core.hooksPath is written to the clone's shared config, so every worktree of
# the clone - AO session worktrees included - picks it up, and being relative
# it resolves inside each worktree: each runs the hooks of its own checkout.
set -eu

git rev-parse --is-inside-work-tree >/dev/null 2>&1 || exit 0

current="$(git config --get core.hooksPath || true)"
if [ "${current}" = ".githooks" ]; then
  exit 0
fi
if [ -n "${current}" ]; then
  echo "install-git-hooks: core.hooksPath is already '${current}'; leaving it. Run 'git config core.hooksPath .githooks' to use this repo's hooks." >&2
  exit 0
fi

hooks_dir="$(git rev-parse --path-format=absolute --git-common-dir)/hooks"
for hook in "${hooks_dir}"/*; do
  case "${hook}" in *.sample) continue ;; esac
  [ -f "${hook}" ] && echo "install-git-hooks: ${hook} will stop running; port it to .githooks if you still need it." >&2
done

git config core.hooksPath .githooks
echo "install-git-hooks: core.hooksPath set to .githooks (secret scan runs on every commit)."
