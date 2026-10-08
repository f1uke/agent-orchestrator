import { describe, expect, it } from "vitest";
import type { IosRun } from "../hooks/useIosProject";
import { formatDuration, runProgress } from "./ios-run-progress";

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
		expect(view.kind).toBe("estimate");
		expect(view.fraction).toBeCloseTo(0.5);
		expect(view.label).toBe("Compiling");
		expect(view.tooltip).toContain("Estimated from the last build of NterApp (Dev), 1m 40s");
	});

	it("caps an estimate below done while the build is still going", () => {
		const view = runProgress(running({ lastBuildSeconds: 100 }), T0 + 500_000);
		expect(view.fraction).toBe(0.95);
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
