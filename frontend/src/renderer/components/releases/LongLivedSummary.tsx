import { GitFork } from "lucide-react";
import { dayDiff } from "../../lib/releases/day";
import type { Board, LLBGroup, Train } from "../../lib/releases/types";

function targetTrain(g: LLBGroup): string | null {
	return g.target.kind === "planned" || g.target.kind === "landed" ? g.target.train : null;
}

function urgency(g: LLBGroup, b: Board): "done" | "failed" | "waiting" | "accent" {
	if (g.target.kind === "landed") return "done";
	const t = b.trains.find((x) => x.id === targetTrain(g));
	if (g.target.kind !== "planned" || !t?.freeze) return "accent";
	const days = dayDiff(b.today, t.freeze);
	return days < 0 ? "failed" : days <= 3 ? "waiting" : "accent";
}

/** The folded long-lived line; unfolding into lanes and wires is the next page slice. */
export function LongLivedSummary({
	groups,
	board,
	focus,
	width,
}: {
	groups: LLBGroup[];
	board: Board;
	focus: Train | null;
	width: number;
}) {
	const due = groups.filter((g) => targetTrain(g) === focus?.id);
	const none = groups.filter((g) => g.target.kind === "none").length;
	return (
		<div className="rel-llbsum" style={{ top: 6, width }} data-testid="llb-summary">
			<span className="ls">
				<GitFork className="ic size-3" aria-hidden="true" />
				<b>{groups.length} long-lived</b>
				{due.length > 0 && focus && (
					<>
						<span className="sep">·</span>
						<span className="c" data-st={urgency(due[0], board)}>
							{due.map((g) => g.plan?.name ?? g.name).join(", ")} → {focus.label}
						</span>
					</>
				)}
				{none > 0 && (
					<>
						<span className="sep">·</span>
						<span className="passive">{none} no target</span>
					</>
				)}
			</span>
		</div>
	);
}
