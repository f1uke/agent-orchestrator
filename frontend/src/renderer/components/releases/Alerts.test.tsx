import { fireEvent, render, screen, within } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { demoBoard } from "../../lib/releases/fixture";
import { makeLens } from "../../lib/releases/lens";
import type { AlertDue, AlertHit } from "../../lib/releases/types";
import { parseReleasesSearch } from "../../lib/releases/url";
import { AlertList, AlertRow, type AlertCtx } from "./Alerts";
import { ReleasesCapabilitiesProvider } from "./ReleasesCapabilities";

vi.mock("../../hooks/useReleaseBoard", () => ({
	useHideHit: () => ({ mutate: vi.fn() }),
	useUnhideHit: () => ({ mutate: vi.fn() }),
	useMarkNoMr: () => ({ mutate: vi.fn() }),
}));

const TODAY = "2026-10-14";
const JIRA_RELEASE = "https://demo.example.com/versions/2";

function ctx(over: Partial<AlertCtx> = {}, search: Record<string, unknown> = {}): AlertCtx {
	const board = demoBoard(TODAY);
	return {
		board,
		lens: makeLens(board, parseReleasesSearch(search), new Set()),
		actions: {
			setSearch: vi.fn(),
			openSheet: vi.fn(),
			closeSheet: vi.fn(),
			back: vi.fn(),
			setLayers: vi.fn(),
			setHiddenBoards: vi.fn(),
			reveal: vi.fn(),
			whereIs: () => null,
			openPart: vi.fn(),
		},
		...over,
	};
}

function hit(over: Partial<AlertHit> = {}): AlertHit {
	return {
		id: "E1~v/20-2026/di",
		rule: "E1",
		subject: { kind: "version", train: "20-2026", app: "di" },
		severity: "hygiene",
		bucket: "badge",
		app: "di",
		train: "20-2026",
		copy: {
			what: "Train 20 shipped, but its Jira release is not marked released",
			do: "Mark the Jira release released.",
		},
		cards: [],
		evidence: {},
		fix: "queue",
		primary: { label: "Mark released", fix: true },
		links: [{ label: "Open Jira release", url: JIRA_RELEASE }],
		asOf: `${TODAY}T02:00:00Z`,
		...over,
	};
}

const due = (workDays: number, over: Partial<AlertDue> = {}): AlertDue => ({
	on: "2026-11-03",
	event: "publish",
	workDays,
	...over,
});

function renderIn(ui: ReactNode, readOnly = false) {
	return render(<ReleasesCapabilitiesProvider readOnly={readOnly}>{ui}</ReleasesCapabilitiesProvider>);
}

describe("the time-left chip on an alert", () => {
	it.each([
		[-2, "2 days late", "failed"],
		[0, "Today", "failed"],
		[1, "1 day left", "failed"],
		[3, "3 days left", "waiting"],
		[8, "8 days left", "unknown"],
	])("reads %i working days as %s", (workDays, text, tone) => {
		renderIn(<AlertRow h={hit({ due: due(workDays) })} c={ctx()} />);
		const chip = screen.getByTestId("hit-due");
		expect(chip.textContent).toBe(text);
		expect(chip.getAttribute("data-st")).toBe(tone);
	});

	it("says Missed in red for a missed deadline", () => {
		renderIn(<AlertRow h={hit({ due: due(2, { missed: true }) })} c={ctx()} />);
		expect(screen.getByTestId("hit-due").textContent).toBe("Missed");
		expect(screen.getByTestId("hit-due").getAttribute("data-st")).toBe("failed");
	});

	it("draws no chip for an alert without a deadline", () => {
		renderIn(<AlertRow h={hit()} c={ctx()} />);
		expect(screen.queryByTestId("hit-due")).toBeNull();
	});
});

