/**
 * Command executor — shell-based pipeline stages.
 *
 * A `command` stage is a script. The engine spawns it as a child process,
 * waits for it to exit, and parses its stdout as findings. Stages are NOT
 * talk-to-able (per locked decision 10): there is no AO session, no
 * dashboard chat, no terminal attach. Use the agent executor when you need
 * an interactive collaborator.
 *
 * Contract:
 *   - The command is invoked via `spawn(command, args, { cwd, env })`.
 *   - `cwd` is resolved against the run's workspace root (defaults to the root
 *     itself when the stage doesn't set one).
 *   - The command must exit `0` to be considered successful. Non-zero exit
 *     codes — and unexpected I/O errors — surface as `StageOutcome.failed`.
 *   - The stage's stdout is parsed as JSONL ArtifactInput records, the same
 *     format as the agent executor's findings file. Whitespace-only stdout
 *     yields zero artifacts; an empty stdout is treated identically.
 *   - The stage's stderr is captured into the error message on failure but
 *     never parsed as findings.
 *
 * Fork-PR safety: if the run's triggering PR is from a fork (the engine
 * threads `isFromFork: true` into the start input), the executor refuses to
 * run unless the stage opts in with `Stage.allowFork: true`. The refusal is
 * logged via a `command.refused_fork` observation effect (the engine relays
 * it to the standard observation log) so operators can see why a stage was
 * skipped.
 */

import { spawn } from "node:child_process";
import { join } from "node:path";

import { isWindows, killProcessTree } from "../../platform.js";
import {
  type ArtifactInput,
  type RunId,
  type Stage,
  type StageRunId,
} from "../types.js";
import { coerceArtifactInput, parseFindingsJsonl } from "./findings-parser.js";

/**
 * Inputs the engine passes when starting a command stage. Mirrors the agent
 * executor's StartStageInput but adds `isFromFork` — the PR fork bit is the
 * only piece of SCM state the command executor needs.
 */
export interface CommandStartInput {
  pipelineName: string;
  runId: RunId;
  stageRunId: StageRunId;
  stage: Stage;
  /**
   * Root the command runs in. Stages that set `executor.cwd` resolve against
   * this root. When unset, the executor uses `process.cwd()` so unit tests
   * can call the executor without threading a workspace through.
   */
  workspaceRoot?: string;
  /**
   * True when the triggering PR is from a fork. The executor refuses to run
   * unless the stage sets `allowFork: true`. Defaults to `false` for
   * non-PR runs (manual triggers, internal pipelines).
   */
  isFromFork?: boolean;
  /** Loop counter, surfaced as `AO_LOOP_ROUND` in the child env. */
  loopRound?: number;
}

export type CommandOutcome =
  | { status: "completed"; artifacts: ArtifactInput[] }
  | { status: "failed"; errorMessage: string; refused?: boolean };

export interface CommandStageExecutor {
  run(input: CommandStartInput): Promise<CommandOutcome>;
}

/**
 * Default millisecond cap on a single command stage. Overridable per-stage
 * via `Stage.timeoutMs`, and per-engine via `CommandExecutorDeps.defaultTimeoutMs`.
 */
export const DEFAULT_COMMAND_TIMEOUT_MS = 10 * 60 * 1000;
/** Default cap on stdout bytes; protects the engine from misbehaving scripts. */
export const DEFAULT_COMMAND_STDOUT_CAP_BYTES = 4 * 1024 * 1024;
/**
 * Default cap on stderr bytes. Stderr is only ever surfaced inside an error
 * message, so 64 KB is plenty for diagnosis without filling the conversation.
 */
export const DEFAULT_COMMAND_STDERR_CAP_BYTES = 64 * 1024;
/**
 * Grace period between SIGTERM and SIGKILL when a stage times out. Long
 * enough for a well-behaved child to flush stdout/stderr, short enough that
 * the pipeline still recovers in bounded time.
 */
export const COMMAND_KILL_GRACE_MS = 2_000;

/** Format the fork-refusal error message — exposed so engine logs match. */
export function formatForkRefusalMessage(stageName: string): string {
  return (
    `command stage "${stageName}" refused to run on a fork PR — ` +
    `set stage.allowFork=true to opt in (only safe for trusted scripts).`
  );
}

