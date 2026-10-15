import type { ReactNode } from "react";
import { jiraUrl, statusTone } from "../../lib/releases/cards";
import { shortDay } from "../../lib/releases/day";
import { shortBranch } from "../../lib/releases/llb";
import type { Lens } from "../../lib/releases/lens";
import { DEST_META, destLabel, mineText } from "../../lib/releases/sprint";
import type { Board, CardUnit, MineReason, SprintCard, SprintRow, Train } from "../../lib/releases/types";
import { CODE_META } from "../../lib/releases/vocab";
import { CardLine, Face, JiraKey } from "./CardGroups";
import type { PageActions } from "./ReleasesPage";

type Ctx = { board: Board; lens: Lens; actions: PageActions };
type Sheet = { title: ReactNode; body: ReactNode };

function appName(b: Board, id: string): string {
	return b.apps.find((a) => a.id === id)?.name ?? id;
}

function Sec({ title, children }: { title: string; children: ReactNode }) {
	return (
		<div className="sec">
			<div className="sec-h">
				<span className="rel-eyebrow">{title}</span>
			</div>
			{children}
		</div>
	);
}

function Mine({ mine }: { mine: MineReason[] | undefined }) {
	if (!mine?.length) return null;
	return (
		<>
			<span>Yours</span>
			<span className="c" data-st="accent" data-testid="card-mine">
				{mineText(mine)}
			</span>
		</>
	);
}

export function findSprintCard(b: Board, key: string): { row: SprintRow; card: SprintCard } | null {
	for (const row of b.sprints) {
		const card = row.cards.find((c) => c.key === key);
		if (card) return { row, card };
	}
	return null;
}

function unitsOf(b: Board, key: string): { train: Train; unit: CardUnit }[] {
	return b.trains.flatMap((train) =>
		train.versions.flatMap((v) => v.cards.filter((u) => u.key === key).map((unit) => ({ train, unit }))),
	);
}

export function cardSheet(key: string, c: Ctx): Sheet {
	const b = c.board;
	const sp = findSprintCard(b, key);
	const units = unitsOf(b, key);
	const llbCard = b.llbs.flatMap((g) => g.branches.flatMap((br) => br.cards)).find((x) => x.key === key);
	const head = sp?.card ?? units[0]?.unit ?? llbCard;
	if (!head) return { title: key, body: <div className="sec empty">This card is no longer on the Board.</div> };
	const summary = sp?.card.summary ?? units[0]?.unit.summary ?? llbCard?.summary ?? "";
	const url = sp?.card.url ?? units[0]?.unit.url ?? jiraUrl(b, key);
	const mine = sp?.card.mine ?? units.find((u) => u.unit.mine?.length)?.unit.mine;
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
				<div className="sec text-[14px]">{summary}</div>
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
					{sp && (
						<>
							<span>Sprint</span>
							<span>
								{sp.card.onBoard ? (
									<button
										type="button"
										className="lnk"
										onClick={() => c.actions.openSheet({ kind: "sprint", board: sp.row.board.key }, "push")}
									>
										{sp.row.sprint.name}
									</button>
								) : (
									<span className="c" data-st="waiting">
										Not on the {sp.row.board.key} board, but has an MR into the sprint branch
									</span>
								)}
							</span>
						</>
					)}
					<Mine mine={mine} />
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
					<Sec title="Ships in">
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
					</Sec>
				)}
				{sp && sp.card.routes.length > 0 && (
					<Sec title="Where the code goes">
						{sp.card.routes.map((r) => {
							const m = DEST_META[r.kind];
							const mrs = sp.card.mrs.filter((x) => x.app === r.app);
							return (
								<div key={r.app} className="srow" data-testid="card-route">
									<span className="cg c" data-st={m.tone}>
										{m.glyph}
									</span>
									<span>
										<b>{appName(b, r.app)}</b> ·{" "}
										<span className="c" data-st={m.tone}>
											{m.label}
										</span>{" "}
										→ {destLabel(b, r.to)}
										{r.mismatch && (
											<span className="c" data-st="failed">
												{" "}
												· not where its page or fixVersion says
											</span>
										)}
									</span>
									<span />
									<span className="d">
										{mrs.map((x) => (
											<div key={x.iid}>
												<a href={x.url} target="_blank" rel="noreferrer">
													!{x.iid}
												</a>{" "}
												{x.title} → <span className="mono">{x.target}</span> · {x.state}
												{x.merged ? ` ${shortDay(x.merged)}` : ""} · {x.author}
											</div>
										))}
									</span>
								</div>
							);
						})}
					</Sec>
				)}
				{sp && sp.card.subtasks.length > 0 && (
					<Sec title={`Subtasks (${sp.card.subtasks.length})`}>
						{sp.card.subtasks.map((s) => (
							<CardLine
								key={s.key}
								glyph={statusTone(s.status, s.statusCategory) === "done" ? "✓" : "·"}
								glyphTone={statusTone(s.status, s.statusCategory)}
								glyphTitle={s.mobile ? "mobile subtask" : "subtask"}
								person={s.assignee}
								k={s.key}
								url={jiraUrl(b, s.key)}
								summary={s.summary}
								status={s.status}
								statusCategory={s.statusCategory}
								className="wide"
							/>
						))}
					</Sec>
				)}
			</>
		),
	};
}

