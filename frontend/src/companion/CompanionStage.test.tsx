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
		const proc = container.querySelector("[data-proc]")!;

		fireEvent.pointerEnter(proc);
		fireEvent.pointerLeave(proc);

		expect(onInteractiveChange.mock.calls).toEqual([[true], [false]]);
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
