import { describe, expect, it, vi, afterEach } from "vitest";
import { act, cleanup, fireEvent, render } from "@testing-library/react";
import type { CompanionActivity, CompanionFeed } from "./feed";
import { CompanionStage } from "./CompanionStage";

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
		push(AMBLING);

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
		push(AMBLING);

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