export interface CommandExecutorDeps {
  /**
   * Logger hook for refusals. Engine wires this to the observation effect
   * stream so the refusal appears in pipeline logs. Default: no-op.
   */
  onRefuse?(stage: Stage, message: string): void;
  /** Override clock for tests. Used for deadline math; never read inside the run loop itself. */
  now?(): number;
  /**
   * Engine-wide fallback timeout for command stages that don't set
   * `Stage.timeoutMs`. Defaults to {@link DEFAULT_COMMAND_TIMEOUT_MS}.
   */
  defaultTimeoutMs?: number;
  /** Process spawner — defaults to node:child_process.spawn. Override for tests. */
  spawnFn?: typeof spawn;
}

export function createCommandExecutor(deps: CommandExecutorDeps = {}): CommandStageExecutor {
  const onRefuse = deps.onRefuse ?? (() => undefined);
  const spawnFn = deps.spawnFn ?? spawn;
  const now = deps.now ?? Date.now;
  const defaultTimeoutMs = deps.defaultTimeoutMs ?? DEFAULT_COMMAND_TIMEOUT_MS;

  return {
    run(input: CommandStartInput): Promise<CommandOutcome> {
      const stage = input.stage;
      if (stage.executor.kind !== "command") {
        return Promise.resolve({
          status: "failed",
          errorMessage: `command executor cannot run stage "${stage.name}" with executor.kind=${stage.executor.kind}`,
        });
      }

      if (input.isFromFork && stage.allowFork !== true) {
        const message = formatForkRefusalMessage(stage.name);
        onRefuse(stage, message);
        return Promise.resolve({ status: "failed", errorMessage: message, refused: true });
      }

      const executor = stage.executor;
      const workspaceRoot = input.workspaceRoot ?? process.cwd();
      const cwd = executor.cwd ? join(workspaceRoot, executor.cwd) : workspaceRoot;
      const env = buildChildEnv(input, executor.env);
      const stdoutCap = DEFAULT_COMMAND_STDOUT_CAP_BYTES;
      const timeoutMs = stage.timeoutMs ?? defaultTimeoutMs;
      const startedAt = now();

      return new Promise<CommandOutcome>((resolve) => {
        let child;
        try {
          child = spawnFn(executor.command, executor.args ?? [], {
            cwd,
            env,
            stdio: ["ignore", "pipe", "pipe"],
            // Windows installs node-based CLIs as `.cmd` shims that `spawn`
            // can't resolve without going through cmd.exe. See
            // docs/CROSS_PLATFORM.md.
            shell: isWindows(),
            // Detach the child into its own process group on Unix so a
            // timeout kill can reach grandchildren (e.g. `sh -c 'sleep 30'`
            // — without a group kill, sleep inherits the stdio pipes and
            // `close` never fires). No effect on Windows; taskkill /T
            // walks the tree by PID.
            detached: !isWindows(),
          });
        } catch (err) {
          resolve({
            status: "failed",
            errorMessage: `failed to spawn command "${executor.command}": ${
              err instanceof Error ? err.message : String(err)
            }`,
          });
          return;
        }

        const stdoutChunks: Buffer[] = [];
        const stderrChunks: Buffer[] = [];
        let stdoutBytes = 0;
        let stderrBytes = 0;
        let truncated = false;
        let stderrTruncated = false;
        let settled = false;
        let timedOut = false;
        let killTimer: NodeJS.Timeout | null = null;
        let forceTimer: NodeJS.Timeout | null = null;

        const clearTimers = () => {
          if (killTimer) clearTimeout(killTimer);
          if (forceTimer) clearTimeout(forceTimer);
          killTimer = null;
          forceTimer = null;
        };

        const settle = (outcome: CommandOutcome) => {
          if (settled) return;
          settled = true;
          clearTimers();
          resolve(outcome);
        };

        if (timeoutMs > 0 && Number.isFinite(timeoutMs)) {
          killTimer = setTimeout(() => {
            if (settled) return;
            timedOut = true;
            // Best-effort graceful shutdown of the whole process tree,
            // escalating to SIGKILL after a short grace window so a wedged
            // child (or shell grandchild) can't hold the pipeline. Fire and
            // forget — `close` resolves the outer promise.
            if (child.pid !== undefined) {
              void killProcessTree(child.pid, "SIGTERM");
            }
            forceTimer = setTimeout(() => {
              if (child.pid !== undefined) {
                void killProcessTree(child.pid, "SIGKILL");
              }
            }, COMMAND_KILL_GRACE_MS);
          }, timeoutMs);
        }

        child.stdout?.on("data", (chunk: Buffer) => {
          stdoutBytes += chunk.length;
          if (stdoutBytes <= stdoutCap) {
            stdoutChunks.push(chunk);
          } else if (!truncated) {
            truncated = true;
            // Keep everything up to the cap; drop overflow rather than OOM.
            const overflow = stdoutBytes - stdoutCap;
            if (chunk.length > overflow) {
              stdoutChunks.push(chunk.subarray(0, chunk.length - overflow));
            }
          }
        });
        child.stderr?.on("data", (chunk: Buffer) => {
          stderrBytes += chunk.length;
          if (stderrBytes <= DEFAULT_COMMAND_STDERR_CAP_BYTES) {
            stderrChunks.push(chunk);
          } else if (!stderrTruncated) {
            stderrTruncated = true;
            // Keep everything up to the cap; drop overflow rather than OOM.
            const overflow = stderrBytes - DEFAULT_COMMAND_STDERR_CAP_BYTES;
            if (chunk.length > overflow) {
              stderrChunks.push(chunk.subarray(0, chunk.length - overflow));
            }
          }
        });
        child.on("error", (err) => {
          settle({
            status: "failed",
            errorMessage: `command "${executor.command}" failed: ${err.message}`,
          });
        });
        child.on("close", (code, signal) => {
          const stderrRaw = Buffer.concat(stderrChunks).toString("utf-8").trim();
          const stderr = stderrTruncated ? `${stderrRaw}\n[stderr truncated]` : stderrRaw;
          if (timedOut) {
            const elapsed = now() - startedAt;
            settle({
              status: "failed",
              errorMessage: `command "${executor.command}" timed out after ${timeoutMs}ms (ran for ${elapsed}ms)${stderr ? `: ${stderr}` : ""}`,
            });
            return;
          }
          if (truncated) {
            settle({
              status: "failed",
              errorMessage: `command "${executor.command}" stdout exceeded ${stdoutCap} bytes`,
            });
            return;
          }
          if (signal) {
            settle({
              status: "failed",
              errorMessage: `command "${executor.command}" terminated by signal ${signal}${stderr ? `: ${stderr}` : ""}`,
            });
            return;
          }
          if (code !== 0) {
            settle({
              status: "failed",
              errorMessage: `command "${executor.command}" exited ${code}${stderr ? `: ${stderr}` : ""}`,
            });
            return;
          }
          const stdout = Buffer.concat(stdoutChunks).toString("utf-8");
          let artifacts: ArtifactInput[];
          try {
            artifacts = parseStdoutFindings(stdout);
          } catch (err) {
            settle({
              status: "failed",
              errorMessage: `command "${executor.command}" produced unparseable findings: ${
                err instanceof Error ? err.message : String(err)
              }`,
            });
            return;
          }
          settle({ status: "completed", artifacts });
        });
      });
    },
  };
}