export function sprintSheet(boardKey: string, c: Ctx): Sheet {
	const row = c.board.sprints.find((s) => s.board.key === boardKey);
	if (!row)
		return { title: boardKey, body: <div className="sec empty">This board has no active sprint on the Board.</div> };
	const ships = c.board.trains.find((t) => t.label === `#${row.number + 1}`);
	const cards = c.lens.sprintCards(row);
	return {
		title: (
			<>
				{row.sprint.name}
				<span className="badge" data-st="accent">
					active
				</span>
			</>
		),
		body: (
			<>
				<div className="sec kv">
					<span>Board</span>
					<span>{row.board.key}</span>
					<span>Dates</span>
					<span>
						{row.sprint.start ? shortDay(row.sprint.start) : "-"} → {row.sprint.end ? shortDay(row.sprint.end) : "-"}
					</span>
					{row.board.ownsSprintBranch && (
						<>
							<span>Ships in</span>
							<span>
								{ships ? (
									<button
										type="button"
										className="lnk"
										onClick={() => c.actions.openSheet({ kind: "train", train: ships.id }, "push")}
									>
										Train {ships.label}
									</button>
								) : (
									`#${row.number + 1}`
								)}
							</span>
						</>
					)}
					<span>Cards</span>
					<span>
						{row.counts.new} new this sprint, {row.counts.carriedWithMrs} carried over with MRs or open mobile work,{" "}
						{row.counts.carriedDone} carried over with mobile done. {row.total} on the board in all.
					</span>
					<span>Jira</span>
					<span>
						<a href={row.sprint.url} target="_blank" rel="noreferrer">
							Open sprint
						</a>
					</span>
				</div>
				<Sec title="Where each card ships">
					{row.dests.map((d) => {
						const keys = new Set(d.keys);
						const list = cards.filter((x) => keys.has(x.key));
						if (!list.length) return null;
						return (
							<div key={JSON.stringify(d.to)} className="dgrp" data-testid="sprint-dest">
								<div className="dgh">
									{destLabel(c.board, d.to)}
									<i>{list.length}</i>
								</div>
								{list.map((card) => (
									<CardLine
										key={card.key}
										glyph="·"
										glyphTone={statusTone(card.status, card.statusCategory)}
										glyphTitle={card.status}
										person={card.assignee}
										k={card.key}
										url={card.url}
										summary={card.summary}
										status={card.status}
										statusCategory={card.statusCategory}
										className="wide"
										onOpen={() => c.actions.openSheet({ kind: "card", key: card.key }, "push")}
									/>
								))}
							</div>
						);
					})}
				</Sec>
			</>
		),
	};
}
