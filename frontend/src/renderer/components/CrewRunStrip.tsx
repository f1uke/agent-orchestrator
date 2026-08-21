import { AlertTriangle, CircleSlash, Loader, ShieldOff } from "lucide-react";
import { MONO, PALETTE as P } from "../lib/smoke-test";
import {
	CREW_RUN_MAX_ATTEMPTS,
	type CrewRun,
	crewRunDuration,
	crewRunEscalated,
	crewRunMeta,
	crewRunState,
	crewRunTitle,
	discardStreak,
} from "../lib/crew-run";

/**
 * Tests tab - "Machine runs": every build, test suite or device pass this member
 * bracketed with `ao crew run`, newest first, and what the tree-write detector
 * concluded about each.
 *
 * It exists because a run thrown away silently is no better than a mixed result
 * reported as clean. A discarded run is a THIRD state next to pass and fail, and
 * the only place a person can see one is here.
 *
 * Absent entirely when the session has never bracketed a run - which is the
 * truth for every solo session and every project that does not use the bracket,
 * and their Tests tab must look exactly as it did before this existed.
 */
export function CrewRunStrip({ runs }: { runs: CrewRun[] }) {
	if (runs.length === 0) return null;
	const now = Date.now();
	const streak = discardStreak(runs);
	const escalated = crewRunEscalated(runs);

	return (
		<section
			aria-label="Machine runs"
			style={{
				marginBottom: 12,
				border: `1px solid ${P.borderCard}`,
				background: P.cardBg,
				borderRadius: 10,
				overflow: "hidden",
			}}
		>
			<div
				style={{
					display: "flex",
					alignItems: "baseline",
					justifyContent: "space-between",
					gap: 8,
					padding: "10px 12px 8px",
				}}
			>
				<span style={{ fontSize: 10, fontWeight: 700, letterSpacing: ".06em", color: P.secondary }}>MACHINE RUNS</span>
				<span style={{ fontSize: 11, color: P.muted2 }}>
					{runs.length} bracketed run{runs.length === 1 ? "" : "s"}
				</span>
			</div>

			{escalated && <EscalationBanner streak={streak} />}

			<div style={{ display: "flex", flexDirection: "column" }}>
				{runs.map((run) => (
					<RunRow key={run.id} run={run} now={now} />
				))}
			</div>
		</section>
	);
}

/**
 * The escalation. Three discards in a row means this member cannot get a quiet
 * window in its own worktree, and the automatic retry is spent - so the decision
 * is a person's, and the banner says exactly which decision.
 */
function EscalationBanner({ streak }: { streak: number }) {
	return (
		<div
			style={{
				margin: "0 12px 10px",
				border: `1px solid ${P.qaBorder}`,
				background: P.qaBg,
				borderRadius: 8,
				padding: "8px 10px",
				display: "flex",
				alignItems: "flex-start",
				gap: 7,
			}}
		>
			<AlertTriangle
				size={12}
				strokeWidth={2.2}
				color={P.segFail}
				aria-hidden="true"
				style={{ flex: "none", marginTop: 1 }}
			/>
			<span style={{ fontSize: 11.5, lineHeight: 1.45, color: P.qaFg }}>
				<b style={{ fontWeight: 600 }}>{streak} runs discarded in a row</b> - the tree changed under each one, so none
				of them can be trusted. Automatic re-runs stop after {CREW_RUN_MAX_ATTEMPTS}. Pause the other member so this one
				gets a quiet tree, or accept an uncertified result.
			</span>
		</div>
	);
}

function RunRow({ run, now }: { run: CrewRun; now: number }) {
	const state = crewRunState(run);
	const meta = crewRunMeta(run);
	const duration = crewRunDuration(run, now);

	return (
		<div
			style={{
				display: "flex",
				alignItems: "flex-start",
				gap: 10,
				padding: "9px 12px",
				borderTop: `1px solid ${P.borderExpand}`,
			}}
		>
			<StatePill state={state} meta={meta} />
			<div style={{ flex: 1, minWidth: 0 }}>
				<div
					style={{
						fontSize: 12.5,
						fontWeight: 600,
						color: P.text,
						overflow: "hidden",
						textOverflow: "ellipsis",
						whiteSpace: "nowrap",
					}}
				>
					{crewRunTitle(run)}
				</div>
				<div style={{ marginTop: 3, fontSize: 11.5, lineHeight: 1.45, color: P.secondary2 }}>{meta.caption}</div>
				{state === "discarded" && run.changedPaths && run.changedPaths.length > 0 && (
					<>
						{/* The label sits on its own line, matching STEPS TO PLAY and
						    EXPECTED on the case cards below, so the path chips always
						    start from the same edge however many of them there are. */}
						<div style={{ marginTop: 7, fontSize: 10, fontWeight: 700, letterSpacing: ".06em", color: P.secondary }}>
							WHAT MOVED
						</div>
						<div style={{ marginTop: 5, display: "flex", alignItems: "center", gap: 6, flexWrap: "wrap", rowGap: 4 }}>
							{run.changedPaths.map((path) => (
								<span
									key={path}
									style={{
										fontFamily: MONO,
										fontSize: 10.5,
										color: P.refChip,
										border: `1px solid ${P.borderPill}`,
										background: P.pillBg,
										borderRadius: 4,
										padding: "1px 5px",
									}}
								>
									{path}
								</span>
							))}
						</div>
					</>
				)}
				{state === "uncertified" && run.detectorReason && (
					<div style={{ marginTop: 5, fontSize: 10.5, lineHeight: 1.45, color: P.muted2 }}>
						Reason: {run.detectorReason}
					</div>
				)}
			</div>
			<span style={{ flex: "none", fontSize: 10.5, color: P.muted2, paddingTop: 2 }}>{duration}</span>
		</div>
	);
}

function StatePill({ state, meta }: { state: ReturnType<typeof crewRunState>; meta: ReturnType<typeof crewRunMeta> }) {
	const Icon =
		state === "running" ? Loader : state === "discarded" ? CircleSlash : state === "uncertified" ? ShieldOff : null;
	return (
		<span
			style={{
				flex: "none",
				display: "inline-flex",
				alignItems: "center",
				gap: 5,
				fontSize: 10.5,
				fontWeight: 700,
				letterSpacing: ".03em",
				color: meta.color,
				background: meta.pillBg,
				border: `1px solid ${meta.pillBorder}`,
				borderRadius: 999,
				padding: "2px 8px",
				marginTop: 1,
				// A FIXED width, not a minimum: the pill is the strip's left rail, and
				// every row's title has to start from the same edge. UNCERTIFIED is the
				// longest label, so it sets the column.
				width: 110,
				justifyContent: "center",
			}}
		>
			{Icon && <Icon size={11} strokeWidth={2.4} aria-hidden="true" style={{ flex: "none" }} />}
			{meta.label.toUpperCase()}
		</span>
	);
}
