import type { CSSProperties } from "react";
import type { ApprovalProgress } from "../lib/pr-display";
import { cn } from "../lib/utils";

/**
 * The largest threshold that still renders as pips. Above this the fraction text
 * stands alone (a nine-pip row reads as noise) — see the design's pip ceiling.
 */
const PIP_CEILING = 5;

/**
 * ApprovalMeter is the signature approval-progress affordance: a row of
 * `required` pips with `approved` filled, neutral while short and green once met.
 * It renders nothing when the threshold is unknown (count-only) or exceeds the
 * pip ceiling — the surface still shows the fraction text alongside. Purely
 * decorative: pips are aria-hidden and the surrounding label carries the meaning.
 */
export function ApprovalMeter({ progress, className }: { progress: ApprovalProgress; className?: string }) {
	const { required, approved, met } = progress;
	if (required == null || required > PIP_CEILING) {
		return null;
	}
	const filled = Math.min(approved, required);
	return (
		<span
			className={cn("approval-meter", className)}
			data-met={met}
			aria-hidden="true"
			style={{ "--pip-on": met ? "var(--green)" : "var(--fg-passive)" } as CSSProperties}
		>
			{Array.from({ length: required }, (_, i) => (
				<span key={i} className="approval-meter__pip" data-pip data-on={i < filled} />
			))}
		</span>
	);
}
