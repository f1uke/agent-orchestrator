import type { ReactNode } from "react";
import { jiraUrl, statusTone } from "../../lib/releases/cards";
import { shortBranch } from "../../lib/releases/llb";
import type { Board, CardUnit, Train } from "../../lib/releases/types";
import { CODE_META } from "../../lib/releases/vocab";
import { Face, JiraKey } from "./CardGroups";
import type { PageActions } from "./ReleasesPage";

type Ctx = { board: Board; actions: PageActions };
type Sheet = { title: ReactNode; body: ReactNode };

function appName(b: Board, id: string): string {
	return b.apps.find((a) => a.id === id)?.name ?? id;
}

function unitsOf(b: Board, key: string): { train: Train; unit: CardUnit }[] {
	return b.trains.flatMap((train) =>
		train.versions.flatMap((v) => v.cards.filter((u) => u.key === key).map((unit) => ({ train, unit }))),
	);
}

export function cardSheet(key: string, c: Ctx): Sheet {
	const b = c.board;
	const units = unitsOf(b, key);
	const llbCard = b.llbs.flatMap((g) => g.branches.flatMap((br) => br.cards)).find((x) => x.key === key);
	const head = units[0]?.unit ?? llbCard;
	if (!head) return { title: key, body: <div className="sec empty">This card is no longer on the Board.</div> };
	const url = units[0]?.unit.url ?? jiraUrl(b, key);
	return {
		title: (
			<>
				<JiraKey k={key} url={url} />
				<span className="spill" data-st={statusTone(head.status, head.statusCategory)}>
					{head.status ?? "-"}
				</span>
			</>
		),
		body: (
			<>
				<div className="sec text-[14px]" data-testid="card-summary">
					{head.summary}
				</div>
				<div className="sec kv">
					<span>Assignee</span>
					<span className="inline-flex items-center gap-1.5">
						{head.assignee ? (
							<>
								<Face person={head.assignee} /> {head.assignee.name}
							</>
						) : (
							"-"
						)}
					</span>
					{url && (
						<>
							<span>Jira</span>
							<span>
								<a href={url} target="_blank" rel="noreferrer">
									Open card
								</a>
							</span>
						</>
					)}
				</div>
				{units.length > 0 && (
					<div className="sec">
						<div className="sec-h">
							<span className="rel-eyebrow">Ships in</span>
						</div>
						{units.map(({ train, unit }) => {
							const m = CODE_META[unit.code.kind];
							const into =
								unit.code.kind === "parked" || unit.code.kind === "elsewhere"
									? ` into ${unit.code.branch}`
									: unit.code.kind === "merged" && unit.code.via
										? ` via ${shortBranch(unit.code.via)}`
										: "";
							return (
								<div key={`${train.id}:${unit.app}`} className="srow" data-testid="card-unit">
									<span className="cg c" data-st={m.tone}>
										{m.glyph}
									</span>
									<span>
										<button
											type="button"
											className="lnk"
											onClick={() => c.actions.openSheet({ kind: "version", train: train.id, app: unit.app }, "push")}
										>
											{appName(b, unit.app)} · {train.label}
										</button>{" "}
										<span className="c" data-st={m.tone}>
											{m.label}
											{into}
										</span>
									</span>
									<span className="passive text-[11.5px]">{unit.why.join(", ")}</span>
								</div>
							);
						})}
					</div>
				)}
			</>
		),
	};
}