describe("the lines of an alert", () => {
	it("reads what is wrong, then what to do, and opens the details sheet from line 1", () => {
		const c = ctx();
		renderIn(<AlertRow h={hit()} c={c} />);
		const what = screen.getByTestId("hit-subject");
		expect(what.textContent).toBe("Train 20 shipped, but its Jira release is not marked released");
		expect(screen.getByTestId("hit-do").textContent).toBe("Mark the Jira release released.");
		fireEvent.click(what);
		expect(c.actions.openPart).toHaveBeenCalledTimes(1);
	});

	it("explains the rule in the line 1 tooltip, and why the card is shown for a person", () => {
		const board = demoBoard(TODAY);
		const c = ctx({ board, lens: makeLens(board, parseReleasesSearch({ person: "ada" }), new Set()) });
		const h = hit({ mine: [{ why: "release_mr_author", ref: "!77" }] });
		renderIn(<AlertRow h={h} c={c} />);
		const title = screen.getByTestId("hit-subject").getAttribute("title") ?? "";
		const rule = board.catalogue.find((r) => r.id === "E1")?.title;
		expect(title).toBe(`E1 · ${rule} · click for details · Shown for you: your release MR !77`);
	});

	it("carries no severity label, no rule id and no mine line on the card", () => {
		const { container } = renderIn(<AlertRow h={hit({ severity: "block" })} c={ctx()} />);
		expect(container.querySelector(".sev, .rid, .mine")).toBeNull();
		expect(screen.queryByText("Block")).toBeNull();
		expect(screen.queryByText("E1")).toBeNull();
	});

	it("lists the card keys as buttons that open the card, and skips a key line 1 already names", () => {
		const c = ctx();
		const h = hit({
			copy: { what: "DEMO-101 and 2 other cards are late", do: "Finish them." },
			cards: ["DEMO-101", "DEMO-102", "DEMO-103"],
		});
		renderIn(<AlertRow h={h} c={c} />);
		const keys = screen.getAllByTestId("hit-key");
		expect(keys.map((k) => k.textContent)).toEqual(["DEMO-102", "DEMO-103"]);
		fireEvent.click(keys[0]);
		expect(c.actions.openSheet).toHaveBeenCalledWith({ kind: "card", key: "DEMO-102" }, "push");
	});

	it("shows the card's Jira summary instead of a key line 1 already names", () => {
		renderIn(
			<AlertRow
				h={hit({ copy: { what: "DEMO-101 has no MR yet", do: "Open one." }, cards: ["DEMO-101"] })}
				c={ctx()}
			/>,
		);
		expect(screen.queryByTestId("hit-key")).toBeNull();
		expect(screen.getByTestId("hit-cards").textContent).toBe("Sign-in with a passkey");
	});

	it("shows five keys, then how many more", () => {
		const cards = ["A-1", "A-2", "A-3", "A-4", "A-5", "A-6", "A-7", "A-8"];
		renderIn(<AlertRow h={hit({ cards })} c={ctx()} />);
		expect(screen.getAllByTestId("hit-key")).toHaveLength(5);
		expect(screen.getByTestId("hit-cards").textContent).toContain("+3 more");
	});

	it("draws no cards line for an alert without cards", () => {
		renderIn(<AlertRow h={hit()} c={ctx()} />);
		expect(screen.queryByTestId("hit-cards")).toBeNull();
	});
});

