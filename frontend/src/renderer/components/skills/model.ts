import type { components } from "../../../api/schema";
import type { Proposal } from "../../hooks/useSkills";

// What the Skills inbox shows of a proposal, derived from the API's shapes.

type DiffLine = components["schemas"]["DiffContextLineDTO"];

export type Action = Proposal["action"];

export const ACTION_LABEL: Record<Action, string> = {
	create_memory: "New memory",
	update_memory: "Memory change",
	update_skill: "Skill change",
	edit_rule_file: "Rule",
	conflict: "Conflict",
};

export const OUTCOME_LABEL: Record<string, string> = {
	merged: "from merged work",
	abandoned: "from abandoned work",
	unknown: "task ended without a merge",
	ongoing: "from a running session",
	day: "from an orchestrator day",
};

/** One file's part of AO's diff: a new memory changes its file and MEMORY.md. */
export function diffFiles(diff: string): { path: string; created: boolean; lines: DiffLine[] }[] {
	return diff
		.split(/\n(?=--- )/)
		.filter((part) => part.startsWith("--- "))
		.map((part) => {
			const plus = /\n\+\+\+ b\/?(.*)/.exec(part);
			return { path: plus ? plus[1] : "", created: part.startsWith("--- /dev/null"), lines: diffLines(part) };
		});
}

/** Turns one file of a unified diff into the rows DiffRows renders. */
export function diffLines(diff: string): DiffLine[] {
	const out: DiffLine[] = [];
	let oldLine = 0;
	let newLine = 0;
	for (const raw of diff.split("\n")) {
		if (raw.startsWith("--- ") || raw.startsWith("+++ ") || raw === "" || raw.startsWith("\\")) continue;
		const hunk = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(raw);
		if (hunk) {
			oldLine = Number(hunk[1]);
			newLine = Number(hunk[2]);
			out.push({ kind: "hunk", oldLine: 0, newLine: 0, text: raw });
			continue;
		}
		const sign = raw[0];
		const text = raw.slice(1);
		if (sign === "+") out.push({ kind: "add", oldLine: 0, newLine: newLine++, text });
		else if (sign === "-") out.push({ kind: "del", oldLine: oldLine++, newLine: 0, text });
		else out.push({ kind: "context", oldLine: oldLine++, newLine: newLine++, text });
	}
	return out;
}

/** The file name a proposal writes, for its header. */
export function fileName(target: string): string {
	const skill = /skills\/([^/]+)\/SKILL\.md$/.exec(target);
	if (skill) return `skill ${skill[1]}`;
	return target.slice(target.lastIndexOf("/") + 1);
}

/** A target path with the home directory shown as ~. */
export function tildePath(path: string): string {
	return path.replace(/^\/(?:Users|home)\/[^/]+/, "~");
}

/** Proposals in queue order: conflicts first (they need you most), then by confidence. */
export function queueOrder<T extends { action: string; confidence: number; id: number }>(ps: T[]): T[] {
	return [...ps].sort(
		(a, b) =>
			Number(b.action === "conflict") - Number(a.action === "conflict") || b.confidence - a.confidence || b.id - a.id,
	);
}

/** Days from now as an RFC 3339 instant, for a snooze. */
export function inDays(days: number, now = Date.now()): string {
	return new Date(now + days * 24 * 60 * 60 * 1000).toISOString();
}
