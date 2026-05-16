/**
 * Tests for the command executor — shell-based pipeline stages.
 *
 * Subprocess-bearing tests use `/bin/sh -c` with POSIX shell syntax and are
 * skipped on Windows; the executor itself is cross-platform (Windows users
 * point `executor.command` at `pwsh.exe` or a `.cmd` shim — `spawn(shell:
 * true)` handles the latter). Guard / refusal / kind-mismatch tests are
 * platform-neutral and run everywhere.
 */

import { describe, expect, it, vi } from "vitest";

import { isWindows } from "../platform.js";
import {
  asRunId,
  asStageRunId,
  createCommandExecutor,
  DEFAULT_COMMAND_STDERR_CAP_BYTES,
  formatForkRefusalMessage,
  type CommandStartInput,
  type Stage,
} from "../pipeline/index.js";

const posixDescribe = isWindows() ? describe.skip : describe;

function makeCommandStage(overrides: Partial<Stage> = {}): Stage {
  return {
    name: "lint",
    trigger: { on: ["pr.opened"] },
    executor: { kind: "command", command: "echo", args: [] },
    task: {},
    ...overrides,
  };
}

function makeInput(overrides: Partial<CommandStartInput> = {}): CommandStartInput {
  return {
    pipelineName: "default",
    runId: asRunId("run-1"),
    stageRunId: asStageRunId("sr-1"),
    stage: makeCommandStage(),
    loopRound: 1,
    ...overrides,
  };
}

// Platform-neutral guards: never spawn a subprocess.
describe("command executor — guards (cross-platform)", () => {
  it("rejects non-command stages with a typed failure", async () => {
    const exec = createCommandExecutor();
    const outcome = await exec.run(
      makeInput({
        stage: makeCommandStage({
          executor: { kind: "agent", plugin: "claude-code", mode: "code" },
        }),
      }),
    );
    expect(outcome.status).toBe("failed");
    if (outcome.status !== "failed") throw new Error("unreachable");
    expect(outcome.errorMessage).toContain("command executor cannot run");
  });

  it("refuses to run a fork PR when stage.allowFork is unset", async () => {
    const onRefuse = vi.fn();
    const exec = createCommandExecutor({ onRefuse });
    // Fork refusal short-circuits BEFORE spawn — never executes the shell,
    // so the command string need not be cross-platform.
    const stage = makeCommandStage({
      executor: {
        kind: "command",
        command: "/bin/sh",
        args: ["-c", "echo should-not-run"],
      },
    });

    const outcome = await exec.run(makeInput({ stage, isFromFork: true }));

    expect(outcome.status).toBe("failed");
    if (outcome.status !== "failed") throw new Error("unreachable");
    expect(outcome.refused).toBe(true);
    expect(outcome.errorMessage).toBe(formatForkRefusalMessage(stage.name));
    expect(onRefuse).toHaveBeenCalledTimes(1);
    expect(onRefuse).toHaveBeenCalledWith(stage, formatForkRefusalMessage(stage.name));
  });

  it("refuses to run a fork PR when stage.allowFork is explicitly false", async () => {
    const exec = createCommandExecutor();
    const stage = makeCommandStage({
      allowFork: false,
      executor: { kind: "command", command: "/bin/sh", args: ["-c", "echo hi"] },
    });
    const outcome = await exec.run(makeInput({ stage, isFromFork: true }));
    expect(outcome.status).toBe("failed");
    if (outcome.status !== "failed") throw new Error("unreachable");
    expect(outcome.refused).toBe(true);
  });
});

// Subprocess-bearing tests use /bin/sh and POSIX shell syntax: skip on Windows.
posixDescribe("command executor — guards (POSIX subprocess)", () => {
  it("runs a fork PR when stage.allowFork is explicitly true", async () => {
    const exec = createCommandExecutor();
    const stage = makeCommandStage({
      allowFork: true,
      executor: { kind: "command", command: "/bin/sh", args: ["-c", "echo -n ''"] },
    });
    const outcome = await exec.run(makeInput({ stage, isFromFork: true }));
    expect(outcome).toEqual({ status: "completed", artifacts: [] });
  });

  it("runs non-fork PRs without checking allowFork", async () => {
    const exec = createCommandExecutor();
    const stage = makeCommandStage({
      executor: { kind: "command", command: "/bin/sh", args: ["-c", "true"] },
    });
    const outcome = await exec.run(makeInput({ stage, isFromFork: false }));
    expect(outcome.status).toBe("completed");
  });
});

