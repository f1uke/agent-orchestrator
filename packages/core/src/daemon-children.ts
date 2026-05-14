import {
  type ChildProcess,
  type SpawnOptions,
  execFile as execFileCb,
  spawn,
} from "node:child_process";
import {
  existsSync,
  mkdirSync,
  readFileSync,
  rmSync,
  rmdirSync,
  statSync,
  unlinkSync,
} from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import { setTimeout as sleep } from "node:timers/promises";
import { promisify } from "node:util";
import { atomicWriteFileSync } from "./atomic-write.js";
import { isWindows, killProcessTree } from "./platform.js";

const execFileAsync = promisify(execFileCb);
const DEFAULT_GRACE_MS = 5_000;
const LOCK_STALE_MS = 10_000;

export interface DaemonChildEntry {
  pid: number;
  role: string;
  parentPid: number;
  startedAt: string;
  command?: string;
}

export interface DaemonChildSweepResult {
  attempted: number;
  terminated: number;
  forceKilled: number;
  failed: number;
}

export interface AoOrphanProcess {
  pid: number;
  ppid: number;
  command: string;
  role: string;
}

function getRegistryFile(): string {
  return join(homedir(), ".agent-orchestrator", "daemon-children.json");
}

function getLockDir(): string {
  return join(homedir(), ".agent-orchestrator", "daemon-children.lock");
}

function ensureStateDir(): void {
  mkdirSync(join(homedir(), ".agent-orchestrator"), { recursive: true });
}

function sleepSync(ms: number): void {
  const buffer = new SharedArrayBuffer(4);
  const view = new Int32Array(buffer);
  Atomics.wait(view, 0, 0, ms);
}

function acquireRegistryLock(): () => void {
  ensureStateDir();
  const lockDir = getLockDir();
  const deadline = Date.now() + 5_000;
  while (true) {
    try {
      mkdirSync(lockDir);
      return () => {
        try {
          rmdirSync(lockDir);
        } catch {
          // Best effort.
        }
      };
    } catch (err: unknown) {
      const code = (err as NodeJS.ErrnoException).code;
      if (code !== "EEXIST") throw err;
      try {
        const ageMs = Date.now() - statSync(lockDir).mtimeMs;
        if (ageMs > LOCK_STALE_MS) {
          rmSync(lockDir, { recursive: true, force: true });
          continue;
        }
      } catch {
        // Retry lock acquisition if the lock disappeared between calls.
      }
      if (Date.now() > deadline) {
        throw new Error(`Could not acquire daemon child registry lock (${lockDir})`, {
          cause: err,
        });
      }
      sleepSync(25);
    }
  }
}

function isDaemonChildEntry(value: unknown): value is DaemonChildEntry {
  if (typeof value !== "object" || value === null) return false;
  const entry = value as Partial<DaemonChildEntry>;
  return (
    typeof entry.pid === "number" &&
    entry.pid > 0 &&
    typeof entry.role === "string" &&
    typeof entry.parentPid === "number" &&
    typeof entry.startedAt === "string" &&
    (entry.command === undefined || typeof entry.command === "string")
  );
}

function readRawDaemonChildren(): DaemonChildEntry[] {
  const file = getRegistryFile();
  if (!existsSync(file)) return [];
  try {
    const parsed = JSON.parse(readFileSync(file, "utf-8")) as unknown;
    if (!Array.isArray(parsed)) return [];
    return parsed.filter(isDaemonChildEntry);
  } catch {
    return [];
  }
}

function writeRawDaemonChildren(entries: DaemonChildEntry[]): void {
  const file = getRegistryFile();
  if (entries.length === 0) {
    try {
      unlinkSync(file);
    } catch {
      // File may not exist.
    }
    return;
  }
  ensureStateDir();
  atomicWriteFileSync(file, JSON.stringify(entries, null, 2));
}

function isProcessAlive(pid: number): boolean {
  if (pid <= 0) return false;
  try {
    process.kill(pid, 0);
    return true;
  } catch (err: unknown) {
    return (err as { code?: string }).code === "EPERM";
  }
}

