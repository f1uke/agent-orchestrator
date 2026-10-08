import { describe, expect, it } from "vitest";
import type { IosRun } from "../hooks/useIosProject";
import { formatDuration, nextRing, runProgress, runTiming } from "./ios-run-progress";

const T0 = Date.parse("2026-10-08T09:00:00Z");

const running = (over: Partial<IosRun> = {}): IosRun => ({
	handleId: "iosrun-mer-9",
	scheme: "NterApp",
	configuration: "Dev",
	udid: "UDID-A",
	state: "running",
	startedAt: "2026-10-08T09:00:00Z",
	stage: "building",
	stageStartedAt: "2026-10-08T09:00:00Z",
	buildStartedAt: "2026-10-08T09:00:00Z",
	...over,
});

describe("runProgress", () => {
	it("uses the build's own task counts when it has them", () => {
		const view = runProgress(
			running({ build: { phase: "compiling", counts: { done: 120, total: 480, fraction: 0.25 }, errors: 0, warnings: 3 } }),
			T0 + 30_000,
		);
		expect(view).toMatchObject({ kind: "real", fraction: 0.25, label: "Compiling 120/480" });
		expect(view.tooltip).toContain("120 of 480 build tasks");
	});

	it("estimates from the last build when there are no counts, and says so", () => {
		const view = runProgress(running({ lastBuildSeconds: 100, build: { phase: "compiling", errors: 0, warnings: 0 } }), T0 + 50_000);
		expect(view).toMatchObject({ kind: "estimate" });
		expect("fraction" in view && view.fraction).toBeCloseTo(0.5);
		expect(view.label).toBe("Compiling");
		expect(view.tooltip).toContain("Estimated from the last build of NterApp (Dev), 1m 40s");
	});

	it("caps an estimate below done while the build is still going", () => {
		expect(runProgress(running({ lastBuildSeconds: 100 }), T0 + 500_000)).toMatchObject({ fraction: 0.95 });
	});

	it("estimates, and says why, while another build on this Mac makes the counts ambiguous", () => {
		const view = runProgress(
			running({ lastBuildSeconds: 100, build: { phase: "linking", shared: true, errors: 0, warnings: 0 } }),
			T0 + 10_000,
		);
		expect(view.kind).toBe("estimate");
		expect(view.tooltip).toContain("Another build on this Mac");
	});

	it("stays indeterminate with no counts and no history", () => {
		expect(runProgress(running({ build: { phase: "planning", errors: 0, warnings: 0 } }), T0 + 10_000)).toMatchObject({
			kind: "indeterminate",
			label: "Planning build",
		});
	});

	it("names the steps around the build", () => {
		expect(runProgress(running({ stage: "booting" }), T0).label).toBe("Booting simulator");
		expect(runProgress(running({ stage: "installing" }), T0)).toMatchObject({ label: "Installing", kind: "real", fraction: 1 });
		expect(runProgress(running({ stage: "launching" }), T0)).toMatchObject({ label: "Launching", fraction: 1 });
		expect(runProgress(running({ stage: undefined }), T0).label).toBe("Starting");
	});
});

describe("formatDuration", () => {
	it("reads like Xcode's", () => {
		expect(formatDuration(4.2)).toBe("4s");
		expect(formatDuration(102)).toBe("1m 42s");
		expect(formatDuration(3600)).toBe("60m 0s");
	});
});

describe("runTiming", () => {
	it("counts up while the run works towards launching", () => {
		expect(runTiming(running(), T0 + 42_400)).toBe("42s");
		expect(runTiming(running({ stage: "installing" }), T0 + 102_000)).toBe("1m 42s");
	});

	it("stops counting once the app runs", () => {
		expect(runTiming(running({ stage: "app-running" }), T0 + 300_000)).toBeNull();
	});

	it("says how long the last build took once the run ended", () => {
		expect(runTiming(running({ state: "succeeded", stage: undefined, buildSeconds: 102 }), T0)).toBe("last build 1m 42s");
		expect(runTiming(running({ state: "failed", stage: undefined }), T0)).toBeNull();
	});
});

describe("nextRing", () => {
	const estimate = (fraction: number) => ({ kind: "estimate" as const, fraction, label: "", tooltip: "" });
	const real = (fraction: number) => ({ kind: "real" as const, fraction, label: "", tooltip: "" });

	it("never goes backwards within one source", () => {
		let ring = nextRing(null, real(0.4), "run-1");
		ring = nextRing(ring, real(0.3), "run-1");
		expect(ring.fraction).toBe(0.4);
	});

	it("lets the build's own counts replace an estimate that ran ahead", () => {
		let ring = nextRing(null, estimate(0.6), "run-1");
		ring = nextRing(ring, real(0.05), "run-1");
		expect(ring.fraction).toBe(0.05);
		ring = nextRing(ring, estimate(0.7), "run-1");
		expect(ring.fraction).toBe(0.05);
	});

	it("starts over for a new run", () => {
		const ring = nextRing(nextRing(null, real(0.9), "run-1"), real(0.1), "run-2");
		expect(ring.fraction).toBe(0.1);
	});

	it("keeps the last value while a step reports none", () => {
		const ring = nextRing(nextRing(null, real(0.5), "run-1"), null, "run-1");
		expect(ring.fraction).toBe(0.5);
	});
});
