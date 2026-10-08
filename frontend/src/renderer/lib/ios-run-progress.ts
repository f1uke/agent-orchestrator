import type { IosRun } from "../hooks/useIosProject";

export type RunProgressView = {
	kind: "real" | "estimate" | "indeterminate";
	/** 0-1; absent when indeterminate. */
	fraction?: number;
	label: string;
	tooltip: string;
};

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

/** What the run bar shows for a run that is still going, at time `now` (ms). */
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
	return { kind: "indeterminate", label: phase, tooltip: `Building ${name}` };
}
