/**
 * v1.1 DAG scheduler coverage. Verifies:
 *  - Cycle detection at config load (clear error naming the cycle)
 *  - Two independent stages run concurrently when maxConcurrentStages >= 2
 *  - A stage with unmet dependsOn does not start
 *  - A stage with an unsatisfied routes predicate is skipped, with cascade
 *  - Multiple pipelines can coexist within a single project config
 *
 * The reducer-event helpers (`fireTrigger`, `completeStage`) mirror the style
 * used by the existing pipeline reducer suite so tests read consistently.
 */

import { describe, expect, it } from "vitest";

import {
  ConfiguredPipelineSchema,
  PipelinesConfigSchema,
  asPipelineId,
  asRunId,
  asStageRunId,
  emptyEngineState,
  reduce,
  type EngineState,
  type Pipeline,
  type PipelineEvent,
  type RunId,
  type Stage,
  type StageRunId,
} from "../pipeline/index.js";

const NOW = 1_700_000_000_000;

function makeStage(name: string, overrides: Partial<Stage> = {}): Stage {
  return {
    name,
    trigger: { on: ["pr.opened"] },
    executor: { kind: "agent", plugin: "codex", mode: "review" },
    task: { prompt: `run ${name}` },
    ...overrides,
  };
}

function makePipeline(stages: Stage[], maxConcurrentStages = 1): Pipeline {
  return {
    id: asPipelineId("pl-1"),
    name: "default",
    stages,
    maxConcurrentStages,
  };
}

function fireTrigger(pipeline: Pipeline, runId = asRunId("run-1")) {
  const stageRunIds: Record<string, StageRunId> = Object.fromEntries(
    pipeline.stages.map((s, i) => [s.name, asStageRunId(`sr-${s.name}-${i}`)]),
  );
  const event: PipelineEvent = {
    type: "TRIGGER_FIRED",
    now: NOW,
    trigger: "manual",
    sessionId: "ses-1",
    pipeline,
    headSha: "sha-aaa",
    runId,
    stageRunIds,
  };
  return reduce(emptyEngineState(), event);
}

function startStage(state: EngineState, runId: RunId, stageName: string, t = NOW + 1) {
  return reduce(state, { type: "STAGE_STARTED", now: t, runId, stageName });
}

function completeStage(state: EngineState, runId: RunId, stageName: string, t = NOW + 2) {
  return reduce(state, {
    type: "STAGE_COMPLETED",
    now: t,
    runId,
    stageName,
    artifacts: [],
  });
}

