import { addDays, dayDiff, dayLabel } from "../../lib/releases/day";
import { trainApprovers, trainChips, type Lens } from "../../lib/releases/lens";
import { derivedOf } from "../../lib/releases/sources";
import type { Board, Train } from "../../lib/releases/types";
import { Avatar } from "./Avatar";
import { SourceTag } from "./Sources";
import type { PageActions } from "./ReleasesPage";
import { Snowflake } from "lucide-react";

const MAX_FACES = 7;

function when(t: Train, today: string): string {
	if (!t.freeze || !t.publish) return "dates not set";
	if (t.publish < today) return `published ${dayDiff(t.publish, today)} days ago`;
	if (t.freeze > today) return `freezes in ${dayDiff(today, t.freeze)} days`;
	return `day ${dayDiff(t.freeze, today) + 1} of ${dayDiff(t.freeze, t.publish) + 1} · publish in ${dayDiff(today, t.publish)} days`;
}

function Progress({ t, today }: { t: Train; today: string }) {
	if (!t.freeze || !t.publish) return null;
	const len = dayDiff(t.freeze, t.publish) + 1;
	const at = dayDiff(t.freeze, today);
	return (
		<span className="rel-prog" aria-hidden="true">
			{Array.from({ length: len }, (_, i) => (
				<i key={i} className={`${i < at ? "past" : i === at ? "now" : ""}${i === len - 1 ? " pub" : ""}`} />
			))}
		</span>
	);
}

export function phaseTone(t: Train): "done" | "accent" | "todo" {
	return t.phase === "past" ? "done" : t.phase === "current" ? "accent" : "todo";
}

export function SummaryStrip({
	board,
	lens,
	history,
	actions,
}: {
	board: Board;
	lens: Lens;
	history: boolean;
	actions: PageActions;
}) {
	const t = lens.focus;
	const sprint = t
		? board.sprints.find((s) => s.board.ownsSprintBranch && !s.next && String(s.number + 1) === t.label.slice(1))
		: undefined;
	const people = t
		? trainApprovers(
				t,
				lens.apps.map((a) => a.id),
			)
		: [];
	const pending = people.filter((p) => p.pending);
	const done = people.length - pending.length;
	return (
		<div className="rel-strip">
			<div className="pick" role="group" aria-label="Train in focus">
				{trainChips(board, history, t?.id ?? null).map((x) => (
					<button
						key={x.id}
						type="button"
						className={`rel-tchip${x.phase === "current" ? " cur" : ""}`}
						data-train={x.id}
						aria-pressed={x.id === t?.id}
						onClick={() => actions.setSearch({ focus: x.id })}
					>
						{x.label}
					</button>
				))}
			</div>
			{t && (
				<div className="rel-sum">
					<div className="blk">
						<div className="ttl">
							<button type="button" onClick={() => actions.openSheet({ kind: "train", train: t.id })}>
								Train {t.label}
							</button>
							<span className="badge" data-st={phaseTone(t)}>
								{t.phase}
							</span>
							<small className="inline-flex items-center gap-1">
								<Snowflake className="size-3" aria-hidden="true" /> {t.freeze ? dayLabel(t.freeze) : "-"} → ●{" "}
								{t.publish ? dayLabel(t.publish) : "-"}
								{sprint ? ` · ships Sprint ${sprint.number}` : ""}
								<SourceTag
									p={derivedOf(
										`${t.label} dates`,
										[
											{ kind: "gitlab.milestones", text: "earliest start and latest due over the apps' milestones" },
											{ kind: "jira.board_sprints", text: '"ships Sprint N": train N carries sprint N - 1' },
										],
										"The page's freeze and publish cells only feed rule A6.",
										"Train.freeze, Train.publish",
									)}
								/>
							</small>
						</div>
						<div className="flex items-center gap-2.5">
							<Progress t={t} today={board.today} />
							<span className="muted text-[12px]">{when(t, board.today)}</span>
						</div>
					</div>
					<div className="blk" style={{ flex: 1 }}>
						<span className="rel-eyebrow">Approvers</span>
						{people.length ? (
							<span className="flex items-center">
								<button
									type="button"
									className="rel-faces"
									data-testid="approver-faces"
									aria-label={`${pending.length} approvers pending, ${done} done. Open train ${t.label}`}
									onClick={() => actions.openSheet({ kind: "train", train: t.id })}
								>
									<span className="fs">
										{(pending.length ? pending : people).slice(0, MAX_FACES).map((p) => (
											<span
												key={p.accountId}
												className={`fc${p.pending ? "" : " ok"}`}
												title={`${p.name}: ${p.pending ? "pending" : "approved"}`}
											>
												<Avatar accountId={p.accountId} name={p.name} />
											</span>
										))}
										{(pending.length ? pending : people).length > MAX_FACES && (
											<span className="fc more">+{(pending.length ? pending : people).length - MAX_FACES}</span>
										)}
									</span>
									<span className="t">
										{pending.length ? (
											<b data-st="waiting" className="c font-medium">
												{pending.length} pending
											</b>
										) : (
											<b data-st="done" className="c font-medium">
												all approved
											</b>
										)}
										{done > 0 && <span className="passive"> · {done} done</span>}
									</span>
								</button>
								<SourceTag
									p={{
										title: `Approvers of ${t.label}`,
										how: "read",
										lines: t.versions
											.filter((v) => v.jira && lens.apps.some((a) => a.id === v.app))
											.map((v) => ({ kind: "jira.versions", text: `${v.jira?.name}: approvers and status` })),
										note: "Pending first. Done = approved on every app.",
										field: "Version.jira.approvers",
									}}
								/>
							</span>
						) : (
							<span className="muted text-[12px]">
								No approvers on any Jira release yet.{" "}
								{t.freeze && board.today <= addDays(t.freeze, 2)
									? `The bot sets them on ${dayLabel(addDays(t.freeze, 2))}.`
									: "The bot did not set them."}
							</span>
						)}
					</div>
				</div>
			)}
		</div>
	);
}