describe("the actions of an alert", () => {
	it("makes a fix primary the button that opens the Fix, with the links and Show after it", () => {
		const c = ctx();
		const { container } = renderIn(<AlertRow h={hit()} c={c} />);
		const acts = container.querySelector(".acts") as HTMLElement;
		expect(Array.from(acts.children).map((e) => e.textContent)).toEqual(["Mark released", "Open Jira release", "Show"]);
		fireEvent.click(screen.getByTestId("hit-primary"));
		expect(c.actions.openSheet).toHaveBeenCalledWith({ kind: "fix", hit: "E1~v/20-2026/di" }, "push");
	});

	it("shows the state of a fix that is already queued in place of the primary", () => {
		renderIn(<AlertRow h={hit({ openFix: { id: "fx-1", state: "dry_run" } })} c={ctx()} />);
		expect(screen.queryByTestId("hit-primary")).toBeNull();
		expect(screen.getByTestId("hit-fix-state").textContent).toBe("fix recorded (dry run)");
	});

	it("makes a link primary an anchor and does not repeat it among the other links", () => {
		const h = hit({
			primary: { label: "Open release MR", url: "https://gitlab.example.com/demo/-/merge_requests/77" },
			links: [{ label: "Open pipeline", url: "https://gitlab.example.com/demo/-/pipelines/9" }],
		});
		renderIn(<AlertRow h={h} c={ctx()} />);
		const primary = screen.getByTestId("hit-primary");
		expect(primary.tagName).toBe("A");
		expect(primary.getAttribute("href")).toBe("https://gitlab.example.com/demo/-/merge_requests/77");
		expect(screen.getAllByRole("link").map((a) => a.textContent)).toEqual(["Open release MR", "Open pipeline"]);
	});

	it("hides the alert from the X button", () => {
		renderIn(<AlertRow h={hit()} c={ctx()} />);
		expect(screen.getByRole("button", { name: "Hide E1~v/20-2026/di" })).toBeTruthy();
	});

	it("on a read-only page takes the first link as the primary in place of the fix, and has no hide X", () => {
		renderIn(<AlertRow h={hit()} c={ctx()} />, true);
		const primary = screen.getByTestId("hit-primary");
		expect(primary.tagName).toBe("A");
		expect(primary.textContent).toBe("Open Jira release");
		expect(primary.getAttribute("href")).toBe(JIRA_RELEASE);
		expect(screen.queryByText("Mark released")).toBeNull();
		expect(screen.getAllByRole("link")).toHaveLength(1);
		expect(screen.queryByRole("button", { name: /^Hide / })).toBeNull();
	});

	it("keeps Show on a read-only page", () => {
		const c = ctx();
		renderIn(<AlertRow h={hit()} c={c} />, true);
		fireEvent.click(screen.getByTestId("hit-show"));
		expect(c.actions.reveal).toHaveBeenCalledTimes(1);
	});
});

describe("a list of alerts in a sheet", () => {
	const hits = [
		hit({
			id: "C5~v/21-2026/di",
			rule: "C5",
			severity: "block",
			copy: { what: "Train 21 release MR has conflicts", do: "Resolve them." },
		}),
		hit({
			id: "C1~v/21-2026/di",
			rule: "C1",
			severity: "block",
			copy: { what: "Train 21 pipeline failed", do: "Retry it." },
		}),
		hit({
			id: "A7~v/21-2026/pa",
			rule: "A7",
			severity: "data",
			copy: { what: "Train 21 dates differ", do: "Move one." },
		}),
		hit({ id: "E1~v/20-2026/di", rule: "E1", severity: "hygiene" }),
	];

	it("groups its alerts under one header per severity present, in severity order", () => {
		renderIn(<AlertList title="Needs attention" hits={hits} c={ctx()} />);
		const headers = screen.getAllByTestId("sev-header");
		expect(headers.map((h) => h.firstChild?.textContent)).toEqual([
			"Blocks the release",
			"Systems disagree",
			"Clean up",
		]);
		expect(headers.map((h) => h.querySelector(".n")?.textContent)).toEqual(["2", "1", "1"]);
		const groups = screen.getAllByTestId("sev-group");
		expect(
			within(groups[0])
				.getAllByTestId("hit-subject")
				.map((s) => s.textContent),
		).toEqual(["Train 21 release MR has conflicts", "Train 21 pipeline failed"]);
	});

	it("puts no severity label on any card", () => {
		const { container } = renderIn(<AlertList title="Needs attention" hits={hits} c={ctx()} />);
		expect(container.querySelector(".al .sev")).toBeNull();
		expect(screen.queryByText("Block")).toBeNull();
		expect(screen.queryByText("Data")).toBeNull();
	});

	it("draws nothing for an empty list", () => {
		const { container } = renderIn(<AlertList title="Needs attention" hits={[]} c={ctx()} />);
		expect(container.textContent).toBe("");
	});
});
