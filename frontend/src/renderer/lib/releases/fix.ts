import { apiErrorMessage } from "../api-client";
import type { AlertHit, Fix, FixPlan, FixRef, FixState, FixStep, ShownRequest } from "./types";
import type { Tone } from "./vocab";

export const FIX_STATE_META: Readonly<Record<FixState, { text: string; tone: Tone }>> = {
	dry_run: { text: "fix recorded (dry run)", tone: "running" },
	applying: { text: "applying", tone: "running" },
	applied: { text: "applied", tone: "done" },
	failed: { text: "apply failed", tone: "failed" },
	undone: { text: "undone", tone: "todo" },
	withdrawn: { text: "withdrawn", tone: "todo" },
	superseded: { text: "superseded: the alert went away", tone: "todo" },
};

export function fixStateText(s: FixState): string {
	return FIX_STATE_META[s].text;
}

export type FixAction = { kind: "fix" } | { kind: "open"; ref: FixRef };

export function fixAction(h: AlertHit, hidden: boolean): FixAction | null {
	if (hidden) return null;
	if (h.openFix) return { kind: "open", ref: h.openFix };
	return h.fix ? { kind: "fix" } : null;
}

export type PrimaryAction =
	| { kind: "details"; label: string }
	| { kind: "fix"; label: string }
	| { kind: "state"; ref: FixRef }
	| { kind: "link"; label: string; url: string };

/**
 * An action does one thing: open a link, open the Fix, or open the hit's details sheet.
 * A fix primary falls back to the hit's first link where the Fix is unavailable.
 */
export function primaryAction(h: AlertHit, hidden: boolean, readOnly: boolean): PrimaryAction | null {
	const p = h.primary;
	if (!p) return null;
	if (p.details) return { kind: "details", label: p.label };
	if (!p.fix) return p.url ? { kind: "link", label: p.label, url: p.url } : null;
	const a = readOnly ? null : fixAction(h, hidden);
	if (a) return a.kind === "fix" ? { kind: "fix", label: p.label } : { kind: "state", ref: a.ref };
	const first = h.links.find((l) => l.url);
	return first?.url ? { kind: "link", label: first.label, url: first.url } : null;
}

export function requestText(r: ShownRequest): string {
	const head = `${r.method} ${r.url}`;
	if (r.body === undefined || r.body === null) return head;
	return `${head}\n${typeof r.body === "string" ? r.body : JSON.stringify(r.body, null, 2)}`;
}

export function stepNote(s: FixStep): string {
	switch (s.effect.kind) {
		case "ao_plan_target":
			return "applies now to AO's own plan; nothing goes to GitLab, Jira or Confluence";
		case "copy_text":
			return "nothing is sent: paste it by hand";
		default:
			return "dry run: nothing is sent";
	}
}

export type PlanView = { steps: FixStep[]; confirm: string | null; copy: string | null };

export function planView(p: FixPlan): PlanView {
	const copy =
		p.mode === "copy"
			? p.steps.flatMap((s) => (s.effect.kind === "copy_text" ? [s.effect.text] : [])).join("\n")
			: null;
	const recordable = p.steps.length > 0 && (p.mode === "apply" || p.mode === "queue" || p.mode === "choose");
	const confirm = !recordable ? null : p.mode === "apply" ? "Apply" : "Record as a dry run";
	return { steps: p.steps, confirm, copy };
}

export function undoVerb(s: FixState): "Withdraw" | "Undo" | null {
	return s === "dry_run" ? "Withdraw" : s === "applied" ? "Undo" : null;
}

const VIA: Readonly<Record<string, string>> = { app: "the app", cli: "the CLI", api: "an API call" };

export function actorLabel(actor: string): string {
	const at = actor.indexOf(":");
	const via = at < 0 ? actor : actor.slice(0, at);
	const name = VIA[via];
	if (!name) return actor;
	return at < 0 ? name : `${name} (${actor.slice(at + 1)})`;
}

export function newestFirst(fixes: readonly Fix[]): Fix[] {
	return [...fixes].sort((a, b) => b.createdAt.localeCompare(a.createdAt));
}

export class FixPlanChangedError extends Error {
	constructor(readonly plan: FixPlan) {
		super("The data changed. Review the new plan again.");
		this.name = "FixPlanChangedError";
	}
}

type ApiError = { code?: string; message?: string; details?: { plan?: FixPlan } };

export function fixFailure(status: number, error: unknown): Error {
	const e = error as ApiError | undefined;
	if (status === 409 && e?.code === "FIX_PLAN_CHANGED" && e.details?.plan)
		return new FixPlanChangedError(e.details.plan);
	return new Error(apiErrorMessage(error, `fix request ${status}`));
}