posixDescribe("command executor — stdout findings", () => {
  it("parses JSONL stdout into ArtifactInput records", async () => {
    const exec = createCommandExecutor();
    const finding = {
      kind: "finding",
      filePath: "src/foo.ts",
      startLine: 1,
      endLine: 2,
      title: "t",
      description: "d",
      category: "general",
      severity: "info",
      confidence: 0.5,
    };
    const jsonArtifact = { kind: "json", data: { ok: true } };
    // Single-quoted in sh so JSON.stringify's double quotes survive. JSON
    // never emits literal single quotes so the embedding is unambiguous.
    const script = `echo '${JSON.stringify(finding)}'; echo '${JSON.stringify(jsonArtifact)}'`;

    const stage = makeCommandStage({
      executor: { kind: "command", command: "/bin/sh", args: ["-c", script] },
    });
    const outcome = await exec.run(makeInput({ stage }));

    expect(outcome.status).toBe("completed");
    if (outcome.status !== "completed") throw new Error("unreachable");
    expect(outcome.artifacts).toHaveLength(2);
    expect(outcome.artifacts[0]).toMatchObject({ kind: "finding", title: "t" });
    expect(outcome.artifacts[1]).toMatchObject({ kind: "json", data: { ok: true } });
  });

  it("parses a single JSON array stdout into ArtifactInput records", async () => {
    const exec = createCommandExecutor();
    const arr = [{ kind: "json", data: { a: 1 } }, { kind: "json", data: { b: 2 } }];
    const stage = makeCommandStage({
      executor: {
        kind: "command",
        command: "/bin/sh",
        args: ["-c", `printf '%s' ${JSON.stringify(JSON.stringify(arr))}`],
      },
    });

    const outcome = await exec.run(makeInput({ stage }));
    expect(outcome.status).toBe("completed");
    if (outcome.status !== "completed") throw new Error("unreachable");
    expect(outcome.artifacts).toEqual(arr);
  });

  it("treats empty stdout as zero artifacts", async () => {
    const exec = createCommandExecutor();
    const stage = makeCommandStage({
      executor: { kind: "command", command: "/bin/sh", args: ["-c", "true"] },
    });
    const outcome = await exec.run(makeInput({ stage }));
    expect(outcome).toEqual({ status: "completed", artifacts: [] });
  });

  it("fails on non-zero exit codes and surfaces stderr in the error", async () => {
    const exec = createCommandExecutor();
    const stage = makeCommandStage({
      executor: {
        kind: "command",
        command: "/bin/sh",
        args: ["-c", "echo boom >&2; exit 3"],
      },
    });
    const outcome = await exec.run(makeInput({ stage }));
    expect(outcome.status).toBe("failed");
    if (outcome.status !== "failed") throw new Error("unreachable");
    expect(outcome.errorMessage).toContain("exited 3");
    expect(outcome.errorMessage).toContain("boom");
  });

  it("fails when stdout is invalid JSON", async () => {
    const exec = createCommandExecutor();
    const stage = makeCommandStage({
      executor: {
        kind: "command",
        command: "/bin/sh",
        args: ["-c", "echo 'not json {{{'"],
      },
    });
    const outcome = await exec.run(makeInput({ stage }));
    expect(outcome.status).toBe("failed");
    if (outcome.status !== "failed") throw new Error("unreachable");
    expect(outcome.errorMessage).toContain("unparseable findings");
  });

  it("fails when a finding has confidence out of [0, 1]", async () => {
    const exec = createCommandExecutor();
    const bad = {
      kind: "finding",
      filePath: "x.ts",
      startLine: 1,
      endLine: 1,
      title: "t",
      description: "d",
      category: "c",
      severity: "info",
      confidence: 5,
    };
    const stage = makeCommandStage({
      executor: {
        kind: "command",
        command: "/bin/sh",
        args: ["-c", `printf '%s' ${JSON.stringify(JSON.stringify(bad))}`],
      },
    });
    const outcome = await exec.run(makeInput({ stage }));
    expect(outcome.status).toBe("failed");
    if (outcome.status !== "failed") throw new Error("unreachable");
    expect(outcome.errorMessage).toContain("confidence");
  });
});

