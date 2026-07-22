import { describe, expect, it, vi, afterEach } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { CompanionActivity, CompanionFeed } from "./feed";
import { CompanionStage } from "./CompanionStage";
import { createManualFeed } from "./dev-feed";

afterEach(() => {
	cleanup();
	vi.useRealTimers();
	vi.unstubAllGlobals();
});

/** A feed we drive by hand, so a test never waits on the mock's clock. */
function stubFeed() {
	let listener: ((activities: CompanionActivity[]) => void) | null = null;
	let unsubscribed = 0;
	const feed: CompanionFeed = {
		subscribe(next) {
			listener = next;
			next([]);
			return () => {
				unsubscribed += 1;
				listener = null;
			};
		},
	};
	return {
		feed,
		push: (activities: CompanionActivity[]) => act(() => listener?.(activities)),
		unsubscribed: () => unsubscribed,
	};
}

function prefersReducedMotion(reduce: boolean) {
	vi.stubGlobal("matchMedia", (query: string) => ({
		matches: reduce && query.includes("reduced-motion"),
		media: query,
		onchange: null,
		addEventListener: () => undefined,
		removeEventListener: () => undefined,
		addListener: () => undefined,
		removeListener: () => undefined,
		dispatchEvent: () => false,
	}));
}

function walkers(container: HTMLElement): number {
	return [...container.querySelectorAll("[data-walk-strip]")].filter((el) =>
		(el.getAttribute("style") ?? "").includes("steps"),
	).length;
}

const AMBLING: CompanionActivity[] = [
	{ sessionId: "a", status: "pr_open" },
	{ sessionId: "b", status: "draft" },
	{ sessionId: "c", status: "review_pending" },
];

// Crowding means a stroll is declined when its target is too near another Proc, so
// a roster of three in a jsdom-sized band makes "did anything walk?" a coin flip.
// The walking assertions use a lone Proc, which has nobody to be blocked by.
const LONE_AMBLER: CompanionActivity[] = [{ sessionId: "solo", status: "pr_open" }];

