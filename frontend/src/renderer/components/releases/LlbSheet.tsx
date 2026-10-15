import { GitFork, X } from "lucide-react";
import { useState, type ReactNode } from "react";
import { useSavePlan } from "../../hooks/useReleaseBoard";
import { dayDiff, dayLabel, shortDay } from "../../lib/releases/day";
import { branchState, targetChip, trainLabel, urgency } from "../../lib/releases/llb";
import {
	draftDirty,
	draftFrom,
	PlanConflictError,
	planProblems,
	targetChoices,
	type PlanDraft,
} from "../../lib/releases/plan";
import type { Board, LLBGroup } from "../../lib/releases/types";
import { CardLine } from "./CardGroups";
import { jiraUrl } from "../../lib/releases/cards";
import type { PageActions } from "./ReleasesPage";

function appName(b: Board, id: string): string {
	return b.apps.find((a) => a.id === id)?.name ?? id;
}

function StatusLine({ g, board }: { g: LLBGroup; board: Board }) {
	const t = g.target;
	let tone = "todo";
	let text: ReactNode;
	if (t.kind === "landed") {
		tone = "done";
		const tr = board.trains.find((x) => x.id === t.train);
		text = (
			<>
				Merged on every platform. Its cards ship in Train <b>{trainLabel(board, t.train)}</b>
				{tr?.freeze ? ` (freeze ${dayLabel(tr.freeze)})` : ""}.
			</>
		);
	} else if (t.kind === "dropped") text = "Marked dropped. Its cards will not ship.";
	else if (t.kind === "none") {
		const n = new Set(g.branches.flatMap((b) => b.cards.map((c) => c.key))).size;
		text = `No target train yet. Its ${n} cards have no train until it merges into a sprint branch.`;
	} else {
		tone = urgency(g, board);
		const freeze = board.trains.find((x) => x.id === t.train)?.freeze;
		const left = g.branches.filter((b) => !b.landing?.mergedOn).map((b) => appName(board, b.app));
		const days = freeze ? dayDiff(board.today, freeze) : null;
		const when =
			days === null
				? "Freeze date not known yet"
				: days < 0
					? `Freeze was ${-days} days ago`
					: days === 0
						? "Freeze is today"
						: `Freeze in ${days} days (${dayLabel(freeze ?? "")})`;
		text = (
			<>
				Targets <b>{trainLabel(board, t.train)}</b>.{" "}
				{left.length ? `Not merged into the sprint branch yet on ${left.join(" and ")}. ` : ""}
				{when}.
			</>
		);
	}
	return (
		<div className="status-line" data-st={tone} data-testid="llb-status">
			<span>{text}</span>
		</div>
	);
}

