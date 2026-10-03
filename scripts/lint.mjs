#!/usr/bin/env node
// Backend lint gate behind `npm run lint`: `go test ./...`, then golangci-lint.
//
// golangci-lint keys its cache on package content, not location, and stores
// each finding with the absolute path of the checkout that produced it. With
// the default cache (one per user), a worktree at the same content as another
// one replays that worktree's raw findings under its paths; once that worktree
// is gone, the generated-file filter cannot open those files and the findings
// leak through, so lint goes red on code this branch never had.
//
// So each checkout gets its own cache inside its own git dir
// (`.git/golangci-lint`, or `.git/worktrees/<name>/golangci-lint` for a linked
// worktree): out of the tree, never tracked, still warm across runs, and
// removed together with the worktree. An explicit GOLANGCI_LINT_CACHE wins.
// Node rather than sh so the same entry point works on Windows.

import { spawnSync } from "node:child_process";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const GOLANGCI_LINT = "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const backend = join(root, "backend");

function worktreeCacheDir() {
	const git = spawnSync("git", ["rev-parse", "--path-format=absolute", "--git-dir"], {
		cwd: root,
		encoding: "utf8",
	});
	if (git.status !== 0) return undefined; // not a git checkout: nothing to share a cache with
	return join(git.stdout.trim(), "golangci-lint");
}

const env = { ...process.env };
if (!env.GOLANGCI_LINT_CACHE) {
	const dir = worktreeCacheDir();
	if (dir) env.GOLANGCI_LINT_CACHE = dir;
}

function run(args) {
	const res = spawnSync("go", args, { cwd: backend, env, stdio: "inherit" });
	if (res.error) throw res.error;
	if (res.status !== 0) process.exit(res.status ?? 1);
}

run(["test", "./..."]);
run(["run", GOLANGCI_LINT, "run", "--path-mode=abs", ...process.argv.slice(2)]);
