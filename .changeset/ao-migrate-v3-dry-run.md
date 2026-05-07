---
"@aoagents/ao-core": minor
"@aoagents/ao-cli": minor
---

Add `ao migrate` (replaces `ao migrate-storage`). Inventories the AO storage tree, detects identity-system drift (V1 bare-basename projectIds, doubled-prefix and storageKey-prefixed tmux names, numbered orchestrators, legacy workspacePaths, observability-dir leaks, stranded `~/.worktrees/` leaves, same-repo duplicate registrations, lingering `storageKey` schema fields) and prints a step-by-step V3 plan plus a structured JSON record (`--json [--output <path>]`).

Execution is gated in this release: `ao migrate --execute` and `ao migrate --rollback` print a feedback notice and exit 1. The intent is to collect dry-run output from real users before any disk writes land.

`ao migrate-storage` is removed from the CLI registry; the V1→V2 helpers stay internal in `@aoagents/ao-core` so the new `ao migrate --dry-run` can detect and report on V1 hash directories. The `ao start` legacy-storage warning now points at `ao migrate --dry-run`.

Public API additions in `@aoagents/ao-core`: `inventoryV3`, `planV3`, `formatBytes`, plus types `V3Inventory`, `V3Plan`, `V3Step`, `V3Issue`, `V3IssueKind`, `V3ProjectInventory`, `V3StrandedWorktree`, `V3LiveTmuxSession`, `V3DuplicateRepo`, `V3InventoryOptions`.