async function waitForProcessExit(pid: number, timeoutMs: number): Promise<boolean> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (!isProcessAlive(pid)) return true;
    await sleep(50);
  }
  return !isProcessAlive(pid);
}

export function registerDaemonChild(entry: Omit<DaemonChildEntry, "startedAt">): void {
  if (entry.pid <= 0) return;
  const release = acquireRegistryLock();
  try {
    const next = readRawDaemonChildren().filter((existing) => existing.pid !== entry.pid);
    next.push({ ...entry, startedAt: new Date().toISOString() });
    writeRawDaemonChildren(next);
  } finally {
    release();
  }
}

export function unregisterDaemonChild(pid: number): void {
  const release = acquireRegistryLock();
  try {
    const before = readRawDaemonChildren();
    const next = before.filter((entry) => entry.pid !== pid);
    if (next.length === before.length) return;
    writeRawDaemonChildren(next);
  } finally {
    release();
  }
}

export function getDaemonChildren(): DaemonChildEntry[] {
  const release = acquireRegistryLock();
  try {
    const all = readRawDaemonChildren();
    const live = all.filter((entry) => isProcessAlive(entry.pid));
    if (live.length !== all.length) writeRawDaemonChildren(live);
    return live;
  } finally {
    release();
  }
}

export function clearDaemonChildrenRegistry(): void {
  const release = acquireRegistryLock();
  try {
    writeRawDaemonChildren([]);
  } finally {
    release();
  }
}

function pruneSweptDaemonChildren(sweptPids: Set<number>): void {
  const release = acquireRegistryLock();
  try {
    const next = readRawDaemonChildren().filter(
      (entry) => !sweptPids.has(entry.pid) || isProcessAlive(entry.pid),
    );
    writeRawDaemonChildren(next);
  } finally {
    release();
  }
}

const reapedChildren = new WeakSet<ChildProcess>();
const managedChildren = new Map<number, ChildProcess>();
let managedSignalHandlersInstalled = false;

function terminateManagedChildren(): void {
  for (const [pid, child] of managedChildren) {
    void killProcessTree(pid, "SIGTERM");
    try {
      child.kill("SIGTERM");
    } catch {
      // Already gone.
    }
  }
}

function installManagedSignalHandlers(): void {
  if (managedSignalHandlersInstalled || isWindows()) return;
  managedSignalHandlersInstalled = true;

  const forward = (signal: NodeJS.Signals): void => {
    terminateManagedChildren();

    // Installing a signal listener disables Node's default "exit on signal"
    // behaviour. If no application-level shutdown handler is present, preserve
    // that default after forwarding the signal to managed children.
    if (process.listenerCount(signal) <= 1) {
      const exitCode = signal === "SIGINT" ? 130 : 0;
      setTimeout(() => process.exit(exitCode), 50);
    }
  };

  process.on("SIGINT", forward);
  process.on("SIGTERM", forward);
  process.on("exit", terminateManagedChildren);
}

/**
 * Track a long-running daemon child in the pid registry and forward parent
 * shutdown to it. If the owning process has its own shutdown handler, that
 * handler remains responsible for exiting; otherwise the managed signal
 * handler preserves Node's default signal-exit behaviour after forwarding.
 */
export function registerChildReaper(child: ChildProcess, role: string, command?: string): void {
  if (reapedChildren.has(child)) return;
  reapedChildren.add(child);

  const pid = child.pid;
  if (!pid) return;

  registerDaemonChild({ pid, role, parentPid: process.pid, command });
  managedChildren.set(pid, child);
  installManagedSignalHandlers();

  const cleanup = (): void => {
    managedChildren.delete(pid);
    unregisterDaemonChild(pid);
  };

  child.once("exit", cleanup);
  child.once("error", cleanup);
}

/**
 * The required interface for long-running subprocesses owned by the AO daemon.
 * Callers get normal child_process.spawn behaviour, plus pid registry,
 * signal forwarding, process-group cleanup, and registry unregister on exit.
 */