describe("pipeline DAG — cycle detection (config load)", () => {
  it("rejects a 2-cycle and names both stages in order", () => {
    const result = ConfiguredPipelineSchema.safeParse({
      stages: [
        { ...makeStage("a"), dependsOn: ["b"] },
        { ...makeStage("b"), dependsOn: ["a"] },
      ],
    });
    expect(result.success).toBe(false);
    if (result.success) return;
    const messages = result.error.issues.map((i) => i.message).join("\n");
    expect(messages).toContain("stage dependency cycle");
    expect(messages).toContain("a → b → a");
  });

  it("rejects a 3-cycle with a clear path", () => {
    const result = ConfiguredPipelineSchema.safeParse({
      stages: [
        { ...makeStage("a"), dependsOn: ["c"] },
        { ...makeStage("b"), dependsOn: ["a"] },
        { ...makeStage("c"), dependsOn: ["b"] },
      ],
    });
    expect(result.success).toBe(false);
    if (result.success) return;
    const messages = result.error.issues.map((i) => i.message).join("\n");
    expect(messages).toMatch(/stage dependency cycle.*a.*c|c.*b.*a/);
  });

  it("rejects routes-only cycles (would deadlock at runtime)", () => {
    // Without dependsOn edges in the cycle graph, a→b→a via routes alone
    // would leave both stages waiting on each other in `arePreconditionsTerminal`.
    const result = ConfiguredPipelineSchema.safeParse({
      stages: [
        {
          ...makeStage("a"),
          routes: { when: { kind: "allSucceeded", stages: ["b"] } },
        },
        {
          ...makeStage("b"),
          routes: { when: { kind: "allSucceeded", stages: ["a"] } },
        },
      ],
    });
    expect(result.success).toBe(false);
    if (result.success) return;
    const messages = result.error.issues.map((i) => i.message).join("\n");
    expect(messages).toContain("stage dependency cycle");
    expect(messages).toContain("a → b → a");
  });

  it("rejects mixed dependsOn + routes cycles", () => {
    const result = ConfiguredPipelineSchema.safeParse({
      stages: [
        { ...makeStage("a"), dependsOn: ["b"] },
        {
          ...makeStage("b"),
          routes: { when: { kind: "allSucceeded", stages: ["a"] } },
        },
      ],
    });
    expect(result.success).toBe(false);
    if (result.success) return;
    const messages = result.error.issues.map((i) => i.message).join("\n");
    expect(messages).toContain("stage dependency cycle");
  });

  it("rejects self-dependency without emitting a duplicate cycle error", () => {
    const result = ConfiguredPipelineSchema.safeParse({
      stages: [{ ...makeStage("a"), dependsOn: ["a"] }],
    });
    expect(result.success).toBe(false);
    if (result.success) return;
    const messages = result.error.issues.map((i) => i.message);
    expect(messages.some((m) => m.includes('"a" cannot depend on itself'))).toBe(true);
    // Trivial self-loops are owned by the explicit self-ref check; the cycle
    // detector must NOT emit an additional "stage dependency cycle: a → a".
    expect(messages.some((m) => m.includes("stage dependency cycle"))).toBe(false);
  });

  it("rejects routes self-reference (would deadlock at runtime)", () => {
    // A stage whose routes reference itself never sees its own state become
    // terminal, so `arePreconditionsTerminal` returns false forever.
    const result = ConfiguredPipelineSchema.safeParse({
      stages: [
        {
          ...makeStage("a"),
          routes: { when: { kind: "allSucceeded", stages: ["a"] } },
        },
      ],
    });
    expect(result.success).toBe(false);
    if (result.success) return;
    const messages = result.error.issues.map((i) => i.message).join("\n");
    expect(messages).toContain('"a" cannot route to itself');
  });

  it("rejects empty stages arrays in route predicates", () => {
    // Vacuous truth/falsity on empty stage lists is surprising — every
    // predicate kind must name at least one upstream stage.
    const allEmpty = ConfiguredPipelineSchema.safeParse({
      stages: [
        {
          ...makeStage("a"),
          routes: { when: { kind: "allSucceeded", stages: [] } },
        },
      ],
    });
    expect(allEmpty.success).toBe(false);

    const anyEmpty = ConfiguredPipelineSchema.safeParse({
      stages: [
        {
          ...makeStage("a"),
          routes: { when: { kind: "anyFailed", stages: [] } },
        },
      ],
    });
    expect(anyEmpty.success).toBe(false);
  });

  it("rejects unknown stage names in dependsOn", () => {
    const result = ConfiguredPipelineSchema.safeParse({
      stages: [makeStage("a"), { ...makeStage("b"), dependsOn: ["nonexistent"] }],
    });
    expect(result.success).toBe(false);
    if (result.success) return;
    const messages = result.error.issues.map((i) => i.message).join("\n");
    expect(messages).toContain('unknown stage "nonexistent"');
  });

  it("rejects unknown stage names in routes predicates", () => {
    const result = ConfiguredPipelineSchema.safeParse({
      stages: [
        makeStage("a"),
        {
          ...makeStage("b"),
          dependsOn: ["a"],
          routes: { when: { kind: "allSucceeded", stages: ["nope"] } },
        },
      ],
    });
    expect(result.success).toBe(false);
    if (result.success) return;
    const messages = result.error.issues.map((i) => i.message).join("\n");
    expect(messages).toContain('routes references unknown stage "nope"');
  });

  it("accepts a valid acyclic DAG with mixed dependsOn and routes", () => {
    const result = ConfiguredPipelineSchema.safeParse({
      stages: [
        makeStage("a"),
        { ...makeStage("b"), dependsOn: ["a"] },
        {
          ...makeStage("c"),
          dependsOn: ["a", "b"],
          routes: { when: { kind: "allSucceeded", stages: ["a", "b"] } },
        },
      ],
    });
    expect(result.success).toBe(true);
  });

  it("rejects duplicate stage names", () => {
    const result = ConfiguredPipelineSchema.safeParse({
      stages: [makeStage("a"), makeStage("a")],
    });
    expect(result.success).toBe(false);
    if (result.success) return;
    const messages = result.error.issues.map((i) => i.message).join("\n");
    expect(messages).toContain('Duplicate stage name "a"');
  });
});

