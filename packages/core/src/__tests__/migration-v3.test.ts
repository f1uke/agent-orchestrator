import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { mkdirSync, writeFileSync, rmSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { stringify as stringifyYaml } from "yaml";

import { inventoryV3, planV3 } from "../migration/v3.js";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function createTempDir(): string {
  const dir = join(
    tmpdir(),
    `ao-v3-test-${Date.now()}-${Math.random().toString(36).slice(2)}`,
  );
  mkdirSync(dir, { recursive: true });
  return dir;
}

function writeGlobalConfig(
  aoBaseDir: string,
  projects: Record<string, Record<string, unknown>>,
): string {
  const configPath = join(aoBaseDir, "config.yaml");
  writeFileSync(
    configPath,
    stringifyYaml({
      port: 3000,
      defaults: {},
      projects,
    }),
    "utf-8",
  );
  return configPath;
}

interface SessionFixture {
  sessionId: string;
  tmuxName?: string;
  workspacePath?: string;
  branch?: string;
  kind?: "worker" | "orchestrator";
}

function writeSessionJson(
  projectsDir: string,
  projectId: string,
  fixture: SessionFixture,
): void {
  const sessionsDir = join(projectsDir, projectId, "sessions");
  mkdirSync(sessionsDir, { recursive: true });
  const meta = {
    sessionId: fixture.sessionId,
    kind: fixture.kind ?? "worker",
    project: projectId,
    tmuxName: fixture.tmuxName ?? fixture.sessionId,
    branch: fixture.branch ?? `session/${fixture.sessionId}`,
    workspacePath:
      fixture.workspacePath ??
      join(projectsDir, projectId, "worktrees", fixture.sessionId),
    agent: "claude-code",
    createdAt: "2026-05-07T08:43:35.402Z",
  };
  writeFileSync(
    join(sessionsDir, `${fixture.sessionId}.json`),
    JSON.stringify(meta, null, 2),
    "utf-8",
  );
}

function writeOrchestratorJson(
  projectsDir: string,
  projectId: string,
  fixture: SessionFixture,
): void {
  const meta = {
    sessionId: fixture.sessionId,
    kind: "orchestrator",
    project: projectId,
    tmuxName: fixture.tmuxName ?? fixture.sessionId,
    branch: fixture.branch ?? `orchestrator/${fixture.sessionId}`,
    runtimeHandle: {
      id: fixture.tmuxName ?? fixture.sessionId,
      runtimeName: "tmux",
      data: {
        workspacePath:
          fixture.workspacePath ??
          join(projectsDir, projectId, "worktrees", fixture.sessionId),
      },
    },
    agent: "claude-code",
    createdAt: "2026-04-25T17:32:56.275Z",
  };
  mkdirSync(join(projectsDir, projectId), { recursive: true });
  writeFileSync(
    join(projectsDir, projectId, "orchestrator.json"),
    JSON.stringify(meta, null, 2),
    "utf-8",
  );
}

function writeObservabilityDir(aoBaseDir: string, hash: string): void {
  const dir = join(aoBaseDir, `${hash}-observability`, "processes");
  mkdirSync(dir, { recursive: true });
  writeFileSync(
    join(dir, "session-manager-12345.json"),
    JSON.stringify(
      {
        component: "session-manager",
        pid: 12345,
        projectId: "some-project",
        traces: [],
      },
      null,
      2,
    ),
    "utf-8",
  );
}

// ---------------------------------------------------------------------------
// inventoryV3
// ---------------------------------------------------------------------------

describe("inventoryV3", () => {
  let aoBaseDir: string;
  let projectsDir: string;

  beforeEach(() => {
    aoBaseDir = createTempDir();
    projectsDir = join(aoBaseDir, "projects");
    mkdirSync(projectsDir, { recursive: true });
  });

  afterEach(() => {
    rmSync(aoBaseDir, { recursive: true, force: true });
  });

  it("returns an empty inventory when aoBaseDir does not exist", async () => {
    const missing = join(aoBaseDir, "missing");
    const inv = await inventoryV3({ aoBaseDir: missing, skipTmux: true });
    expect(inv.projects).toHaveLength(0);
    expect(inv.totals.bytes).toBe(0);
    expect(inv.observability.rootLevelDirCount).toBe(0);
  });

  it("classifies V2 hashed and V1 bare-basename projects correctly", async () => {
    // V2 hashed
    mkdirSync(join(projectsDir, "agent-orchestrator_a1b2c3d4e5", "sessions"), {
      recursive: true,
    });
    // V1 bare
    mkdirSync(join(projectsDir, "agent-orchestrator", "sessions"), {
      recursive: true,
    });

    const configPath = writeGlobalConfig(aoBaseDir, {
      "agent-orchestrator_a1b2c3d4e5": {
        path: "/Users/x/v2/agent-orchestrator",
        sessionPrefix: "ao",
        repo: {
          owner: "ComposioHQ",
          name: "agent-orchestrator",
          platform: "github",
          originUrl: "https://github.com/composiohq/agent-orchestrator",
        },
      },
      "agent-orchestrator": {
        path: "/Users/x/v1/agent-orchestrator",
        sessionPrefix: "ao",
        storageKey: "7dc54da05c9e",
        repo: {
          owner: "ComposioHQ",
          name: "agent-orchestrator",
          platform: "github",
          originUrl: "https://github.com/composiohq/agent-orchestrator",
        },
      },
    });

    const inv = await inventoryV3({
      aoBaseDir,
      globalConfigPath: configPath,
      skipTmux: true,
    });

    expect(inv.projects).toHaveLength(2);
    const v2 = inv.projects.find((p) => p.layout === "v2-hashed");
    const v1 = inv.projects.find((p) => p.layout === "v1-bare");
    expect(v2?.projectId).toBe("agent-orchestrator_a1b2c3d4e5");
    expect(v1?.projectId).toBe("agent-orchestrator");
    expect(v1?.rekeyTo).toMatch(/^agent-orchestrator_[0-9a-f]{10}$/);
    expect(v1?.storageKeyField).toBe("7dc54da05c9e");

    // Issues raised for V1
    expect(v1?.issues.some((i) => i.kind === "v1-bare-basename")).toBe(true);
    expect(v1?.issues.some((i) => i.kind === "storageKey-field-present")).toBe(true);
  });

  it("flags duplicate repos by originUrl", async () => {
    mkdirSync(join(projectsDir, "agent-orchestrator", "sessions"), {
      recursive: true,
    });
    mkdirSync(join(projectsDir, "agent-orchestrator_168566536d", "sessions"), {
      recursive: true,
    });

    const configPath = writeGlobalConfig(aoBaseDir, {
      "agent-orchestrator": {
        path: "/Users/x/clones/c1/agent-orchestrator",
        sessionPrefix: "ao",
        repo: {
          owner: "ComposioHQ",
          name: "agent-orchestrator",
          platform: "github",
          originUrl: "https://github.com/composiohq/agent-orchestrator",
        },
      },
      "agent-orchestrator_168566536d": {
        path: "/Users/x/clones/c2/agent-orchestrator",
        sessionPrefix: "ao2",
        repo: {
          owner: "ComposioHQ",
          name: "agent-orchestrator",
          platform: "github",
          originUrl: "https://github.com/composiohq/agent-orchestrator",
        },
      },
    });

    const inv = await inventoryV3({
      aoBaseDir,
      globalConfigPath: configPath,
      skipTmux: true,
    });

    expect(inv.duplicateRepos).toHaveLength(1);
    expect(inv.duplicateRepos[0].projectIds.sort()).toEqual([
      "agent-orchestrator",
      "agent-orchestrator_168566536d",
    ]);
  });

  it("counts observability dir leak", async () => {
    writeObservabilityDir(aoBaseDir, "0149ff87f4a5");
    writeObservabilityDir(aoBaseDir, "03706227e15e");
    writeObservabilityDir(aoBaseDir, "fea10426c4ba");

    const inv = await inventoryV3({ aoBaseDir, skipTmux: true });

    expect(inv.observability.rootLevelDirCount).toBe(3);
    expect(inv.observability.bytes).toBeGreaterThan(0);
    expect(inv.observability.oldestModifiedAt).toBeTypeOf("string");
  });

  it("detects bare hash dirs and .migrated dirs", async () => {
    mkdirSync(join(aoBaseDir, "111111111114"), { recursive: true });
    mkdirSync(join(aoBaseDir, "111111111114.migrated"), { recursive: true });

    const inv = await inventoryV3({ aoBaseDir, skipTmux: true });

    expect(inv.bareHashDirs).toEqual(["111111111114"]);
    expect(inv.migratedDirs).toEqual(["111111111114.migrated"]);
  });

  it("flags numbered orchestrators", async () => {
    const projectId = "agent-orchestrator_a1b2c3d4e5";
    mkdirSync(join(projectsDir, projectId, "sessions"), { recursive: true });
    writeSessionJson(projectsDir, projectId, {
      sessionId: "ao-orchestrator-1",
      kind: "orchestrator",
    });
    writeSessionJson(projectsDir, projectId, {
      sessionId: "ao-orchestrator-2",
      kind: "orchestrator",
    });

    const configPath = writeGlobalConfig(aoBaseDir, {
      [projectId]: { path: "/Users/x/repo", sessionPrefix: "ao" },
    });

    const inv = await inventoryV3({
      aoBaseDir,
      globalConfigPath: configPath,
      skipTmux: true,
    });

    const project = inv.projects[0];
    expect(project.orchestratorVariants).toContain("ao-orchestrator-1");
    expect(project.orchestratorVariants).toContain("ao-orchestrator-2");
    expect(project.issues.filter((i) => i.kind === "numbered-orchestrator").length).toBe(2);
  });

  it("flags doubled-prefix tmux name in metadata", async () => {
    const projectId = "agent-orchestrator_a1b2c3d4e5";
    writeSessionJson(projectsDir, projectId, {
      sessionId: "ao-orchestrator",
      tmuxName: "ao-ao-orchestrator", // doubled
      kind: "orchestrator",
    });

    const configPath = writeGlobalConfig(aoBaseDir, {
      [projectId]: { path: "/Users/x/repo", sessionPrefix: "ao" },
    });

    const inv = await inventoryV3({
      aoBaseDir,
      globalConfigPath: configPath,
      skipTmux: true,
    });

    const project = inv.projects[0];
    expect(project.legacyTmuxNamesInMetadata).toBe(1);
    expect(project.issues.some((i) => i.kind === "doubled-prefix-tmux")).toBe(true);
  });

  it("flags storageKey-prefixed tmux name in orchestrator.json", async () => {
    const projectId = "agent-orchestrator";
    writeOrchestratorJson(projectsDir, projectId, {
      sessionId: "ao-orchestrator",
      tmuxName: "66c66786e971-agent-orchestrator-ao-orchestrator-8",
      workspacePath: "/Users/x/.worktrees/agent-orchestrator/ao-orchestrator-8",
    });

    const configPath = writeGlobalConfig(aoBaseDir, {
      [projectId]: { path: "/Users/x/repo", sessionPrefix: "ao", storageKey: "66c66786e971" },
    });

    const inv = await inventoryV3({
      aoBaseDir,
      globalConfigPath: configPath,
      skipTmux: true,
    });

    const project = inv.projects[0];
    expect(project.liveOrchestratorTmuxName).toBe(
      "66c66786e971-agent-orchestrator-ao-orchestrator-8",
    );
    expect(project.issues.some((i) => i.kind === "legacy-tmux-in-metadata")).toBe(true);
    expect(project.issues.some((i) => i.kind === "legacy-workspace-path")).toBe(true);
  });

  it("flags stranded worktrees in legacy ~/.worktrees/", async () => {
    const projectId = "agent-orchestrator_a1b2c3d4e5";
    mkdirSync(join(projectsDir, projectId, "sessions"), { recursive: true });

    const configPath = writeGlobalConfig(aoBaseDir, {
      [projectId]: { path: "/Users/x/repo", sessionPrefix: "ao" },
    });

    // Set up legacy worktree tree
    const legacyRoot = createTempDir();
    mkdirSync(join(legacyRoot, "agent-orchestrator", "ao-101"), { recursive: true });
    mkdirSync(join(legacyRoot, "agent-orchestrator", "ao-102"), { recursive: true });

    const inv = await inventoryV3({
      aoBaseDir,
      globalConfigPath: configPath,
      legacyWorktreeRoot: legacyRoot,
      skipTmux: true,
    });

    expect(inv.strandedWorktrees).toHaveLength(2);
    expect(inv.strandedWorktrees[0].candidateProjectId).toBe(projectId);
    expect(inv.strandedWorktrees[0].candidateSessionId).toMatch(/^ao-10\d$/);

    rmSync(legacyRoot, { recursive: true, force: true });
  });

  it("flags global config storageKey fields", async () => {
    mkdirSync(join(projectsDir, "p1"), { recursive: true });
    mkdirSync(join(projectsDir, "p2_a1b2c3d4e5"), { recursive: true });

    const configPath = writeGlobalConfig(aoBaseDir, {
      p1: { path: "/x", sessionPrefix: "p1", storageKey: "aaaaaaaaaaaa" },
      p2_a1b2c3d4e5: { path: "/y", sessionPrefix: "p2", storageKey: "bbbbbbbbbbbb" },
    });

    const inv = await inventoryV3({
      aoBaseDir,
      globalConfigPath: configPath,
      skipTmux: true,
    });

    const storageKeyIssues = inv.globalConfigIssues.filter(
      (i) => i.kind === "storageKey-field-present",
    );
    expect(storageKeyIssues).toHaveLength(2);
  });

  it("flags registry entries that have no on-disk project dir", async () => {
    const configPath = writeGlobalConfig(aoBaseDir, {
      "missing-on-disk": { path: "/x", sessionPrefix: "m" },
    });

    const inv = await inventoryV3({
      aoBaseDir,
      globalConfigPath: configPath,
      skipTmux: true,
    });

    const stranded = inv.globalConfigIssues.filter(
      (i) => i.kind === "stranded-legacy-hash-dir",
    );
    expect(stranded).toHaveLength(1);
    expect(stranded[0].ref).toBe("missing-on-disk");
  });
});

// ---------------------------------------------------------------------------
// planV3
// ---------------------------------------------------------------------------

describe("planV3", () => {
  let aoBaseDir: string;
  let projectsDir: string;

  beforeEach(() => {
    aoBaseDir = createTempDir();
    projectsDir = join(aoBaseDir, "projects");
    mkdirSync(projectsDir, { recursive: true });
  });

  afterEach(() => {
    rmSync(aoBaseDir, { recursive: true, force: true });
  });

  it("returns minimal plan for clean V2-only disk", async () => {
    mkdirSync(join(projectsDir, "p1_a1b2c3d4e5", "sessions"), { recursive: true });

    const configPath = writeGlobalConfig(aoBaseDir, {
      p1_a1b2c3d4e5: { path: "/x", sessionPrefix: "p1" },
    });

    const inv = await inventoryV3({
      aoBaseDir,
      globalConfigPath: configPath,
      skipTmux: true,
    });
    const plan = planV3(inv, "0.6.0");

    expect(plan.totals.projectsToRekey).toBe(0);
    expect(plan.totals.sessionsToRewrite).toBe(0);
    expect(plan.totals.tmuxRenames).toBe(0);
    expect(plan.totals.worktreeAdoptions).toBe(0);
    expect(plan.totals.observabilityDirsToCollapse).toBe(0);

    // Always includes identity.json + counter steps
    expect(plan.steps.find((s) => s.id === "write-identity-json")).toBeDefined();
    expect(plan.steps.find((s) => s.id === "reconcile-counter")).toBeDefined();
    expect(plan.steps.find((s) => s.id === "dead-export-manifest")).toBeDefined();

    // Should NOT include conditional steps when there's nothing to do
    expect(plan.steps.find((s) => s.id === "rekey-v1-entries")).toBeUndefined();
    expect(plan.steps.find((s) => s.id === "rename-tmux-sessions")).toBeUndefined();
    expect(plan.steps.find((s) => s.id === "collapse-observability")).toBeUndefined();
  });

  it("emits rekey + same-repo merge steps for the user's actual disk shape", async () => {
    // Simulate the user's real situation: V1 bare 'agent-orchestrator' + V2 hashed sibling
    mkdirSync(join(projectsDir, "agent-orchestrator", "sessions"), { recursive: true });
    mkdirSync(join(projectsDir, "agent-orchestrator_168566536d", "sessions"), {
      recursive: true,
    });

    const configPath = writeGlobalConfig(aoBaseDir, {
      "agent-orchestrator": {
        path: "/Users/x/clones/clone-1/agent-orchestrator",
        sessionPrefix: "ao",
        storageKey: "7dc54da05c9e",
        repo: {
          owner: "ComposioHQ",
          name: "agent-orchestrator",
          platform: "github",
          originUrl: "https://github.com/composiohq/agent-orchestrator",
        },
      },
      "agent-orchestrator_168566536d": {
        path: "/Users/x/clones/clone-2/agent-orchestrator",
        sessionPrefix: "ao2",
        repo: {
          owner: "ComposioHQ",
          name: "agent-orchestrator",
          platform: "github",
          originUrl: "https://github.com/composiohq/agent-orchestrator",
        },
      },
    });

    const inv = await inventoryV3({
      aoBaseDir,
      globalConfigPath: configPath,
      skipTmux: true,
    });
    const plan = planV3(inv, "0.6.0");

    // V1 entry needs re-key
    expect(plan.totals.projectsToRekey).toBe(1);
    const rekey = plan.steps.find((s) => s.id === "rekey-v1-entries");
    expect(rekey?.count).toBe(1);
    expect(rekey?.details[0]).toMatch(/^agent-orchestrator → agent-orchestrator_[0-9a-f]{10}$/);

    // Same-repo merge surfaced
    expect(plan.steps.find((s) => s.id === "same-repo-merge")).toBeDefined();
    expect(plan.warnings.some((w) => w.includes("same-repo duplicate"))).toBe(true);

    // storageKey strip step
    expect(plan.totals.storageKeyFieldsToStrip).toBe(1);
    expect(plan.steps.find((s) => s.id === "strip-storage-key")).toBeDefined();
  });

  it("includes orchestrator-normalize step when numbered orchestrators present", async () => {
    const projectId = "agent-orchestrator_a1b2c3d4e5";
    writeSessionJson(projectsDir, projectId, {
      sessionId: "ao-orchestrator-1",
      kind: "orchestrator",
    });
    writeSessionJson(projectsDir, projectId, {
      sessionId: "ao-orchestrator-2",
      kind: "orchestrator",
    });
    writeSessionJson(projectsDir, projectId, {
      sessionId: "ao-orchestrator-3",
      kind: "orchestrator",
    });

    const configPath = writeGlobalConfig(aoBaseDir, {
      [projectId]: { path: "/x", sessionPrefix: "ao" },
    });

    const inv = await inventoryV3({
      aoBaseDir,
      globalConfigPath: configPath,
      skipTmux: true,
    });
    const plan = planV3(inv, "0.6.0");

    expect(plan.totals.orchestratorsToNormalize).toBe(3);
    expect(plan.steps.find((s) => s.id === "normalize-orchestrators")).toBeDefined();
  });

  it("includes observability-collapse step when leak present", async () => {
    writeObservabilityDir(aoBaseDir, "0149ff87f4a5");
    writeObservabilityDir(aoBaseDir, "03706227e15e");

    const inv = await inventoryV3({ aoBaseDir, skipTmux: true });
    const plan = planV3(inv, "0.6.0");

    expect(plan.totals.observabilityDirsToCollapse).toBe(2);
    const step = plan.steps.find((s) => s.id === "collapse-observability");
    expect(step).toBeDefined();
    expect(step?.count).toBe(2);
  });

  it("includes adopt-worktrees step when stranded worktrees present", async () => {
    const projectId = "agent-orchestrator_a1b2c3d4e5";
    mkdirSync(join(projectsDir, projectId, "sessions"), { recursive: true });

    const configPath = writeGlobalConfig(aoBaseDir, {
      [projectId]: { path: "/x", sessionPrefix: "ao" },
    });

    const legacyRoot = createTempDir();
    mkdirSync(join(legacyRoot, "agent-orchestrator", "ao-101"), { recursive: true });

    const inv = await inventoryV3({
      aoBaseDir,
      globalConfigPath: configPath,
      legacyWorktreeRoot: legacyRoot,
      skipTmux: true,
    });
    const plan = planV3(inv, "0.6.0");

    expect(plan.totals.worktreeAdoptions).toBe(1);
    expect(plan.steps.find((s) => s.id === "adopt-stranded-worktrees")).toBeDefined();

    rmSync(legacyRoot, { recursive: true, force: true });
  });

  it("schemaVersion + aoVersion present on the plan", async () => {
    const inv = await inventoryV3({ aoBaseDir, skipTmux: true });
    const plan = planV3(inv, "0.6.0");

    expect(plan.schemaVersion).toBe(3);
    expect(plan.aoVersion).toBe("0.6.0");
    expect(plan.generatedAt).toMatch(/^\d{4}-\d{2}-\d{2}T/);
    expect(plan.inventory.schemaVersion).toBe(3);
  });
});