function PlanForm({ g, board }: { g: LLBGroup; board: Board }) {
	const [draft, setDraft] = useState<PlanDraft>(() => draftFrom(g));
	const [conflict, setConflict] = useState(false);
	const [saved, setSaved] = useState(false);
	const save = useSavePlan();
	const problems = planProblems(draft);
	const dirty = draftDirty(g, draft);
	const set = (patch: Partial<PlanDraft>) => {
		setSaved(false);
		setDraft({ ...draft, ...patch });
	};
	const choice = draft.targetKind === "train" ? (draft.targetTrain ?? "") : draft.targetKind;
	const others = board.llbs
		.flatMap((x) => x.branches)
		.filter((b) => !b.landing?.mergedOn && !draft.branches.some((d) => d.app === b.app && d.branch === b.branch));
	const submit = () => {
		setConflict(false);
		save.mutate(
			{ id: g.id, draft },
			{
				onSuccess: (plan) => {
					setDraft({ ...draft, ifVersion: plan.version });
					setSaved(true);
				},
				onError: (e) => setConflict(e instanceof PlanConflictError),
			},
		);
	};
	const reload = () => {
		setConflict(false);
		setDraft(draftFrom(g));
	};
	const overwrite = () => {
		setConflict(false);
		setDraft({ ...draft, ifVersion: g.plan?.version ?? 0 });
	};
	return (
		<div className="plan" data-testid="plan-form">
			<div className="ph">
				Plan
				<span className="who">
					{g.plan
						? `set by ${g.plan.updatedBy} · ${shortDay(g.plan.updatedAt.slice(0, 10))} · v${g.plan.version}`
						: "not set"}
				</span>
			</div>
			<label className="fl2" htmlFor="plan-name">
				Name
			</label>
			<input id="plan-name" value={draft.name} onChange={(e) => set({ name: e.target.value })} />
			<div className="fl2">Target train</div>
			<div className="tchips2" role="group" aria-label="Target train">
				<button
					type="button"
					className="tc word"
					aria-pressed={choice === "unplanned"}
					onClick={() => set({ targetKind: "unplanned", targetTrain: undefined })}
				>
					Not planned yet
				</button>
				{targetChoices(board).map((c) => (
					<button
						key={c.key}
						type="button"
						className="tc"
						aria-pressed={choice === c.key}
						onClick={() => set({ targetKind: "train", targetTrain: c.key })}
					>
						{c.label}
						{c.freeze && (
							<small>
								{shortDay(c.freeze)}
								{c.guess ? "?" : ""}
							</small>
						)}
					</button>
				))}
				<button
					type="button"
					className="tc word"
					aria-pressed={choice === "dropped"}
					onClick={() => set({ targetKind: "dropped", targetTrain: undefined })}
				>
					Dropped
				</button>
			</div>
			<label className="fl2" htmlFor="plan-note">
				Note
			</label>
			<textarea
				id="plan-note"
				rows={2}
				placeholder="Why it waits, who decides, links"
				value={draft.note}
				onChange={(e) => set({ note: e.target.value })}
			/>
			<div className="fl2">Branches</div>
			{draft.branches.map((b, i) => (
				<div key={`${b.app}:${b.branch}:${i}`} className="brow">
					<span className="plat-pill">{appName(board, b.app)}</span>
					<span className="mono">{b.branch}</span>
					<button
						type="button"
						className="rm"
						aria-label={`Remove ${b.app}:${b.branch}`}
						onClick={() => set({ branches: draft.branches.filter((_, j) => j !== i) })}
					>
						<X className="size-3.5" />
					</button>
				</div>
			))}
			{others.length > 0 && (
				<select
					className="addb"
					aria-label="Add a branch"
					value=""
					onChange={(e) => {
						const [app, ...rest] = e.target.value.split(":");
						if (app) set({ branches: [...draft.branches, { app, branch: rest.join(":") }] });
					}}
				>
					<option value="">Add a branch the matching missed…</option>
					{others.map((b) => (
						<option key={`${b.app}:${b.branch}`} value={`${b.app}:${b.branch}`}>
							{appName(board, b.app)} · {b.branch}
						</option>
					))}
				</select>
			)}
			<div className="fl2">Accepted MRs</div>
			{draft.accepted.length === 0 && <div className="passive text-[12px]">None</div>}
			{draft.accepted.map((a, i) => (
				<div key={`${a.mrRef}:${i}`} className="brow">
					<span className="mono">{a.mrRef}</span>
					<input
						aria-label={`Reason for ${a.mrRef}`}
						value={a.reason}
						onChange={(e) =>
							set({ accepted: draft.accepted.map((x, j) => (j === i ? { ...x, reason: e.target.value } : x)) })
						}
					/>
					<button
						type="button"
						className="rm"
						aria-label={`Remove ${a.mrRef}`}
						onClick={() => set({ accepted: draft.accepted.filter((_, j) => j !== i) })}
					>
						<X className="size-3.5" />
					</button>
				</div>
			))}
			<AcceptPicker
				g={g}
				draft={draft}
				onAdd={(mrRef) => set({ accepted: [...draft.accepted, { mrRef, reason: "" }] })}
			/>
			{problems.length > 0 && dirty && (
				<ul className="probs" data-testid="plan-problems">
					{problems.map((p) => (
						<li key={p}>{p}</li>
					))}
				</ul>
			)}
			{conflict && (
				<div className="status-line" data-st="failed" data-testid="plan-conflict" role="alert">
					<span>
						Changed elsewhere, reload. Your edit is kept here.{" "}
						<button type="button" className="lnk" onClick={reload}>
							Reload
						</button>{" "}
						·{" "}
						<button type="button" className="lnk" onClick={overwrite}>
							Keep my edit
						</button>
					</span>
				</div>
			)}
			{save.isError && !conflict && (
				<div className="status-line" data-st="failed" role="alert">
					<span>{save.error instanceof Error ? save.error.message : "Save failed"}</span>
				</div>
			)}
			<div className="acts2">
				<button
					type="button"
					className="btnp"
					disabled={!dirty || problems.length > 0 || save.isPending}
					onClick={submit}
				>
					Save plan
				</button>
				{dirty && (
					<button type="button" className="btng" onClick={() => setDraft(draftFrom(g))}>
						Discard
					</button>
				)}
				{saved && !dirty && <span className="saved">Saved</span>}
			</div>
		</div>
	);
}

function AcceptPicker({ g, draft, onAdd }: { g: LLBGroup; draft: PlanDraft; onAdd: (ref: string) => void }) {
	const refs = g.branches
		.flatMap((b) => (b.landing ? [`${b.app}!${b.landing.iid}`] : []))
		.filter((r) => !draft.accepted.some((a) => a.mrRef === r));
	if (!refs.length) return null;
	return (
		<select
			className="addb"
			aria-label="Accept a landing MR"
			value=""
			onChange={(e) => e.target.value && onAdd(e.target.value)}
		>
			<option value="">Accept a landing MR…</option>
			{refs.map((r) => (
				<option key={r} value={r}>
					{r}
				</option>
			))}
		</select>
	);
}