describe("pipeline DAG — parallel scheduling", () => {
  it("starts two independent stages concurrently when maxConcurrentStages=2", () => {
    const pipeline = makePipeline([makeStage("a"), makeStage("b")], 2);
    const { effects } = fireTrigger(pipeline);
    const startEffects = effects.filter((e) => e.type === "START_STAGE");
    expect(startEffects).toHaveLength(2);
    const names = startEffects.map((e) => (e.type === "START_STAGE" ? e.stage.name : "")).sort();
    expect(names).toEqual(["a", "b"]);
  });

  it("respects declaration order when slots < eligible stages", () => {
    const pipeline = makePipeline([makeStage("a"), makeStage("b"), makeStage("c")], 2);
    const { effects } = fireTrigger(pipeline);
    const names = effects
      .filter((e) => e.type === "START_STAGE")
      .map((e) => (e.type === "START_STAGE" ? e.stage.name : ""))
      .sort();
    expect(names).toEqual(["a", "b"]);
  });

  it("a stage with unmet dependsOn does not start at trigger time", () => {
    const pipeline = makePipeline([makeStage("a"), { ...makeStage("b"), dependsOn: ["a"] }], 2);
    const { state, effects } = fireTrigger(pipeline);
    const starts = effects.filter((e) => e.type === "START_STAGE");
    expect(starts).toHaveLength(1);
    if (starts[0].type !== "START_STAGE") throw new Error();
    expect(starts[0].stage.name).toBe("a");
    expect(state.runs[asRunId("run-1")].stages.b.status).toBe("pending");
  });

  it("starts a dependent stage after its dependsOn succeeds", () => {
    const pipeline = makePipeline([makeStage("a"), { ...makeStage("b"), dependsOn: ["a"] }], 1);
    const triggered = fireTrigger(pipeline);
    const started = startStage(triggered.state, asRunId("run-1"), "a");
    const { state, effects } = completeStage(started.state, asRunId("run-1"), "a");
    expect(state.runs[asRunId("run-1")].stages.b.status).toBe("pending");
    const startB = effects.find((e) => e.type === "START_STAGE" && e.stage.name === "b");
    expect(startB).toBeDefined();
  });

  it("starts multiple downstream branches concurrently when both deps succeed", () => {
    // a → {b, c} (b and c both depend on a only, no inter-branch dependency)
    const pipeline = makePipeline(
      [
        makeStage("a"),
        { ...makeStage("b"), dependsOn: ["a"] },
        { ...makeStage("c"), dependsOn: ["a"] },
      ],
      2,
    );
    const triggered = fireTrigger(pipeline);
    const started = startStage(triggered.state, asRunId("run-1"), "a");
    const { effects } = completeStage(started.state, asRunId("run-1"), "a");
    const starts = effects
      .filter((e) => e.type === "START_STAGE")
      .map((e) => (e.type === "START_STAGE" ? e.stage.name : ""))
      .sort();
    expect(starts).toEqual(["b", "c"]);
  });

  it("does not exceed maxConcurrentStages when many branches unlock at once", () => {
    const pipeline = makePipeline(
      [
        makeStage("a"),
        { ...makeStage("b"), dependsOn: ["a"] },
        { ...makeStage("c"), dependsOn: ["a"] },
        { ...makeStage("d"), dependsOn: ["a"] },
      ],
      2,
    );
    const triggered = fireTrigger(pipeline);
    const started = startStage(triggered.state, asRunId("run-1"), "a");
    const { effects } = completeStage(started.state, asRunId("run-1"), "a");
    const starts = effects.filter((e) => e.type === "START_STAGE");
    expect(starts).toHaveLength(2);
  });
});