export function spawnManagedDaemonChild(
  role: string,
  command: string,
  args: readonly string[],
  options: SpawnOptions = {},
): ChildProcess {
  const child = spawn(command, [...args], options);
  registerChildReaper(child, role, [command, ...args].join(" "));
  return child;
}

export async function sweepDaemonChildren(
  graceMs: number = DEFAULT_GRACE_MS,
): Promise<DaemonChildSweepResult> {
  const entries = getDaemonChildren();
  const result: DaemonChildSweepResult = {
    attempted: entries.length,
    terminated: 0,
    forceKilled: 0,
    failed: 0,
  };

  for (const entry of entries) {
    await killProcessTree(entry.pid, "SIGTERM");
  }

  for (const entry of entries) {
    if (await waitForProcessExit(entry.pid, graceMs)) {
      result.terminated++;
      continue;
    }

    await killProcessTree(entry.pid, "SIGKILL");
    if (await waitForProcessExit(entry.pid, 1_000)) {
      result.forceKilled++;
    } else {
      result.failed++;
    }
  }

  pruneSweptDaemonChildren(new Set(entries.map((entry) => entry.pid)));
  return result;
}

export function classifyAoOrphanCommand(command: string): string | null {
  if (/node\b.*@aoagents[/\\]ao-web(?:@[^/\\\s]+)?[/\\]dist-server[/\\].+/i.test(command)) {
    return "ao-web";
  }
  if (/node\b.*\bao\b.*\blifecycle-worker\b.+/i.test(command)) {
    return "lifecycle-worker";
  }
  if (/node\b.*ao-messaging[/\\].*[/\\]ao-msg-watch\.mjs\b/i.test(command)) {
    return "ao-msg-watch";
  }
  if (/node\b.*next-server/i.test(command)) {
    return "next-server";
  }
  return null;
}

export function detectAoOrphansFromPsOutput(psOutput: string): AoOrphanProcess[] {
  const orphans: AoOrphanProcess[] = [];
  for (const rawLine of psOutput.split(/\r?\n/)) {
    const line = rawLine.trim();
    if (!line) continue;

    const match = line.match(/^(\d+)\s+(\d+)\s+(.+)$/);
    if (!match) continue;

    const pid = Number(match[1]);
    const ppid = Number(match[2]);
    const command = match[3] ?? "";
    if (!Number.isFinite(pid) || !Number.isFinite(ppid) || ppid !== 1) continue;

    const role = classifyAoOrphanCommand(command);
    if (!role) continue;
    orphans.push({ pid, ppid, command, role });
  }
  return orphans;
}

export async function scanAoOrphans(): Promise<AoOrphanProcess[]> {
  if (isWindows()) return [];
  try {
    const { stdout } = await execFileAsync("ps", ["-axo", "pid,ppid,command"], {
      windowsHide: true,
      maxBuffer: 10 * 1024 * 1024,
    });
    return detectAoOrphansFromPsOutput(stdout);
  } catch {
    return [];
  }
}

export async function reapAoOrphans(
  orphans: AoOrphanProcess[],
  graceMs: number = DEFAULT_GRACE_MS,
): Promise<DaemonChildSweepResult> {
  const result: DaemonChildSweepResult = {
    attempted: orphans.length,
    terminated: 0,
    forceKilled: 0,
    failed: 0,
  };

  for (const orphan of orphans) {
    await killProcessTree(orphan.pid, "SIGTERM");
  }

  for (const orphan of orphans) {
    if (await waitForProcessExit(orphan.pid, graceMs)) {
      result.terminated++;
      continue;
    }
    await killProcessTree(orphan.pid, "SIGKILL");
    if (await waitForProcessExit(orphan.pid, 1_000)) {
      result.forceKilled++;
    } else {
      result.failed++;
    }
  }

  return result;
}

export function __getDaemonChildrenRegistryFile(): string {
  return getRegistryFile();
}
