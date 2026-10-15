import { dayDiff, shortDay } from "./day";
import type { AppVersion, Day, Hotfix } from "./types";
import { STAGE_META, type Tone } from "./vocab";

export type BarPart = { id: "mr" | "next" | "tag" | "title"; text: string; tone?: Tone; emphasis?: string };

function mrPart(v: AppVersion): BarPart {
	const r = v.releaseMr;
	if (!r) return { id: "mr", text: "no release MR", tone: "passive" };
	const n = `!${r.mr.iid}`;
	if (r.mr.state === "merged") return { id: "mr", text: `${n} merged`, tone: "done" };
	if (r.detail?.conflicts) return { id: "mr", text: `${n} conflicts`, tone: "failed" };
	const pipeline = r.detail?.pipeline?.status;
	if (pipeline === "failed") return { id: "mr", text: `${n} pipeline failed`, tone: "failed" };
	if (pipeline === "running") return { id: "mr", text: `${n} pipeline running`, tone: "running" };
	return { id: "mr", text: `${n} open`, tone: "running" };
}

function nextPart(v: AppVersion, today: Day): BarPart {
	const open = v.stages.filter((s) => s.date && s.status !== "done" && s.status !== "not_observable");
	const next = open.find((s) => (s.date ?? "") >= today) ?? open[0];
	if (next?.date) {
		const d = dayDiff(today, next.date);
		const when = d === 0 ? "today" : d > 0 ? `in ${d}d` : `${-d}d late`;
		const name = STAGE_META[next.key].short;
		return { id: "next", text: `next ${name} ${when}`, emphasis: name };
	}
	const publish = v.milestone.due;
	if (!publish) return { id: "next", text: "published", tone: "done" };
	return {
		id: "next",
		text: publish <= today ? `published ${dayDiff(publish, today)}d ago` : `published ${shortDay(publish)}`,
		tone: "done",
	};
}

function tagPart(name: string | undefined): BarPart {
	return name ? { id: "tag", text: `tag ${name}`, tone: "done" } : { id: "tag", text: "no tag yet", tone: "passive" };
}

export function barLine(v: AppVersion, today: Day): BarPart[] {
	return [mrPart(v), nextPart(v, today), tagPart(v.tag?.name)];
}

export function hotfixLine(h: Hotfix): BarPart[] {
	return [tagPart(h.tag?.name), { id: "title", text: h.milestone.title }];
}
