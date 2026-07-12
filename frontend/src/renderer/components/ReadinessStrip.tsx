import type { CSSProperties } from "react";
import { cn } from "../lib/utils";
import type { Readiness, ReadinessGate, ReadinessHue, ReadinessTone } from "../lib/readiness";

// Verdict hue → sanctioned board lane palette (DESIGN.md). Blue = waiting on a
// process, amber = agent working, coral/red = blocked/needs-you, green =
// ready/merged, grey = not started.
const HUE_VAR: Record<ReadinessHue, string> = {
	working: "var(--lane-working)",
	review: "var(--lane-review)",
	needs: "var(--lane-needs)",
	merge: "var(--lane-merge)",
	todo: "var(--lane-todo)",
};

// Per-gate tone → semantic status token.
const TONE_VAR: Record<ReadinessTone, string> = {
	pass: "var(--green)",
	wait: "var(--amber)",
	block: "var(--red)",
	idle: "var(--fg-passive)",
};

/**
 * The Summary-tab readiness / gating strip: a headline verdict + one-line reason,
 * over a horizontal merge-pipeline "spine" whose gates are colored by state so
 * "how far along, and where's the blocker?" reads at a glance. Pure presentation
 * over {@link deriveReadiness} output.
 */
export function ReadinessStrip({ readiness }: { readiness: Readiness }) {
	const { verdict, gates, currentKey, contextLabel } = readiness;
	return (
		<div
			className="readiness"
			style={{ "--rs-hue": HUE_VAR[verdict.hue] } as CSSProperties}
			role="status"
			aria-label={`Merge readiness: ${verdict.word}. ${verdict.caption}`}
		>
			<div className="readiness__verdict">
				<span className={cn("readiness__dot", verdict.pulse && "readiness__dot--pulse")} aria-hidden="true" />
				<span className="readiness__headline">{verdict.word}</span>
				{contextLabel ? <span className="readiness__ctx">{contextLabel}</span> : null}
			</div>
			<p className="readiness__caption">{verdict.caption}</p>
			<div className="readiness__pipe" role="list" aria-label="Merge pipeline gates">
				{gates.map((gate) => (
					<ReadinessGateNode key={gate.key} gate={gate} current={gate.key === currentKey} />
				))}
			</div>
		</div>
	);
}

function ReadinessGateNode({ gate, current }: { gate: ReadinessGate; current: boolean }) {
	return (
		<div
			className="readiness-gate"
			role="listitem"
			data-tone={gate.tone}
			aria-label={`${gate.label}: ${gate.state}`}
			style={{ "--tone": TONE_VAR[gate.tone] } as CSSProperties}
		>
			<span
				className={cn(
					"readiness-gate__node",
					gate.tone === "pass" && "readiness-gate__node--fill",
					gate.tone === "block" && "readiness-gate__node--fill readiness-gate__node--block",
					current && "readiness-gate__node--ring",
				)}
				aria-hidden="true"
			/>
			<span className="readiness-gate__label">{gate.label}</span>
			<span className="readiness-gate__state">{gate.state}</span>
		</div>
	);
}