describe("pipeline DAG — routes predicate", () => {
  it("skips a stage whose allSucceeded routes references a non-succeeded upstream", () => {
    // This shape uses routes to express "only run b when a succeeded AND
    // some other parallel stage `c` succeeded". When c is skipped (because
    // its own routes are unsatisfied), b's routes also fail and b is skipped.
    const pipeline = makePipeline(
      [
        makeStage("a"),
        // c skips itself by requiring a stage that won't succeed.
        {
          ...makeStage("c"),
          dependsOn: ["a"],
          routes: { when: { kind: "anyFailed", stages: ["a"] } },
        },
        // b only runs when both a AND c succeeded.
        {
          ...makeStage("b"),
          dependsOn: ["a", "c"],
          routes: { when: { kind: "allSucceeded", stages: ["a", "c"] } },
        },
      ],
      2,
    );

    const triggered = fireTrigger(pipeline);
    const started = startStage(triggered.state, asRunId("run-1"), "a");
    const { state } = completeStage(started.state, asRunId("run-1"), "a");

    const run = state.runs[asRunId("run-1")];
    expect(run.stages.a.status).toBe("succeeded");
    expect(run.stages.c.status).toBe("skipped");
    expect(run.stages.b.status).toBe("skipped");
    expect(run.loopState).toBe("done");
  });

  it("emits pipeline.stage.terminated observations for cascade-skipped stages", () => {
    const pipeline = makePipeline(
      [
        makeStage("a"),
        {
          ...makeStage("b"),
          dependsOn: ["a"],
          routes: { when: { kind: "anyFailed", stages: ["a"] } },
        },
      ],
      1,
    );

    const triggered = fireTrigger(pipeline);
    const started = startStage(triggered.state, asRunId("run-1"), "a");
    const { effects } = completeStage(started.state, asRunId("run-1"), "a");

    const skipObs = effects.find(
      (e) =>
        e.type === "EMIT_OBSERVATION" &&
        e.event.name === "pipeline.stage.terminated" &&
        (e.event.data as { stageName?: string; status?: string }).stageName === "b" &&
        (e.event.data as { stageName?: string; status?: string }).status === "skipped",
    );
    expect(skipObs).toBeDefined();
  });

  it("runs the stage when its routes predicate is satisfied", () => {
    const pipeline = makePipeline(
      [
        makeStage("a"),
        {
          ...makeStage("b"),
          dependsOn: ["a"],
          routes: { when: { kind: "allSucceeded", stages: ["a"] } },
        },
      ],
      1,
    );
    const triggered = fireTrigger(pipeline);
    const started = startStage(triggered.state, asRunId("run-1"), "a");
    const { state, effects } = completeStage(started.state, asRunId("run-1"), "a");

    expect(state.runs[asRunId("run-1")].stages.b.status).toBe("pending");
    const startB = effects.find((e) => e.type === "START_STAGE" && e.stage.name === "b");
    expect(startB).toBeDefined();
  });

  it("anySucceeded predicate runs the stage if any upstream succeeded", () => {
    // Two independent producers feed a fan-in: c only needs one to succeed.
    const pipeline = makePipeline(
      [
        makeStage("a"),
        makeStage("b"),
        {
          ...makeStage("c"),
          dependsOn: ["a", "b"],
          routes: { when: { kind: "anySucceeded", stages: ["a", "b"] } },
        },
      ],
      2,
    );
    const triggered = fireTrigger(pipeline);
    let s = startStage(triggered.state, asRunId("run-1"), "a", NOW + 1).state;
    s = startStage(s, asRunId("run-1"), "b", NOW + 2).state;
    s = completeStage(s, asRunId("run-1"), "a", NOW + 3).state;
    const finalRes = completeStage(s, asRunId("run-1"), "b", NOW + 4);

    const startC = finalRes.effects.find((e) => e.type === "START_STAGE" && e.stage.name === "c");
    expect(startC).toBeDefined();
    expect(finalRes.state.runs[asRunId("run-1")].stages.c.status).toBe("pending");
  });
});

describe("pipeline DAG — multi-pipeline support", () => {
  it("validates and parses a config with multiple named pipelines", () => {
    const result = PipelinesConfigSchema.safeParse({
      review: {
        stages: [makeStage("review")],
      },
      ship: {
        stages: [makeStage("build"), { ...makeStage("deploy"), dependsOn: ["build"] }],
        maxConcurrentStages: 2,
      },
    });
    expect(result.success).toBe(true);
    if (!result.success) return;
    expect(Object.keys(result.data).sort()).toEqual(["review", "ship"]);
    expect(result.data.ship.stages).toHaveLength(2);
  });

  it("rejects a multi-pipeline config when one pipeline has a cycle", () => {
    const result = PipelinesConfigSchema.safeParse({
      good: { stages: [makeStage("a")] },
      bad: {
        stages: [
          { ...makeStage("x"), dependsOn: ["y"] },
          { ...makeStage("y"), dependsOn: ["x"] },
        ],
      },
    });
    expect(result.success).toBe(false);
    if (result.success) return;
    const messages = result.error.issues.map((i) => i.message).join("\n");
    expect(messages).toContain("stage dependency cycle");
  });
});
