import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { SessionStatus, WorkspaceSession } from "../types/workspace";
import { statusGlyph, statusLabel } from "./status-glyph";

// Every status the daemon can derive. Kept as a literal list on purpose: if a new
// status is added to the union, TypeScript fails this file until it is listed
// here, so a status can never quietly ship with no glyph and no label.
const ALL_STATUSES: SessionStatus[] = [
	"todo",
	"working",
	"pr_open",
	"draft",
	"ci_failed",
	"review_pending",
	"changes_requested",
	"approved",
	"mergeable",
	"merged",
	"needs_input",
	"no_signal",
	"idle",
	"terminated",
	"unknown",
];

// The four statuses the retired coral left-bar collapsed into a single mark.
const NEEDS_YOU: SessionStatus[] = ["needs_input", "no_signal", "ci_failed", "changes_requested"];

function session(status: SessionStatus, overrides: Partial<WorkspaceSession> = {}): WorkspaceSession {
	return {
		id: `sess-${status}`,
		workspaceId: "proj-1",
		workspaceName: "my-app",
		title: `a task that is ${status}`,
		provider: "claude-code",
		kind: "worker",
		branch: `ao/${status}`,
		status,
		updatedAt: "2026-06-10T00:00:00Z",
		prs: [],
		...overrides,
	};
}

describe("statusGlyph", () => {
	it("gives every status a glyph that really draws, and a non-empty label", () => {
		for (const status of ALL_STATUSES) {
			const glyph = statusGlyph(session(status));
			// Render it: a glyph that does not paint an <svg> is not a glyph, and
			// jsdom would happily let a broken one sit in the tree unnoticed.
			expect(renderToStaticMarkup(createElement(glyph.Icon)), `${status} draws nothing`).toContain("<svg");
			expect(glyph.label, `${status} has no label`).not.toBe("");
			expect(glyph.lane.dotVar, `${status} has no lane hue`).toMatch(/^var\(--lane-/);
		}
	});

	it("distinguishes the four NEEDS YOU statuses by SHAPE, not only by colour", () => {
		const glyphs = NEEDS_YOU.map((status) => statusGlyph(session(status)));

		// They all share one lane hue — that is exactly why the old single coral bar
		// could not tell them apart...
		const hues = new Set(glyphs.map((g) => g.lane.dotVar));
		expect(hues.size).toBe(1);

		// ...so the shape must carry the difference, on its own. Compare the drawn
		// paths, not the component identities: two different components that happen
		// to render the same silhouette would still be indistinguishable on screen.
		const drawn = glyphs.map((g) => renderToStaticMarkup(createElement(g.Icon)));
		expect(new Set(drawn).size).toBe(NEEDS_YOU.length);
	});

	it("distinguishes the four NEEDS YOU statuses by TEXT too, so colour is never the only channel", () => {
		const labels = NEEDS_YOU.map((status) => statusLabel(session(status)));
		expect(new Set(labels).size).toBe(NEEDS_YOU.length);
		expect(labels).toEqual(["Input needed", "No signal", "CI failed", "Changes requested"]);
	});

	it("fills only a genuinely live agent, so the heaviest mark means 'running'", () => {
		const filled = ALL_STATUSES.filter((status) => statusGlyph(session(status)).filled);
		expect(filled).toEqual(["working", "idle"]);
	});

	it("labels a merge request MR and a pull request PR", () => {
		const gitlab = session("pr_open", {
			prs: [{ url: "https://gitlab.com/acme/app/-/merge_requests/7", number: 7, state: "open" }],
		} as Partial<WorkspaceSession>);
		const github = session("pr_open", {
			prs: [{ url: "https://github.com/acme/app/pull/7", number: 7, state: "open" }],
		} as Partial<WorkspaceSession>);

		expect(statusLabel(gitlab)).toBe("MR open");
		expect(statusLabel(github)).toBe("PR open");
	});

	it("sorts each status into the lane its column expects", () => {
		const laneOf = (status: SessionStatus) => statusGlyph(session(status)).lane.key;

		expect(laneOf("todo")).toBe("todo");
		expect(laneOf("working")).toBe("working");
		expect(laneOf("idle")).toBe("working");
		for (const status of NEEDS_YOU) expect(laneOf(status), status).toBe("action");
		expect(laneOf("review_pending")).toBe("pending");
		expect(laneOf("pr_open")).toBe("pending");
		expect(laneOf("draft")).toBe("pending");
		expect(laneOf("unknown")).toBe("pending");
		expect(laneOf("approved")).toBe("merge");
		expect(laneOf("mergeable")).toBe("merge");
	});
});