export function llbSheet(id: string, board: Board, actions: PageActions): { title: ReactNode; body: ReactNode } {
	const g = board.llbs.find((x) => x.id === id);
	if (!g)
		return { title: "Long-lived branch", body: <div className="sec empty">This group is no longer on the Board.</div> };
	return {
		title: (
			<>
				<GitFork className="inline size-4 text-[var(--purple)]" aria-hidden="true" /> {g.plan?.name ?? g.name}
			</>
		),
		body: (
			<>
				<div className="sec">
					<StatusLine g={g} board={board} />
				</div>
				<PlanForm key={g.id} g={g} board={board} />
				{g.branches.map((b) => {
					const st = branchState(g, b);
					const keys = [...new Map(b.cards.map((c) => [c.key, c])).values()];
					const l = b.landing;
					return (
						<div key={`${b.app}:${b.branch}`} className="sec" data-testid="llb-branch">
							<div className="sec-h">
								<span className="plat-pill">{appName(board, b.app)}</span>
								<span className="mono">{b.branch}</span>
								<span className="badge" data-st={st.tone}>
									{st.label}
								</span>
							</div>
							<div className="kv">
								<span>Opened</span>
								<span>
									{shortDay(b.openedOn)} · last card in {shortDay(b.lastIn)}
								</span>
								<span>Merge out</span>
								<span>
									{l ? (
										<>
											<a href={l.url} target="_blank" rel="noreferrer">
												!{l.iid}
											</a>{" "}
											into <span className="mono">{l.targetBranch}</span> · {l.state}
											{l.mergedOn ? ` ${shortDay(l.mergedOn)}` : ""}
										</>
									) : b.gone ? (
										"branch deleted"
									) : (
										"not opened"
									)}
								</span>
								{b.ahead != null && (
									<>
										<span>Ahead of develop</span>
										<span>{b.ahead} commits</span>
									</>
								)}
								{b.train && (
									<>
										<span>Rides</span>
										<span>
											<button
												type="button"
												className="lnk"
												onClick={() => actions.openSheet({ kind: "train", train: b.train ?? "" }, "push")}
											>
												Train {trainLabel(board, b.train)}
											</button>
										</span>
									</>
								)}
							</div>
							<div className="mt-2">
								{keys.map((c) => (
									<CardLine
										key={c.key}
										glyph="⑂"
										glyphTone="hotfix"
										glyphTitle="merged into the long-lived branch"
										person={c.assignee}
										k={c.key}
										url={jiraUrl(board, c.key)}
										summary={c.summary}
										status={c.status}
										statusCategory={c.statusCategory}
										onOpen={() => actions.openSheet({ kind: "card", key: c.key }, "push")}
									/>
								))}
							</div>
						</div>
					);
				})}
			</>
		),
	};
}

export function llbsSheet(
	board: Board,
	actions: PageActions,
	keep: (g: LLBGroup) => boolean,
): { title: ReactNode; body: ReactNode } {
	const groups = board.llbs.filter(keep);
	const active = (g: LLBGroup) => g.branches.some((b) => !b.landing?.mergedOn && !b.gone);
	const row = (g: LLBGroup) => {
		const chip = targetChip(g, board);
		const n = new Set(g.branches.flatMap((b) => b.cards.map((c) => c.key))).size;
		return (
			<button
				key={g.id}
				type="button"
				className="lbrow"
				onClick={() => actions.openSheet({ kind: "llb", id: g.id }, "push")}
			>
				<span className="nm2">
					<GitFork className="size-3.5 text-[var(--purple)]" aria-hidden="true" />
					{g.plan?.name ?? g.name}
				</span>
				{chip && (
					<span className={`tgt${chip.tone ? "" : " none"}`} data-st={chip.tone ?? undefined}>
						{chip.text}
					</span>
				)}
				<span className="sub">
					{g.branches.map((b) => {
						const st = branchState(g, b);
						return (
							<span key={`${b.app}:${b.branch}`} className="plat-pill c" data-st={st.tone}>
								{appName(board, b.app)} · {st.label}
							</span>
						);
					})}
					<span>
						{n} card{n === 1 ? "" : "s"}
					</span>
					<span>since {shortDay(g.branches.map((b) => b.openedOn).sort()[0] ?? board.today)}</span>
				</span>
			</button>
		);
	};
	const open = groups.filter(active);
	const done = groups.filter((g) => !active(g));
	return {
		title: (
			<>
				Long-lived branches
				<span className="badge" data-st="hotfix">
					{open.length} open
				</span>
			</>
		),
		body: (
			<>
				<div className="sec muted text-[12.5px]">
					Feature branches that collect cards over several sprints, then merge into a sprint branch. Set a target train
					on each so the page can show what is due.
				</div>
				<div className="sec">
					<div className="sec-h">
						<span className="rel-eyebrow">Open {open.length}</span>
					</div>
					{open.map(row)}
				</div>
				{done.length > 0 && (
					<div className="sec">
						<div className="sec-h">
							<span className="rel-eyebrow">Merged or deleted {done.length}</span>
						</div>
						{done.map(row)}
					</div>
				)}
			</>
		),
	};
}
