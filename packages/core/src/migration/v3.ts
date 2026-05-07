/**
 * Storage V3 inventory + plan generator.
 *
 * Pure functions over the AO base directory. NO file system writes.
 * The CLI command (`ao migrate`) consumes this output to render the human plan
 * and the JSON record. Execution is gated until v0.6.1.
 *
 * Design context:
 *   - V3 keeps the V2 `{basename}_{hash10}` projectId format but applies it
 *     uniformly. V1 bare-basename entries get re-keyed; V2 entries pass through.
 *   - Identifies leaks (observability dirs, stranded worktrees) and metadata
 *     drift (doubled-prefix tmux names, legacy storageKey-prefixed names,
 *     numbered orchestrators) so the dry-run output can be reviewed before any
 *     execution lands.
 *   - Reuses inventoryHashDirs from storage-v2.ts for V1 detection so we keep
 *     the V1→V3 path in one PR (rather than V1→V2→V3 separately).
 */

import {
  existsSync,
  readFileSync,
  readdirSync,
  statSync,
  type Stats,
} from "node:fs";
import { join } from "node:path";
import { parse as parseYaml } from "yaml";

import { generateExternalId } from "../global-config.js";
import {
  detectActiveSessions,
  inventoryHashDirs,
  type HashDirEntry,
} from "./storage-v2.js";

// ---------------------------------------------------------------------------
// Public types
// ---------------------------------------------------------------------------

export type V3IssueKind =
  | "v1-bare-basename"
  | "storageKey-field-present"
  | "doubled-prefix-tmux"
  | "legacy-tmux-in-metadata"
  | "legacy-workspace-path"
  | "numbered-orchestrator"
  | "stranded-worktree"
  | "duplicate-repo"
  | "observability-leak"
  | "stranded-legacy-hash-dir";

export interface V3Issue {
  kind: V3IssueKind;
  detail: string;
  ref?: string;
}

export interface V3ProjectInventory {
  projectId: string;
  layout: "v1-bare" | "v2-hashed";
  rekeyTo: string | null; // proposed V3 id (null if already V2)
  path: string | null;
  realpath: string | null;
  originUrl: string | null;
  sessionPrefix: string | null;
  storageKeyField: string | null;
  sessionsCount: number;
  archiveCount: number;
  worktreesCount: number;
  orchestratorVariants: string[];
  liveOrchestratorTmuxName: string | null;
  legacyTmuxNamesInMetadata: number;
  legacyWorkspacePathsInMetadata: number;
  bytes: number;
  issues: V3Issue[];
}

export interface V3StrandedWorktree {
  path: string;
  branch: string | null;
  candidateProjectId: string | null;
  candidateSessionId: string | null;
}

export interface V3LiveTmuxSession {
  name: string;
  convention: "v3" | "doubled-prefix" | "legacy-storagekey" | "unknown";
}

export interface V3DuplicateRepo {
  originUrl: string;
  projectIds: string[];
}

export interface V3Inventory {
  schemaVersion: 3;
  scannedAt: string;
  aoBaseDir: string;
  totals: {
    bytes: number;
    sessions: number;
    worktrees: number;
    observabilityDirs: number;
  };
  projects: V3ProjectInventory[];
  observability: {
    rootLevelDirCount: number;
    bytes: number;
    oldestModifiedAt: string | null;
  };
  strandedWorktrees: V3StrandedWorktree[];
  bareHashDirs: string[];
  migratedDirs: string[];
  liveTmuxSessions: V3LiveTmuxSession[];
  duplicateRepos: V3DuplicateRepo[];
  v1HashDirs: HashDirEntry[];
  globalConfigIssues: V3Issue[];
}

export interface V3Step {
  order: number;
  id: string;
  title: string;
  description: string;
  count: number;
  details: string[];
}

export interface V3Plan {
  schemaVersion: 3;
  generatedAt: string;
  aoVersion: string;
  inventory: V3Inventory;
  steps: V3Step[];
  totals: {
    projectsToRekey: number;
    sessionsToRewrite: number;
    tmuxRenames: number;
    worktreeAdoptions: number;
    orchestratorsToNormalize: number;
    observabilityDirsToCollapse: number;
    bareHashDirsToRemove: number;
    storageKeyFieldsToStrip: number;
    estimatedBytesFreed: number;
  };
  warnings: string[];
  blockers: string[];
}