describe("CompanionStage", () => {
	it("shows one Proc per session in the feed", () => {
		const { feed, push } = stubFeed();
		const { container } = render(<CompanionStage feed={feed} />);

		push([
			{ sessionId: "a", status: "working" },
			{ sessionId: "b", status: "idle" },
		]);

		expect(container.querySelectorAll("[data-proc]")).toHaveLength(2);
	});

	it("removes a Proc when its session leaves the feed", () => {
		const { feed, push } = stubFeed();
		const { container } = render(<CompanionStage feed={feed} />);

		push([
			{ sessionId: "a", status: "working" },
			{ sessionId: "b", status: "idle" },
		]);
		push([{ sessionId: "b", status: "idle" }]);

		expect(container.querySelectorAll("[data-proc]")).toHaveLength(1);
	});

	it("places each Proc along the band with a composited transform", () => {
		const { feed, push } = stubFeed();
		const { container } = render(<CompanionStage feed={feed} />);

		push([{ sessionId: "a", status: "working" }]);
		const style = container.querySelector<HTMLElement>("[data-proc]")?.getAttribute("style") ?? "";

		expect(style).toMatch(/translate3d\(-?\d+(\.\d+)?px, 0(px)?, 0(px)?\)/);
	});

	it("takes the pointer only while it is over a Proc", () => {
		const onInteractiveChange = vi.fn();
		const { feed, push } = stubFeed();
		const { container } = render(<CompanionStage feed={feed} onInteractiveChange={onInteractiveChange} />);
		push([{ sessionId: "a", status: "working" }]);
		const figure = container.querySelector("[data-figure] rect")!;
		const stage = container.querySelector(".companion-stage")!;

		fireEvent.pointerMove(figure, { bubbles: true });
		fireEvent.pointerMove(stage, { bubbles: true });

		expect(onInteractiveChange.mock.calls).toEqual([[true], [false]]);
	});

	it("lets a click on a Proc's empty FRAME fall through to the desktop", () => {
		// The reported bug: clicking the band where no Proc is drawn did not pass
		// through. Each Proc's wrapper is the whole ~150px drawn frame — figure plus
		// the scenery either side — and it was taking the pointer for all of it.
		const onInteractiveChange = vi.fn();
		const { feed, push } = stubFeed();
		const { container } = render(<CompanionStage feed={feed} onInteractiveChange={onInteractiveChange} />);
		push([{ sessionId: "a", status: "working" }]);

		fireEvent.pointerMove(container.querySelector("[data-proc]")!, { bubbles: true });

		expect(onInteractiveChange).not.toHaveBeenCalled();
	});

	it("lets a click on a Proc's scenery fall through — a desk is not a pet", () => {
		const onInteractiveChange = vi.fn();
		const { feed, push } = stubFeed();
		const { container } = render(<CompanionStage feed={feed} onInteractiveChange={onInteractiveChange} />);
		push([{ sessionId: "a", status: "working" }]);

		fireEvent.pointerMove(container.querySelector('[data-slot="ground"] rect')!, { bubbles: true });

		expect(onInteractiveChange).not.toHaveBeenCalled();
	});

	it("hands the pointer back when it leaves the overlay without crossing off a Proc", () => {
		const onInteractiveChange = vi.fn();
		const { feed, push } = stubFeed();
		const { container } = render(<CompanionStage feed={feed} onInteractiveChange={onInteractiveChange} />);
		push([{ sessionId: "a", status: "working" }]);

		fireEvent.pointerMove(container.querySelector("[data-figure] rect")!, { bubbles: true });
		fireEvent.blur(window);

		expect(onInteractiveChange.mock.calls).toEqual([[true], [false]]);
	});

	it("keeps parked Procs off each other", () => {
		vi.useFakeTimers();
		const { feed, push } = stubFeed();
		const { container } = render(<CompanionStage feed={feed} />);
		push(["a", "b", "c", "d", "e"].map((id) => ({ sessionId: id, status: "working" as const })));

		act(() => vi.advanceTimersByTime(5_000));

		const xs = [...container.querySelectorAll<HTMLElement>("[data-proc]")]
			.map((el) => Number(/translate3d\((-?[\d.]+)px/.exec(el.getAttribute("style") ?? "")?.[1] ?? 0))
			.sort((a, b) => a - b);
		for (let i = 1; i < xs.length; i++) {
			expect(xs[i] - xs[i - 1]).toBeGreaterThan(40);
		}
	});

	it("strolls when motion is allowed", () => {
		vi.useFakeTimers();
		prefersReducedMotion(false);
		const { feed, push } = stubFeed();
		const { container } = render(<CompanionStage feed={feed} />);
		push(LONE_AMBLER);

		act(() => vi.advanceTimersByTime(400_000));

		expect(walkers(container)).toBeGreaterThan(0);
	});

	it("stands every Proc still under prefers-reduced-motion", () => {
		vi.useFakeTimers();
		prefersReducedMotion(true);
		const { feed, push } = stubFeed();
		const { container } = render(<CompanionStage feed={feed} />);
		push(AMBLING);

		act(() => vi.advanceTimersByTime(400_000));

		expect(walkers(container)).toBe(0);
		expect(container.querySelectorAll("[data-proc]")).toHaveLength(AMBLING.length);
	});

	it("parks while the overlay is hidden, so an occluded desktop costs nothing", () => {
		vi.useFakeTimers();
		prefersReducedMotion(false);
		const { feed, push } = stubFeed();
		const { container } = render(<CompanionStage feed={feed} />);
		push(LONE_AMBLER);

		act(() => {
			Object.defineProperty(document, "visibilityState", { configurable: true, value: "hidden" });
			document.dispatchEvent(new Event("visibilitychange"));
			vi.advanceTimersByTime(400_000);
		});

		expect(walkers(container)).toBe(0);

		act(() => {
			Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" });
			document.dispatchEvent(new Event("visibilitychange"));
			vi.advanceTimersByTime(400_000);
		});

		expect(walkers(container)).toBeGreaterThan(0);
	});

	it("lets go of the feed when it unmounts", () => {
		const { feed, unsubscribed } = stubFeed();
		const view = render(<CompanionStage feed={feed} />);

		view.unmount();

		expect(unsubscribed()).toBe(1);
	});
});

describe("dragging and naming", () => {
	function pushNamed() {
		const { feed, push } = stubFeed();
		const view = render(<CompanionStage feed={feed} />);
		push([{ sessionId: "a", status: "pr_open", name: "fix the flaky test", project: "agent-orchestrator" }]);
		return { ...view, push };
	}

	it("shows each Proc's board name under it", () => {
		const { container } = pushNamed();

		expect(container.querySelector("[data-name-tag]")?.textContent).toBe("fix the flaky test");
	});

	it("picks a Proc up on press and puts it down on release", () => {
		const { container } = pushNamed();
		// Re-queried every time: picking a Proc up swaps its pose, so React replaces
		// the node and the one grabbed a moment ago is no longer in the document.
		const figure = () => container.querySelector("[data-figure] rect")!;

		fireEvent.pointerDown(figure(), { bubbles: true, clientX: 400 });
		expect(container.querySelector("[data-teased]")).not.toBeNull();

		fireEvent.pointerUp(figure(), { bubbles: true, clientX: 400 });
		expect(container.querySelector("[data-teased]")).toBeNull();
	});

	it("carries the Proc with the pointer while it is held", () => {
		const { container } = pushNamed();
		const figure = () => container.querySelector("[data-figure] rect")!;
		const at = () =>
			Number(
				/translate3d\((-?[\d.]+)px/.exec(container.querySelector("[data-proc]")!.getAttribute("style") ?? "")?.[1],
			);

		const before = at();
		fireEvent.pointerDown(figure(), { bubbles: true, clientX: 400 });
		fireEvent.pointerMove(figure(), { bubbles: true, clientX: 520 });

		expect(at()).toBeCloseTo(before + 120, 0);
	});

	it("keeps the pointer for the whole drag, even once it slips off the Proc", () => {
		// A drag pulls the pointer off constantly. Reverting to click-through mid-drag
		// would hand the rest of the gesture to the desktop.
		const onInteractiveChange = vi.fn();
		const { feed, push } = stubFeed();
		const { container } = render(<CompanionStage feed={feed} onInteractiveChange={onInteractiveChange} />);
		push([{ sessionId: "a", status: "pr_open", name: "n", project: "p" }]);
		const figure = container.querySelector("[data-figure] rect")!;

		fireEvent.pointerDown(figure, { bubbles: true, clientX: 400 });
		onInteractiveChange.mockClear();

		fireEvent.pointerMove(container.querySelector(".companion-stage")!, { bubbles: true, clientX: 520 });

		expect(onInteractiveChange).not.toHaveBeenCalledWith(false);
	});

	it("opens a tooltip only after the pointer has rested, and closes it when it leaves", () => {
		vi.useFakeTimers();
		const { feed, push } = stubFeed();
		const { container } = render(<CompanionStage feed={feed} />);
		push([{ sessionId: "a", status: "working", name: "fix the flaky test", project: "agent-orchestrator" }]);
		const figure = container.querySelector("[data-figure] rect")!;

		fireEvent.pointerMove(figure, { bubbles: true });
		act(() => vi.advanceTimersByTime(400));
		expect(container.querySelector("[data-tooltip]")).toBeNull();

		act(() => vi.advanceTimersByTime(1_200));
		expect(container.querySelector("[data-tooltip]")?.textContent).toContain("agent-orchestrator");

		fireEvent.pointerMove(container.querySelector(".companion-stage")!, { bubbles: true });
		act(() => vi.advanceTimersByTime(400));
		expect(container.querySelector("[data-tooltip]")).toBeNull();
	});
});

describe("while two Procs are talking", () => {
	// The listener said "…", which read as the message having been truncated away
	// to dots rather than as somebody listening. It says nothing at all now: it has
	// not spoken, and silence is the honest picture of that.
	it("gives the card to the one that is speaking and none to the one being told", async () => {
		const feed = createManualFeed([
			{ sessionId: "demo-app-1", status: "pr_open", name: "one" },
			{ sessionId: "demo-app-2", status: "pr_open", name: "two" },
		]);
		render(<CompanionStage feed={feed} bubbleFor={(id) => feed.bubbleFor(id)} reducedMotion />);
		await act(async () => {
			feed.push({
				sessionId: "demo-app-2",
				kind: "message",
				at: new Date().toISOString(),
				text: "[from @demo-app-1] P1 is fixed",
				ttlMs: 12_000,
			} as never);
			await Promise.resolve();
		});

		await waitFor(() => expect(screen.getAllByText("P1 is fixed")).toHaveLength(1));
		expect(screen.queryByText("…")).toBeNull();
	});
});

describe("a bubble travels with the Proc that is saying it", () => {
	// It used to be mounted only when there was something to say — so it appeared
	// already at the DESTINATION of a walk that was still in progress, with no
	// previous transform to animate from, and hung in the air while its Proc ran to
	// catch up. The wrapper is always there; only its contents come and go.
	it("keeps a wrapper over every Proc, whether or not it is speaking", async () => {
		const feed = createManualFeed([
			{ sessionId: "demo-app-1", status: "pr_open", name: "one" },
			{ sessionId: "demo-app-2", status: "pr_open", name: "two" },
		]);
		const { container } = render(<CompanionStage feed={feed} bubbleFor={(id) => feed.bubbleFor(id)} />);

		await waitFor(() => expect(container.querySelectorAll("[data-proc]")).toHaveLength(2));
		expect(container.querySelectorAll(".companion-proc-chrome")).toHaveLength(2);
		expect(container.querySelectorAll("[data-bubble]")).toHaveLength(0);
	});
});