posixDescribe("command executor — environment", () => {
  it("threads AO_PIPELINE_* env vars into the child process", async () => {
    const exec = createCommandExecutor();
    const stage = makeCommandStage({
      executor: {
        kind: "command",
        command: "/bin/sh",
        args: [
          "-c",
          'printf \'{"kind":"json","data":{"stage":"%s","run":"%s"}}\' "$AO_PIPELINE_STAGE_NAME" "$AO_PIPELINE_RUN_ID"',
        ],
      },
    });

    const outcome = await exec.run(
      makeInput({
        runId: asRunId("run-xyz"),
        stage,
      }),
    );

    expect(outcome.status).toBe("completed");
    if (outcome.status !== "completed") throw new Error("unreachable");
    expect(outcome.artifacts[0]).toMatchObject({
      kind: "json",
      data: { stage: "lint", run: "run-xyz" },
    });
  });

  it("lets stage.env overrides win over default env", async () => {
    const exec = createCommandExecutor();
    const stage = makeCommandStage({
      executor: {
        kind: "command",
        command: "/bin/sh",
        args: ["-c", 'printf \'{"kind":"json","data":{"v":"%s"}}\' "$MY_VAR"'],
        env: { MY_VAR: "from-stage" },
      },
    });
    const outcome = await exec.run(makeInput({ stage }));
    expect(outcome.status).toBe("completed");
    if (outcome.status !== "completed") throw new Error("unreachable");
    expect(outcome.artifacts[0]).toMatchObject({ kind: "json", data: { v: "from-stage" } });
  });
});

posixDescribe("command executor — stderr cap", () => {
  it("truncates stderr that exceeds DEFAULT_COMMAND_STDERR_CAP_BYTES and notes it in the error", async () => {
    const exec = createCommandExecutor();
    // Emit well over the 64 KB stderr cap then exit non-zero so stderr surfaces
    // in the error message. `yes y | head -c N` writes N bytes to stdout; we
    // redirect to stderr and add a newline-terminated final byte to ensure the
    // buffer flushes cleanly.
    const bytesOver = DEFAULT_COMMAND_STDERR_CAP_BYTES + 10_000;
    const stage = makeCommandStage({
      executor: {
        kind: "command",
        command: "/bin/sh",
        args: ["-c", `yes y | head -c ${bytesOver} >&2; exit 1`],
      },
    });

    const outcome = await exec.run(makeInput({ stage }));

    expect(outcome.status).toBe("failed");
    if (outcome.status !== "failed") throw new Error("unreachable");
    // The error message must contain the truncation marker.
    expect(outcome.errorMessage).toContain("[stderr truncated]");
    // The captured stderr in the error message must stay bounded — confirm it
    // is well under 2x the cap (the raw cap plus the marker suffix).
    expect(outcome.errorMessage.length).toBeLessThan(DEFAULT_COMMAND_STDERR_CAP_BYTES * 2);
  });
});

posixDescribe("command executor — timeout enforcement", () => {
  it("kills a stage that exceeds Stage.timeoutMs and reports the timeout", async () => {
    const exec = createCommandExecutor();
    const stage = makeCommandStage({
      timeoutMs: 100,
      executor: {
        kind: "command",
        command: "/bin/sh",
        args: ["-c", "sleep 30"],
      },
    });

    const outcome = await exec.run(makeInput({ stage }));

    expect(outcome.status).toBe("failed");
    if (outcome.status !== "failed") throw new Error("unreachable");
    expect(outcome.errorMessage).toContain("timed out after 100ms");
  });

  it("respects defaultTimeoutMs when Stage.timeoutMs is unset", async () => {
    const exec = createCommandExecutor({ defaultTimeoutMs: 100 });
    const stage = makeCommandStage({
      executor: {
        kind: "command",
        command: "/bin/sh",
        args: ["-c", "sleep 30"],
      },
    });

    const outcome = await exec.run(makeInput({ stage }));

    expect(outcome.status).toBe("failed");
    if (outcome.status !== "failed") throw new Error("unreachable");
    expect(outcome.errorMessage).toContain("timed out after 100ms");
  });

  it("does not time out a fast stage that completes before the deadline", async () => {
    const exec = createCommandExecutor({ defaultTimeoutMs: 30_000 });
    const stage = makeCommandStage({
      executor: { kind: "command", command: "/bin/sh", args: ["-c", "true"] },
    });
    const outcome = await exec.run(makeInput({ stage }));
    expect(outcome.status).toBe("completed");
  });
});