export interface InventoryOptions {
  /** Base directory to scan. Defaults to `~/.agent-orchestrator`. */
  aoBaseDir: string;
  /** Path to global config (`config.yaml`). */
  globalConfigPath?: string;
  /** Optional override for the legacy worktree root (`~/.worktrees`). */
  legacyWorktreeRoot?: string;
  /** Skip tmux probe (faster + offline tests). */
  skipTmux?: boolean;
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

/** V2 tmux name pattern: {prefix}-{N}, {prefix}-orchestrator, {prefix}-orchestrator-{N}. */
const V3_TMUX_PATTERN = /^[a-z0-9][a-zA-Z0-9_-]*(?:-\d+|-orchestrator(?:-\d+)?)$/;

/** Legacy storageKey-prefixed tmux: {12-hex}-... */
const LEGACY_STORAGEKEY_TMUX = /^[0-9a-f]{12}-/;

/** Doubled prefix: {prefix}-{prefix}-orchestrator (the ao-ao-orchestrator bug). */
function isDoubledPrefix(name: string, knownPrefixes: Set<string>): boolean {
  for (const prefix of knownPrefixes) {
    if (name === `${prefix}-${prefix}-orchestrator`) return true;
    if (name.startsWith(`${prefix}-${prefix}-`)) return true;
  }
  return false;
}

// ---------------------------------------------------------------------------
// Inventory
// ---------------------------------------------------------------------------

export async function inventoryV3(opts: InventoryOptions): Promise<V3Inventory> {
  const { aoBaseDir, globalConfigPath, legacyWorktreeRoot, skipTmux } = opts;
  const scannedAt = new Date().toISOString();

  if (!existsSync(aoBaseDir)) {
    return emptyInventory(aoBaseDir, scannedAt);
  }

  const globalConfig = readGlobalConfigRaw(globalConfigPath);
  const projectsRoot = join(aoBaseDir, "projects");

  // Walk projects/{id}/
  const projects: V3ProjectInventory[] = [];
  const knownPrefixes = new Set<string>();

  if (existsSync(projectsRoot) && statSync(projectsRoot).isDirectory()) {
    for (const projectId of readdirSync(projectsRoot)) {
      if (projectId.startsWith(".")) continue;
      const projectDir = join(projectsRoot, projectId);
      let projectStat: Stats;
      try {
        projectStat = statSync(projectDir);
      } catch {
        continue;
      }
      if (!projectStat.isDirectory()) continue;

      const inv = inventoryProject(projectId, projectDir, globalConfig);
      projects.push(inv);
      if (inv.sessionPrefix) knownPrefixes.add(inv.sessionPrefix);
    }
  }

  // Observability leak
  const observability = inventoryObservabilityDirs(aoBaseDir);

  // Bare hash dirs + .migrated
  const bareHashDirs: string[] = [];
  const migratedDirs: string[] = [];
  for (const name of readdirSync(aoBaseDir)) {
    if (/^[0-9a-f]{12}$/.test(name)) bareHashDirs.push(name);
    if (/\.migrated$/.test(name)) migratedDirs.push(name);
  }

  // Stranded worktrees in ~/.worktrees/
  const strandedWorktrees = inventoryStrandedWorktrees(legacyWorktreeRoot, projects);

  // Live tmux sessions
  const liveTmuxSessions = skipTmux
    ? []
    : await inventoryLiveTmuxSessions(knownPrefixes);

  // Duplicate repos by originUrl
  const duplicateRepos = inventoryDuplicateRepos(projects);

  // V1 hash dirs (for V1→V3 in one pass)
  const v1HashDirs = inventoryHashDirs(aoBaseDir, globalConfigPath);

  // Global config issues
  const globalConfigIssues = inventoryGlobalConfigIssues(globalConfig, projects);

  // Totals
  const totals = {
    bytes:
      projects.reduce((sum, p) => sum + p.bytes, 0) +
      observability.bytes,
    sessions: projects.reduce((sum, p) => sum + p.sessionsCount, 0),
    worktrees: projects.reduce((sum, p) => sum + p.worktreesCount, 0),
    observabilityDirs: observability.rootLevelDirCount,
  };

  return {
    schemaVersion: 3,
    scannedAt,
    aoBaseDir,
    totals,
    projects,
    observability,
    strandedWorktrees,
    bareHashDirs,
    migratedDirs,
    liveTmuxSessions,
    duplicateRepos,
    v1HashDirs,
    globalConfigIssues,
  };
}

function emptyInventory(aoBaseDir: string, scannedAt: string): V3Inventory {
  return {
    schemaVersion: 3,
    scannedAt,
    aoBaseDir,
    totals: { bytes: 0, sessions: 0, worktrees: 0, observabilityDirs: 0 },
    projects: [],
    observability: { rootLevelDirCount: 0, bytes: 0, oldestModifiedAt: null },
    strandedWorktrees: [],
    bareHashDirs: [],
    migratedDirs: [],
    liveTmuxSessions: [],
    duplicateRepos: [],
    v1HashDirs: [],
    globalConfigIssues: [],
  };
}

// ---------------------------------------------------------------------------
// Per-project inventory
// ---------------------------------------------------------------------------

interface RawGlobalConfig {
  projects: Record<string, Record<string, unknown>>;
}

function readGlobalConfigRaw(globalConfigPath?: string): RawGlobalConfig {
  if (!globalConfigPath || !existsSync(globalConfigPath)) {
    return { projects: {} };
  }
  try {
    const text = readFileSync(globalConfigPath, "utf-8");
    const parsed = parseYaml(text) as Record<string, unknown> | null;
    const projects =
      parsed && typeof parsed["projects"] === "object" && parsed["projects"]
        ? (parsed["projects"] as Record<string, Record<string, unknown>>)
        : {};
    return { projects };
  } catch {
    return { projects: {} };
  }
}

function isV2HashedId(projectId: string): boolean {
  // V2 format: {sanitized basename, max 30}_{10 hex}
  return /^[a-z0-9][a-z0-9_-]{0,29}_[0-9a-f]{10}$/.test(projectId);
}

function inventoryProject(
  projectId: string,
  projectDir: string,
  globalConfig: RawGlobalConfig,
): V3ProjectInventory {
  const issues: V3Issue[] = [];
  const layout: "v1-bare" | "v2-hashed" = isV2HashedId(projectId)
    ? "v2-hashed"
    : "v1-bare";

  const registryEntry = globalConfig.projects[projectId];
  const path = typeof registryEntry?.["path"] === "string" ? registryEntry["path"] : null;
  const realpath = path; // resolving here would do FS work; keep raw for inventory
  const repo = registryEntry?.["repo"] as Record<string, unknown> | undefined;
  const originUrl = typeof repo?.["originUrl"] === "string" ? repo["originUrl"] : null;
  const sessionPrefix =
    typeof registryEntry?.["sessionPrefix"] === "string"
      ? registryEntry["sessionPrefix"]
      : null;
  const storageKeyField =
    typeof registryEntry?.["storageKey"] === "string" ? registryEntry["storageKey"] : null;

  // Re-key target (for V1 → V3); null if already V2
  let rekeyTo: string | null = null;
  if (layout === "v1-bare") {
    if (path) {
      rekeyTo = generateExternalId(path, originUrl);
    } else {
      // Without a path we can't compute; flag it
      rekeyTo = null;
    }
    issues.push({
      kind: "v1-bare-basename",
      detail: `Project "${projectId}" uses bare-basename layout; would re-key to "${rekeyTo ?? "(unable: no path)"}".`,
      ref: projectId,
    });
  }

  if (storageKeyField) {
    issues.push({
      kind: "storageKey-field-present",
      detail: `Project "${projectId}" still has the legacy storageKey field "${storageKeyField}" in config.yaml.`,
      ref: projectId,
    });
  }

  // Walk sessions/
  const sessionsDir = join(projectDir, "sessions");
  let sessionsCount = 0;
  let archiveCount = 0;
  const orchestratorVariants: string[] = [];
  let legacyTmuxNamesInMetadata = 0;
  let legacyWorkspacePathsInMetadata = 0;

  if (existsSync(sessionsDir)) {
    for (const entry of readdirSync(sessionsDir)) {
      if (entry === "archive") {
        archiveCount = countNonHidden(join(sessionsDir, "archive"));
        continue;
      }
      if (entry.startsWith(".")) continue;
      const sessionPath = join(sessionsDir, entry);
      let stat: Stats;
      try {
        stat = statSync(sessionPath);
      } catch {
        continue;
      }
      if (!stat.isFile()) continue;

      sessionsCount += 1;

      const sessionMeta = readSessionMeta(sessionPath);
      const sessionId = sessionMeta?.["sessionId"] ?? entry.replace(/\.json$/, "");

      // Numbered orchestrator detection: matches {prefix}-orchestrator-{N}
      if (typeof sessionId === "string" && /-orchestrator-\d+$/.test(sessionId)) {
        orchestratorVariants.push(sessionId);
        issues.push({
          kind: "numbered-orchestrator",
          detail: `Session "${sessionId}" uses a numbered orchestrator suffix; should normalize to "${sessionPrefix ?? "<prefix>"}-orchestrator".`,
          ref: sessionPath,
        });
      } else if (
        typeof sessionId === "string" &&
        sessionPrefix &&
        sessionId === `${sessionPrefix}-orchestrator`
      ) {
        orchestratorVariants.push(sessionId);
      }

      // Legacy tmuxName / workspacePath checks
      const tmuxName = sessionMeta?.["tmuxName"];
      if (typeof tmuxName === "string") {
        if (LEGACY_STORAGEKEY_TMUX.test(tmuxName)) {
          legacyTmuxNamesInMetadata += 1;
          issues.push({
            kind: "legacy-tmux-in-metadata",
            detail: `Session JSON has legacy storageKey-prefixed tmuxName "${tmuxName}".`,
            ref: sessionPath,
          });
        } else if (
          sessionPrefix &&
          tmuxName.startsWith(`${sessionPrefix}-${sessionPrefix}-`)
        ) {
          legacyTmuxNamesInMetadata += 1;
          issues.push({
            kind: "doubled-prefix-tmux",
            detail: `Session JSON has doubled-prefix tmuxName "${tmuxName}".`,
            ref: sessionPath,
          });
        }
      }

      const workspacePath = sessionMeta?.["workspacePath"];
      if (typeof workspacePath === "string" && workspacePath.includes("/.worktrees/")) {
        legacyWorkspacePathsInMetadata += 1;
        issues.push({
          kind: "legacy-workspace-path",
          detail: `Session JSON workspacePath points at legacy ~/.worktrees/ tree: "${workspacePath}".`,
          ref: sessionPath,
        });
      }
    }
  }

  // orchestrator.json (singleton, alongside sessions/)
  const orchestratorJson = join(projectDir, "orchestrator.json");
  let liveOrchestratorTmuxName: string | null = null;
  if (existsSync(orchestratorJson)) {
    const meta = readSessionMeta(orchestratorJson);
    const tmuxName = meta?.["tmuxName"];
    if (typeof tmuxName === "string") {
      liveOrchestratorTmuxName = tmuxName;
      if (LEGACY_STORAGEKEY_TMUX.test(tmuxName)) {
        legacyTmuxNamesInMetadata += 1;
        issues.push({
          kind: "legacy-tmux-in-metadata",
          detail: `orchestrator.json has legacy storageKey-prefixed tmuxName "${tmuxName}".`,
          ref: orchestratorJson,
        });
      }
      if (
        sessionPrefix &&
        tmuxName.startsWith(`${sessionPrefix}-${sessionPrefix}-`)
      ) {
        issues.push({
          kind: "doubled-prefix-tmux",
          detail: `orchestrator.json has doubled-prefix tmuxName "${tmuxName}".`,
          ref: orchestratorJson,
        });
      }
    }
    const ws = meta?.["runtimeHandle"];
    if (
      ws &&
      typeof ws === "object" &&
      typeof (ws as Record<string, unknown>)["data"] === "object"
    ) {
      const wsPath = (
        (ws as Record<string, unknown>)["data"] as Record<string, unknown>
      )["workspacePath"];
      if (typeof wsPath === "string" && wsPath.includes("/.worktrees/")) {
        legacyWorkspacePathsInMetadata += 1;
        issues.push({
          kind: "legacy-workspace-path",
          detail: `orchestrator.json runtimeHandle.data.workspacePath points at legacy ~/.worktrees/ tree: "${wsPath}".`,
          ref: orchestratorJson,
        });
      }
    }
  }

  // Worktrees
  const worktreesDir = join(projectDir, "worktrees");
  let worktreesCount = 0;
  if (existsSync(worktreesDir)) {
    for (const entry of readdirSync(worktreesDir)) {
      if (entry.startsWith(".")) continue;
      try {
        if (statSync(join(worktreesDir, entry)).isDirectory()) worktreesCount += 1;
      } catch {
        // ignore
      }
    }
  }

  const bytes = directoryBytes(projectDir);

  return {
    projectId,
    layout,
    rekeyTo,
    path,
    realpath,
    originUrl,
    sessionPrefix,
    storageKeyField,
    sessionsCount,
    archiveCount,
    worktreesCount,
    orchestratorVariants,
    liveOrchestratorTmuxName,
    legacyTmuxNamesInMetadata,
    legacyWorkspacePathsInMetadata,
    bytes,
    issues,
  };
}

function readSessionMeta(filePath: string): Record<string, unknown> | null {
  try {
    const content = readFileSync(filePath, "utf-8").trim();
    if (!content) return null;
    if (content.startsWith("{")) {
      return JSON.parse(content) as Record<string, unknown>;
    }
    return null;
  } catch {
    return null;
  }
}

function countNonHidden(dir: string): number {
  if (!existsSync(dir)) return 0;
  try {
    return readdirSync(dir).filter((n) => !n.startsWith(".")).length;
  } catch {
    return 0;
  }
}

function directoryBytes(dir: string): number {
  let total = 0;
  try {
    for (const entry of readdirSync(dir)) {
      const p = join(dir, entry);
      let s: Stats;
      try {
        s = statSync(p);
      } catch {
        continue;
      }
      if (s.isDirectory()) {
        total += directoryBytes(p);
      } else if (s.isFile()) {
        total += s.size;
      }
    }
  } catch {
    // ignore unreadable
  }
  return total;
}

// ---------------------------------------------------------------------------
// Observability leak inventory
// ---------------------------------------------------------------------------

function inventoryObservabilityDirs(aoBaseDir: string): {
  rootLevelDirCount: number;
  bytes: number;
  oldestModifiedAt: string | null;
} {
  let count = 0;
  let bytes = 0;
  let oldest: number | null = null;

  for (const name of readdirSync(aoBaseDir)) {
    if (!name.endsWith("-observability")) continue;
    const p = join(aoBaseDir, name);
    let s: Stats;
    try {
      s = statSync(p);
    } catch {
      continue;
    }
    if (!s.isDirectory()) continue;
    count += 1;
    bytes += directoryBytes(p);
    const mtime = s.mtimeMs;
    if (oldest === null || mtime < oldest) oldest = mtime;
  }

  return {
    rootLevelDirCount: count,
    bytes,
    oldestModifiedAt: oldest === null ? null : new Date(oldest).toISOString(),
  };
}

// ---------------------------------------------------------------------------
// Stranded worktrees
// ---------------------------------------------------------------------------

function inventoryStrandedWorktrees(
  legacyWorktreeRoot: string | undefined,
  projects: V3ProjectInventory[],
): V3StrandedWorktree[] {
  if (!legacyWorktreeRoot || !existsSync(legacyWorktreeRoot)) return [];
  const out: V3StrandedWorktree[] = [];

  for (const projectName of readdirSync(legacyWorktreeRoot)) {
    const projectDir = join(legacyWorktreeRoot, projectName);
    let s: Stats;
    try {
      s = statSync(projectDir);
    } catch {
      continue;
    }
    if (!s.isDirectory()) continue;

    for (const wtName of readdirSync(projectDir)) {
      const wtPath = join(projectDir, wtName);
      try {
        if (!statSync(wtPath).isDirectory()) continue;
      } catch {
        continue;
      }

      // Try to resolve a candidate project + session by sessionPrefix and worktree name.
      const candidate = projects.find(
        (p) =>
          p.sessionPrefix !== null &&
          (wtName.startsWith(`${p.sessionPrefix}-`) ||
            wtName === `${p.sessionPrefix}-orchestrator`),
      );

      out.push({
        path: wtPath,
        branch: null, // could read via `git -C wtPath branch --show-current`; defer
        candidateProjectId: candidate?.projectId ?? null,
        candidateSessionId: candidate ? wtName : null,
      });
    }
  }

  return out;
}

// ---------------------------------------------------------------------------
// Live tmux sessions
// ---------------------------------------------------------------------------

async function inventoryLiveTmuxSessions(
  knownPrefixes: Set<string>,
): Promise<V3LiveTmuxSession[]> {
  let sessionNames: string[];
  try {
    sessionNames = await detectActiveSessions(Array.from(knownPrefixes));
  } catch {
    sessionNames = [];
  }

  return sessionNames.map((name) => ({
    name,
    convention: classifyTmuxName(name, knownPrefixes),
  }));
}

function classifyTmuxName(
  name: string,
  knownPrefixes: Set<string>,
): V3LiveTmuxSession["convention"] {
  if (LEGACY_STORAGEKEY_TMUX.test(name)) return "legacy-storagekey";
  if (isDoubledPrefix(name, knownPrefixes)) return "doubled-prefix";
  if (V3_TMUX_PATTERN.test(name)) return "v3";
  return "unknown";
}

// ---------------------------------------------------------------------------
// Duplicate repos
// ---------------------------------------------------------------------------

function inventoryDuplicateRepos(projects: V3ProjectInventory[]): V3DuplicateRepo[] {
  const byOrigin = new Map<string, string[]>();
  for (const p of projects) {
    if (!p.originUrl) continue;
    const ids = byOrigin.get(p.originUrl) ?? [];
    ids.push(p.projectId);
    byOrigin.set(p.originUrl, ids);
  }
  const out: V3DuplicateRepo[] = [];
  for (const [originUrl, projectIds] of byOrigin) {
    if (projectIds.length > 1) {
      out.push({ originUrl, projectIds });
    }
  }
  return out;
}

// ---------------------------------------------------------------------------
// Global config issues
// ---------------------------------------------------------------------------

function inventoryGlobalConfigIssues(
  globalConfig: RawGlobalConfig,
  projects: V3ProjectInventory[],
): V3Issue[] {
  const issues: V3Issue[] = [];

  // Per-project storageKey strip
  for (const [pid, entry] of Object.entries(globalConfig.projects)) {
    if (typeof entry["storageKey"] === "string") {
      issues.push({
        kind: "storageKey-field-present",
        detail: `Global config project "${pid}" still has storageKey="${entry["storageKey"]}".`,
        ref: pid,
      });
    }
  }

  // Project IDs in registry but not on disk
  const onDisk = new Set(projects.map((p) => p.projectId));
  for (const pid of Object.keys(globalConfig.projects)) {
    if (!onDisk.has(pid)) {
      issues.push({
        kind: "stranded-legacy-hash-dir",
        detail: `Project "${pid}" is in config.yaml but no projects/${pid}/ directory exists.`,
        ref: pid,
      });
    }
  }

  return issues;
}

// ---------------------------------------------------------------------------
// Plan generation
// ---------------------------------------------------------------------------

export function planV3(inventory: V3Inventory, aoVersion: string): V3Plan {
  const generatedAt = new Date().toISOString();
  const steps: V3Step[] = [];

  // Step 1: Re-key V1 entries
  const v1Projects = inventory.projects.filter((p) => p.layout === "v1-bare");
  if (v1Projects.length > 0) {
    steps.push({
      order: 1,
      id: "rekey-v1-entries",
      title: "Re-key V1 bare-basename projects to V2 format",
      description:
        "Compute generateExternalId(realpath, originUrl) for each V1 project; rename projects/{old} → projects/{new}; update config.yaml registry key.",
      count: v1Projects.length,
      details: v1Projects.map(
        (p) => `${p.projectId} → ${p.rekeyTo ?? "(unable: missing path)"}`,
      ),
    });
  }

  // Step 2: Same-repo merge prompt
  if (inventory.duplicateRepos.length > 0) {
    steps.push({
      order: 2,
      id: "same-repo-merge",
      title: "Detect same-repo dual registrations",
      description:
        "Projects sharing the same originUrl are candidates for merging. User confirms; default keeps both.",
      count: inventory.duplicateRepos.length,
      details: inventory.duplicateRepos.map(
        (d) => `${d.originUrl}: ${d.projectIds.join(" + ")}`,
      ),
    });
  }

  // Step 3: Path renames (covered by step 1 for V1 entries)

  // Step 4: Write identity.json (per project)
  steps.push({
    order: 4,
    id: "write-identity-json",
    title: "Write identity.json into each project directory",
    description:
      "Per project: write projects/{id}/identity.json with displayName, originUrl, path, realpath, sessionPrefix, repo, defaultBranch, schemaVersion: 3.",
    count: inventory.projects.length,
    details: inventory.projects.map((p) => `projects/${p.rekeyTo ?? p.projectId}/identity.json`),
  });

  // Step 5: Reconcile session counter
  steps.push({
    order: 5,
    id: "reconcile-counter",
    title: "Write .next-session-id.json per project",
    description:
      "Scan sessions/, find max(N) per prefix, advance counter, write .next-session-id.json. The remote scan stays as a reconciler-only fallback.",
    count: inventory.projects.length,
    details: inventory.projects.map(
      (p) => `projects/${p.rekeyTo ?? p.projectId}/.next-session-id.json`,
    ),
  });

  // Step 6: Rewrite session JSONs (legacy tmux + workspace paths)
  const sessionsToRewrite =
    inventory.projects.reduce(
      (sum, p) => sum + p.legacyTmuxNamesInMetadata + p.legacyWorkspacePathsInMetadata,
      0,
    );
  if (sessionsToRewrite > 0) {
    steps.push({
      order: 6,
      id: "rewrite-session-metadata",
      title: "Rewrite session JSONs with stale tmuxName / workspacePath",
      description:
        "For every session whose tmuxName uses legacy storageKey prefix, doubled prefix, or whose workspacePath points at ~/.worktrees/, rewrite to V3 format. tmuxName ≡ sessionId.",
      count: sessionsToRewrite,
      details: inventory.projects
        .filter(
          (p) => p.legacyTmuxNamesInMetadata + p.legacyWorkspacePathsInMetadata > 0,
        )
        .map(
          (p) =>
            `${p.projectId}: ${p.legacyTmuxNamesInMetadata} legacy tmux + ${p.legacyWorkspacePathsInMetadata} legacy paths`,
        ),
    });
  }

  // Step 7: Tmux session renames (live)
  const tmuxRenames = inventory.liveTmuxSessions.filter(
    (t) => t.convention === "doubled-prefix" || t.convention === "legacy-storagekey",
  );
  if (tmuxRenames.length > 0) {
    steps.push({
      order: 7,
      id: "rename-tmux-sessions",
      title: "Rename live tmux sessions to V3 names",
      description:
        "tmux rename-session for each non-V3 live session. Failure to rename = warn + continue (user can re-attach manually).",
      count: tmuxRenames.length,
      details: tmuxRenames.map((t) => `${t.name} (${t.convention})`),
    });
  }

  // Step 8: Adopt stranded worktrees
  if (inventory.strandedWorktrees.length > 0) {
    steps.push({
      order: 8,
      id: "adopt-stranded-worktrees",
      title: "Adopt stranded ~/.worktrees/ leaves into projects/{id}/worktrees/",
      description:
        "For each leaf in ~/.worktrees/{name}/{sid}: find session JSON whose branch matches; mv into projects/{id}/worktrees/{sid}; rewrite workspacePath; run git worktree repair.",
      count: inventory.strandedWorktrees.length,
      details: inventory.strandedWorktrees.map(
        (w) =>
          `${w.path} → ${
            w.candidateProjectId
              ? `projects/${w.candidateProjectId}/worktrees/${w.candidateSessionId ?? "?"}`
              : "(no candidate match — adopt with explicit flag)"
          }`,
      ),
    });
  }

  // Step 8b: Normalize numbered orchestrators
  const projectsWithNumberedOrchestrator = inventory.projects.filter(
    (p) => p.orchestratorVariants.some((v) => /-orchestrator-\d+$/.test(v)),
  );
  if (projectsWithNumberedOrchestrator.length > 0) {
    const totalNumbered = projectsWithNumberedOrchestrator.reduce(
      (sum, p) =>
        sum + p.orchestratorVariants.filter((v) => /-orchestrator-\d+$/.test(v)).length,
      0,
    );
    steps.push({
      order: 9,
      id: "normalize-orchestrators",
      title: "Normalize numbered orchestrators to one-per-project",
      description:
        "For each project with multiple {prefix}-orchestrator-N entries, pick the most recent live one as canonical {prefix}-orchestrator and archive the rest. Detection regex tightens to ^{prefix}-orchestrator$.",
      count: totalNumbered,
      details: projectsWithNumberedOrchestrator.map(
        (p) =>
          `${p.projectId}: ${p.orchestratorVariants.filter((v) => /-orchestrator-\d+$/.test(v)).length} numbered variants`,
      ),
    });
  }

  // Step 9: Collapse observability
  if (inventory.observability.rootLevelDirCount > 0) {
    steps.push({
      order: 10,
      id: "collapse-observability",
      title: "Collapse root-level *-observability dirs into projects/{id}/observability/",
      description:
        "Read each obs JSON's projectId field and route into the matching project; unattributable files go to ~/.agent-orchestrator/observability/orphan/. Remove emptied {hash}-observability dirs.",
      count: inventory.observability.rootLevelDirCount,
      details: [
        `${inventory.observability.rootLevelDirCount} dirs (~${formatBytes(
          inventory.observability.bytes,
        )} of obs data)`,
      ],
    });
  }

  // Step 10: Strip storageKey
  const storageKeyFieldsToStrip = inventory.globalConfigIssues.filter(
    (i) => i.kind === "storageKey-field-present",
  ).length;
  if (storageKeyFieldsToStrip > 0) {
    steps.push({
      order: 11,
      id: "strip-storage-key",
      title: "Strip storageKey field from config.yaml entries",
      description:
        "Walk config.yaml, remove storageKey from every project entry. Bump global schemaVersion to 3.",
      count: storageKeyFieldsToStrip,
      details: inventory.globalConfigIssues
        .filter((i) => i.kind === "storageKey-field-present")
        .map((i) => i.ref ?? "")
        .filter((r) => r),
    });
  }

  // Step 11: GC bare hash + .migrated dirs
  if (inventory.bareHashDirs.length + inventory.migratedDirs.length > 0) {
    steps.push({
      order: 12,
      id: "gc-stranded-dirs",
      title: "GC bare hash and .migrated directories",
      description:
        "Delete bare {12-hex}/ and {12-hex}.migrated/ at root after safety check (must be empty or completed-migration markers).",
      count: inventory.bareHashDirs.length + inventory.migratedDirs.length,
      details: [...inventory.bareHashDirs, ...inventory.migratedDirs],
    });
  }

  // Step 12: Dead-export manifest
  steps.push({
    order: 13,
    id: "dead-export-manifest",
    title: "Write dead-export manifest for follow-up deletion PR",
    description:
      "Emit migrations/v3-{ts}.dead-exports.txt listing exports from @aoagents/ao-core with zero non-test callers. Migrator does NOT delete code.",
    count: 12,
    details: [
      "deriveStorageKey",
      "generateTmuxName",
      "parseTmuxName",
      "getProjectBaseDir",
      "getSessionsDir",
      "getWorktreesDir",
      "getFeedbackReportsDir",
      "getArchiveDir",
      "getOriginFilePath",
      "validateAndStoreOrigin",
      "requireStorageKey",
      "generateConfigHash",
    ],
  });

  // Totals
  const totals = {
    projectsToRekey: v1Projects.length,
    sessionsToRewrite,
    tmuxRenames: tmuxRenames.length,
    worktreeAdoptions: inventory.strandedWorktrees.length,
    orchestratorsToNormalize: projectsWithNumberedOrchestrator.reduce(
      (sum, p) =>
        sum + p.orchestratorVariants.filter((v) => /-orchestrator-\d+$/.test(v)).length,
      0,
    ),
    observabilityDirsToCollapse: inventory.observability.rootLevelDirCount,
    bareHashDirsToRemove: inventory.bareHashDirs.length,
    storageKeyFieldsToStrip,
    estimatedBytesFreed:
      inventory.observability.bytes +
      inventory.bareHashDirs.length * 1024 +
      inventory.migratedDirs.length * 1024,
  };

  // Warnings + blockers (informational; --execute is gated regardless)
  const warnings: string[] = [];
  const blockers: string[] = [];

  if (inventory.liveTmuxSessions.length > 0) {
    warnings.push(
      `${inventory.liveTmuxSessions.length} live tmux session(s) detected. Execution would refuse without --force.`,
    );
  }
  if (inventory.duplicateRepos.length > 0) {
    warnings.push(
      `${inventory.duplicateRepos.length} same-repo duplicate(s) detected. Execution would prompt; default merges.`,
    );
  }
  if (inventory.v1HashDirs.length > 0 && v1Projects.length === 0) {
    warnings.push(
      `${inventory.v1HashDirs.length} legacy hash directory layout(s) detected outside the registry. Manual review recommended.`,
    );
  }

  return {
    schemaVersion: 3,
    generatedAt,
    aoVersion,
    inventory,
    steps,
    totals,
    warnings,
    blockers,
  };
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`;
}

export { formatBytes };