function buildChildEnv(
  input: CommandStartInput,
  overrides: Record<string, string> | undefined,
): NodeJS.ProcessEnv {
  const base: NodeJS.ProcessEnv = { ...process.env };
  base["AO_PIPELINE_NAME"] = input.pipelineName;
  base["AO_PIPELINE_RUN_ID"] = String(input.runId);
  base["AO_PIPELINE_STAGE_NAME"] = input.stage.name;
  base["AO_PIPELINE_STAGE_RUN_ID"] = String(input.stageRunId);
  if (input.loopRound !== undefined) {
    base["AO_PIPELINE_LOOP_ROUND"] = String(input.loopRound);
  }
  if (overrides) {
    for (const [k, v] of Object.entries(overrides)) base[k] = v;
  }
  return base;
}

/**
 * Parse stdout into ArtifactInput records.
 *
 * Stdout may be either:
 *   - JSONL (one JSON object per line) — the same shape produced by agent
 *     stages' findings file. Recommended for streaming output.
 *   - A single JSON array of ArtifactInput records.
 *   - Empty / whitespace-only — yields zero artifacts.
 *
 * Anything else (a bare JSON object, partial line, comma-separated values)
 * throws — operators get a precise line number rather than silent data loss.
 */
function parseStdoutFindings(stdout: string): ArtifactInput[] {
  const trimmed = stdout.trim();
  if (!trimmed) return [];

  if (trimmed.startsWith("[")) {
    let parsed: unknown;
    try {
      parsed = JSON.parse(trimmed);
    } catch (err) {
      throw new Error(
        `stdout JSON array failed to parse: ${err instanceof Error ? err.message : String(err)}`,
        { cause: err },
      );
    }
    if (!Array.isArray(parsed)) {
      throw new Error("stdout JSON did not produce an array");
    }
    return parsed.map((entry, idx) => coerceArtifactInput(entry, idx + 1));
  }

  return parseFindingsJsonl(stdout);
}
