import { approvals, nowStage, owner, unknownText, versionOf, type Lens } from "../../lib/releases/lens";
import type { BoardApp, Stage } from "../../lib/releases/types";
import { SEVERITY_META, STAGE_META, STATUS_TEXT, stageTone } from "../../lib/releases/vocab";
import { versionMineText } from "../../lib/releases/sprint";
import type { PageActions } from "./ReleasesPage";

export function Stepper({ stages, big = false }: { stages: Stage[]; big?: boolean }) {
	return (
		<span className={`rel-step${big ? " big" : ""}`}>
			{stages.map((s, i) => (
				<span key={s.key} className="contents">
					{i > 0 && <u />}
					<i data-st={stageTone(s.status)} title={`${STAGE_META[s.key].label}: ${STATUS_TEXT[s.status]}`} />
				</span>
			))}
		</span>
	);
}

function MineLine({ app, lens }: { app: string; lens: Lens }) {
	const t = lens.focus;
	const v = t ? versionOf(t, app) : undefined;
	const units = (v?.cards ?? []).filter((u) => u.mine?.length);
	const bits = [
		v ? versionMineText(v, lens.identity) : "",
		units.length ? `${units.length} of my card${units.length === 1 ? "" : "s"}` : "",
	].filter(Boolean);
	return bits.length ? (
		<div className="meline" data-testid="label-mine">
			{bits.join(" · ")}
		</div>
	) : (
		<div className="meline off" data-testid="label-mine">
			Nothing of mine in {t?.label ?? "this train"}
		</div>
	);
}

export function StickyAppLabel({ app, lens, actions }: { app: BoardApp; lens: Lens; actions: PageActions }) {
	const t = lens.focus;
	const v = t ? versionOf(t, app.id) : undefined;
	const hits = lens.appHits(app.id);
	const top = hits[0];
	const family = app.name.replace(/\s*(iOS|Android)\s*$/i, "").trim() || app.name;
	const head = (
		<div className="nm">
			{family}
			<span className="plat">{app.platform === "ios" ? "iOS" : "Android"}</span>
			{hits.length > 0 && top && (
				<span
					className="flag"
					data-st={SEVERITY_META[top.severity].tone}
					title={`${hits.length} alert(s) on ${app.name} in ${t?.label}`}
				>
					{hits.length}
				</span>
			)}
		</div>
	);
	if (!t || !v) {
		return (
			<div className="rel-lab" data-testid={`label-${app.id}`}>
				{head}
				<div className="passive text-[12px]">Not in train {t?.label ?? ""}</div>
			</div>
		);
	}
	const now = nowStage(v.stages);
	const own = owner(v);
	const ap = approvals(v);
	const merged = v.cards.filter((c) => c.code.kind === "merged").length;
	const nowAt = Date.now();
	return (
		<button
			type="button"
			className="rel-lab"
			data-testid={`label-${app.id}`}
			onClick={() => actions.openSheet({ kind: "version", train: t.id, app: app.id })}
		>
			{head}
			<div className="ver">
				<span className="mono">{v.version}</span>
				<span className="passive mono">{t.label}</span>
				<Stepper stages={v.stages} />
			</div>
			<div data-testid="label-stage">
				{now ? (
					<span className="now" data-st={stageTone(now.status)}>
						{STAGE_META[now.key].short} ·{" "}
						{now.status === "unknown" ? unknownText(now.missing, nowAt) : STATUS_TEXT[now.status]}
					</span>
				) : (
					<span className="now" data-st="done">
						shipped
					</span>
				)}
			</div>
			<div className="why" data-testid="label-blocker" data-st={top ? SEVERITY_META[top.severity].tone : undefined}>
				{top ? (
					<>
						<b>{SEVERITY_META[top.severity].label}</b> {top.text}
					</>
				) : now?.detail ? (
					now.detail
				) : (
					<span className="passive">Nothing blocking</span>
				)}
			</div>
			{lens.me ? (
				<MineLine app={app.id} lens={lens} />
			) : (
				<div className="meta">
					{v.cards.length > 0 && (
						<span>
							<b>
								{merged}/{v.cards.length}
							</b>{" "}
							merged
						</span>
					)}
					<span>
						<b>
							{ap.ok}/{ap.total}
						</b>{" "}
						approved
					</span>
					{own && <span title={`${own.name} (${own.from})`}>{own.name.split(" ")[0]}</span>}
				</div>
			)}
		</button>
	);
}
