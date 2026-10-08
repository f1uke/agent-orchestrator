# ao project

Register, inspect, configure and remove the repos AO spawns sessions in: `ao project add`,
`ao project ls`, `ao project get`, `ao project rm`, `ao project set-config`. Flags, and what
every config field means: `ao project <command> --help`.

## Register a repo

```bash
ao project add --path /abs/path/to/repo --name "my-repo"
ao project add --path /abs/path/to/parent --as-workspace --name "side-quests"   # a folder of repos
```

`--path` must be an existing git repository. With `--as-workspace` it may be a parent folder
of git repos: AO adopts the parent as the root repo and gitignores the children.

## Change a project's config

```bash
ao project set-config nter-ios-app --default-branch develop --model claude-opus-5-5
```

- **Field flags write only the fields you name**; every other setting keeps its stored value.
- **A repeatable flag writes its whole list.** `--env FEATURE_X=1` leaves the project with that
  one variable. To add one, read the current pairs with `ao project get <id> --json` and pass
  them all with the new one. The same holds for `--symlink`, `--post-create` and
  `--sim-trust-ca`; `""` clears the list.
- **`--config-json` REPLACES the whole stored config.** Use it only for a setting no flag
  covers: read `ao project get <id> --json`, edit that object, and pass it back whole. Passing
  a partial object drops every field it leaves out.
- **A change reaches the NEXT session spawned or restored in that project**, not the ones
  already running.
- **`--claude-profile <name>`** sets the Claude profile (`ao claude-profile ls`) the project's
  claude-code sessions and reviewers launch with; `""` goes back to `Subscription`. A running
  session keeps its own profile: switch it with `ao session set-claude-profile`.

## Script-only mobile projects

```bash
# The --mobile-* flags write one setting together, so pass them together.
ao project set-config nter-ios-app --mobile-scripts nter --mobile-platform ios \
  --mobile-scripts-verify-skill projects/nter/verify-ios
ao project set-config nter-android-app --mobile-scripts nter --mobile-platform android
ao project set-config nter-ios-app --mobile-scripts ""            # turn it off
```

The iOS and Android repos of one product share its scripts (`projects/nter` in the store). The
next worker's prompt then teaches the script workflow in place of the `ao sim` catalog: every
check and every piece of evidence is a script run, dev and a solo worker may drive by hand only
while debugging, and qa only to author a missing script. AO links the verify skill into each
worktree as `.claude/skills/verify` once its folder holds a `SKILL.md`.

## Remove a project

`ao project rm <id>` asks for confirmation; `-y` skips it.
