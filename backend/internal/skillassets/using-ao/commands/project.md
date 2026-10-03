# ao project

Manage projects: register repos, inspect, configure per-project settings, and remove.

## Syntax

```
ao project <subcommand> [args] [flags]
```

## Subcommands

---

### ao project add

Register a local git repo as a project so sessions can be spawned in it. The path must be an existing git repository on disk. With `--as-workspace`, the path may be a parent folder containing direct child git repositories; AO initializes/adopts the parent as the root repo and gitignores children.

**Syntax:**
```
ao project add [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--as-workspace` | Register a parent folder as a workspace project (root-as-repo plus direct child repos) | - |
| `--id string` | Project id | Derived by the daemon from the path |
| `--name string` | Display name | - |
| `--orchestrator-agent string` | Default orchestrator session agent | - |
| `--path string` | Absolute path to the local git repo | Required |
| `--worker-agent string` | Default worker session agent | - |

**Examples:**

```bash
# Register a repo as a project
ao project add --path /Users/harshit/Downloads/side-quests/agent-orchestrator --name "agent-orchestrator"
```

```bash
# Register a workspace (parent folder containing multiple repos)
ao project add --path /Users/harshit/Downloads/side-quests --as-workspace --name "side-quests"
```

---

### ao project ls

List registered projects. Aliases: `ls`, `list`.

**Syntax:**
```
ao project ls [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--json` | Output projects as JSON | - |

**Examples:**

```bash
# List all registered projects
ao project ls
```

---

### ao project get

Fetch one registered project.

**Syntax:**
```
ao project get <id> [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--json` | Output project as JSON | - |

**Examples:**

```bash
# Get details for the agent-orchestrator project
ao project get agent-orchestrator
```

---

### ao project rm

Remove a registered project. Aliases: `rm`, `remove`, `delete`.

**Syntax:**
```
ao project rm <id> [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--json` | Output removal result as JSON | - |
| `-y, --yes` | Skip confirmation prompt | - |

**Examples:**

```bash
# Remove a project (with confirmation)
ao project rm agent-orchestrator
```

```bash
# Remove without prompt
ao project rm agent-orchestrator -y
```

---

### ao project set-config

Set a project's per-project config (branch, session prefix, env, symlinks, post-create, agent model/permissions, role overrides, tracker intake, branch convention, device settings). The config is resolved when a session spawns. Field flags write ONLY the fields they name and leave the rest of the stored config alone; pass the whole object with `--config-json`, or `--clear` to remove all config.

`--config-json` **replaces** the stored config rather than patching it, so it must carry every field you want to keep - read the current one with `ao project get <id> --json`, edit that object, and pass it back whole. A key that is not part of the config is refused with a usage error instead of being dropped.

**Syntax:**
```
ao project set-config <id> [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--clear` | Clear all config | - |
| `--config-json string` | Full config as a JSON object (overrides field flags) | - |
| `--branch-prefix string` | Branch prefix for auto-named branches (required for `custom`; default `feature/` for `gitflow`) | - |
| `--default-branch string` | Base branch new session worktrees are created from | - |
| `--env stringArray` | Env var `KEY=VALUE` forwarded into sessions (repeatable) | - |
| `--git-workflow string` | Branch convention: `none`, `gitflow` or `custom` | `none` |
| `--ios-simulator` | This project targets iOS, so sessions get the Device tab and the `ao sim` guidance | off |
| `--json` | Output the updated project as JSON | - |
| `--mobile-platform string` | With `--mobile-scripts`: the app this repo builds, `ios` (scripts run through `ao sim flow run`) or `android` (through `maestro --device`) | required with `--mobile-scripts` |
| `--mobile-scripts string` | Drive this project's simulators/emulators ONLY through the scripts of this product (its folder in the scripts store, e.g. `nter`); `""` turns it off | off |
| `--mobile-scripts-store string` | With `--mobile-scripts`: the scripts store checkout | `~/Documents/Projects/mobile-ui-scripts` |
| `--model string` | Agent model override (e.g. `claude-opus-4-5`) | - |
| `--no-auto-crew` | Never form a crew automatically; a person can still add a qa by hand | off |
| `--orchestrator-agent string` | Harness override for orchestrator sessions | - |
| `--pause-before-implementing` | A standard/deep worker stops once it understands the task and hands back before implementing | off |
| `--permission string` | Permission mode: `default`, `accept-edits`, `auto`, `bypass-permissions` | - |
| `--post-create stringArray` | Command to run after workspace creation (repeatable) | - |
| `--session-prefix string` | Displayed session-id prefix | - |
| `--symlink stringArray` | Repo-relative path to symlink into workspaces (repeatable) | - |
| `--tracker-assignee string` | Issue assignee required for intake eligibility | - |
| `--tracker-intake` | Enable issue-tracker intake for matching issues | off |
| `--tracker-provider string` | Issue-tracker provider: `github` or `gitlab` | `github` |
| `--tracker-repo string` | Issue-tracker repo (GitHub owner/repo or GitLab group/project) | from git origin |
| `--web-ui` | This project has a web UI, so sessions get the Browser tab | off |
| `--worker-agent string` | Harness override for worker sessions | - |

**Examples:**

```bash
# Set default branch and model for a project
ao project set-config agent-orchestrator --default-branch main --model claude-opus-4-5
```

```bash
# Set an env var and a post-create command
ao project set-config agent-orchestrator --env "NODE_ENV=development" --post-create "npm install"
```

```bash
# A mobile project whose devices are driven only by scripts: the iOS and the
# Android repo of one product share its scripts (projects/nter in the store).
# The three --mobile-* flags write one setting together, so pass them together.
ao project set-config nter-ios-app --mobile-scripts nter --mobile-platform ios
ao project set-config nter-android-app --mobile-scripts nter --mobile-platform android
ao project set-config nter-ios-app --mobile-scripts ""     # turn it off
```

The setting reaches the NEXT worker spawned (or restored) in that project: its prompt then teaches the script workflow in place of step-by-step `ao sim` driving, and its qa plays smoke cases with scripts. A project that does not set it is unchanged.
