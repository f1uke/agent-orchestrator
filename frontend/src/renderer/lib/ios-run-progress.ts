import type { IosRun } from "../hooks/useIosProject";

export type RunProgressView =
	| { kind: "real" | "estimate"; fraction: number; label: string; tooltip: string }
	| { kind: "indeterminate" | "running"; label: string; tooltip: string };

const ESTIMATE_CAP = 0.95;

const PHASE_LABEL: Record<NonNullable<NonNullable<IosRun["build"]>["phase"]>, string> = {
	resolving: "Resolving packages",
	planning: "Planning build",
	compiling: "Compiling",
	linking: "Linking",
	signing: "Signing",
	building: "Building",
};

export function formatDuration(seconds: number): string {
	const whole = Math.max(0, Math.round(seconds));
	if (whole < 60) return `${whole}s`;
	return `${Math.floor(whole / 60)}m ${whole % 60}s`;
}

export function builtName(run: Pick<IosRun, "scheme" | "configuration">): string {
	return run.configuration ? `${run.scheme} (${run.configuration})` : run.scheme;
}

export function runProgress(run: IosRun, now: number): RunProgressView {
	const name = builtName(run);
	switch (run.stage) {
		case "preparing":
		case undefined:
			return { kind: "indeterminate", label: "Starting", tooltip: `Starting ${name}` };
		case "booting":
			return { kind: "indeterminate", label: "Booting simulator", tooltip: `Booting the simulator for ${name}` };
		case "installing":
			return { kind: "real", fraction: 1, label: "Installing", tooltip: `Built ${name}; installing it` };
		case "launching":
			return { kind: "real", fraction: 1, label: "Launching", tooltip: `Built ${name}; launching it` };
		case "app-running":
			return { kind: "running", label: `Running ${name}`, tooltip: `${name} is running. Stop terminates it.` };
		case "building":
			return buildProgress(run, now, name);
	}
}

function buildProgress(run: IosRun, now: number, name: string): RunProgressView {
	const build = run.build;
	const phase = PHASE_LABEL[build?.phase ?? "building"];
	const counts = build?.counts;
	if (counts && !build?.shared) {
		return {
			kind: "real",
			fraction: Math.min(Math.max(counts.fraction, 0), 1),
			label: `${phase} ${counts.done}/${counts.total}`,
			tooltip: `Building ${name}: ${counts.done} of ${counts.total} build tasks done (${Math.round(counts.fraction * 100)}%)`,
		};
	}
	const last = run.lastBuildSeconds;
	const startedAt = run.buildStartedAt ? Date.parse(run.buildStartedAt) : Number.NaN;
	if (last && last > 0 && Number.isFinite(startedAt)) {
		const elapsed = Math.max(0, now - startedAt) / 1000;
		const why = build?.shared
			? "Another build on this Mac is reporting progress, so this build's own counts cannot be told apart. "
			: "";
		return {
			kind: "estimate",
			fraction: Math.min(elapsed / last, ESTIMATE_CAP),
			label: phase,
			tooltip: `${why}Estimated from the last build of ${name}, ${formatDuration(last)}`,
		};
	}
	const tooltip = build?.shared
		? `Building ${name}. Another build on this Mac is reporting progress, so this build's own counts cannot be told apart, and there is no earlier build like this one to estimate from.`
		: `Building ${name}`;
	return { kind: "indeterminate", label: phase, tooltip };
}

export function runTiming(run: IosRun, now: number): string | null {
	if (run.state === "running") {
		if (run.stage === "app-running") return null;
		const startedAt = Date.parse(run.startedAt);
		return Number.isFinite(startedAt) ? formatDuration(Math.max(0, now - startedAt) / 1000) : null;
	}
	return run.buildSeconds ? `last build ${formatDuration(run.buildSeconds)}` : null;
}

export type RingState = { key: string; fraction?: number; real: boolean };

export function nextRing(prior: RingState | null, view: RunProgressView | null, key: string): RingState {
	const same = prior?.key === key ? prior : null;
	if (!view || (view.kind !== "real" && view.kind !== "estimate")) return same ?? { key, real: false };
	const next = { key, fraction: view.fraction, real: view.kind === "real" };
	if (!same || same.fraction === undefined) return next;
	const countsReplaceAnEstimate = next.real && !same.real;
	const sameSourceMovedOn = next.real === same.real && next.fraction > same.fraction;
	return countsReplaceAnEstimate || sameSourceMovedOn ? next : same;
}
