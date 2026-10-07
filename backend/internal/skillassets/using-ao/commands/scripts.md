# ao scripts

Publish and inspect your task's own worktree of the mobile scripts store. On a project with `mobileScripts`, AO gives each task a git worktree of the store on branch `ao/<session>`, and `$AO_SCRIPTS_STORE` names it. Write and commit scripts there. `ao scripts publish` then merges your commits into the store's main checkout. A crew shares one worktree: qa uses dev's.

Never edit the store's main checkout from a session. Only commits publish: uncommitted files stay in your worktree, and ending the session is refused while any are left.

## Syntax

```
ao scripts status [--session <id>] [--json]
ao scripts publish [--session <id>] [--json]
```

## Flags

| Flag | Description |
|---|---|
| `--session <id>` | Session whose workspace to use. Default: `$AO_SESSION_ID`. A crew member means its dev's workspace. |
| `--json` | Print the daemon's JSON response. |

## Subcommands

---

### ao scripts status

Show the worktree path, its branch and the base branch it publishes into, the uncommitted files, the commits the store does not have yet, and the main checkout's own uncommitted files. A publish cannot touch those files.

```bash
ao scripts status
```

---

### ao scripts publish

Merge the commits on your branch into the store's base branch. A fast forward when the base has not moved, a merge commit when it has. Exits 1 when the publish is refused, with the files and the fix:

| Refusal | What to do |
|---|---|
| `publish_conflict` | Merge the base into your branch in `$AO_SCRIPTS_STORE` (`git -C "$AO_SCRIPTS_STORE" merge <base>`), resolve, commit, publish again. |
| `store_dirty_overlap` | The main checkout has uncommitted edits in files your commits change. Whoever made them commits or removes them there; then publish again. |
| `store_off_base` | The main checkout is not on the base branch. Ask the human; it is a shared checkout. |
| `publish_failed` | Publish again; if it keeps failing, read `git -C <store> status`. |

```bash
git -C "$AO_SCRIPTS_STORE" add projects/nter/login/flow.yaml
git -C "$AO_SCRIPTS_STORE" commit -m "nter: login flow"
ao scripts publish
```
