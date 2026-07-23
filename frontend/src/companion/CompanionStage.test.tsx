import { describe, expect, it, vi, afterEach } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { CompanionActivity, CompanionFeed } from "./feed";
import { castForSession, withSpecies } from "./cast";
import { MEET_RUN_MAX_MS } from "./behaviour";
import { CompanionStage, POINTER_REVALIDATE_MS } from "./CompanionStage";
import { createManualFeed } from "./dev-feed";
import { LOOKS_STORAGE_KEY, serializeProjectLooks } from "./look-store";
import { PORTAL_OUT_MS, PORTAL_REDUCED_MS } from "./portal-transit";
import { storeProjectSpecies } from "./look-store-live";
import { speciesForProject } from "./species";

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
			// NOT an empty snapshot first. The real feed's first listener is called with
			// the roster it actually fetched (`live-feed.ts` starts the poll and publishes
			// once), and that first snapshot is the BASELINE — the sessions that were
			// already running. Handing the stage an empty one and the roster second says
			// something different and untrue: that every session on the machine started
			// the moment the overlay opened, which is a screenful of portals.
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

	it("keeps a Proc on the band while it leaves through its portal, and removes it after", () => {
		vi.useFakeTimers();
		const { feed, push } = stubFeed();
		const { container } = render(<CompanionStage feed={feed} />);

		push([
			{ sessionId: "a", status: "working" },
			{ sessionId: "b", status: "idle" },
		]);
		push([{ sessionId: "b", status: "idle" }]);

		// Still two. The one whose session ended is on its way out, not gone — that
		// instant vanish is the whole thing the portal replaces.
		expect(container.querySelectorAll("[data-proc]")).toHaveLength(2);
		expect(container.querySelector('[data-session="a"] .companion-proc-portal')).not.toBeNull();

		act(() => void vi.advanceTimersByTime(PORTAL_OUT_MS + 1_000));

		expect(container.querySelectorAll("[data-proc]")).toHaveLength(1);
	});

	// The correctness crux. An overlay that reloads, or one that swaps its mock cast for
	// the real roster, must not celebrate a spawn for every session already running.
	it("does not portal in the roster it starts with — only sessions that appear later", () => {
		const { feed, push } = stubFeed();
		const { container } = render(<CompanionStage feed={feed} />);

		push([
			{ sessionId: "a", status: "working" },
			{ sessionId: "b", status: "idle" },
		]);

		expect(container.querySelectorAll(".companion-proc-portal")).toHaveLength(0);

		push([
			{ sessionId: "a", status: "working" },
			{ sessionId: "b", status: "idle" },
			{ sessionId: "c", status: "working" },
		]);

		expect(container.querySelectorAll(".companion-proc-portal")).toHaveLength(1);
		expect(container.querySelector('[data-session="c"] .companion-proc-portal')).not.toBeNull();
	});

	// What the human actually saw on the live overlay, which the lab never showed because
	// the lab never runs more sessions than the band can hold: spawning a worker drew TWO
	// portals — the right one at the new Proc, and a second one standing on its own with
	// no pet in it at all. The second was a session the cap had shoved off the band being
	// seen out as though it had ended; by the time you look, the Proc it is closing over
	// has already gone into it, so what is left is a ring over an empty spot.
	it("draws ONE portal for a spawn on a full band, on the new Proc and nowhere else", () => {
		const { feed, push } = stubFeed();
		const { container } = render(<CompanionStage feed={feed} />);
		const full = Array.from({ length: 14 }, (_, i) => ({ sessionId: `s${i}`, status: "pr_open" as const }));

		push(full);
		expect(container.querySelectorAll(".companion-proc-portal")).toHaveLength(0);

		push([...full, { sessionId: "spawned", status: "todo" }]);

		const portals = container.querySelectorAll(".companion-proc-portal");
		expect(portals).toHaveLength(1);
		const proc = portals[0].closest<HTMLElement>("[data-proc]");
		expect(proc?.dataset.session).toBe("spawned");
		// And it is drawn where its Proc actually stands, not at the band's origin.
		expect(proc?.style.transform).toMatch(/^translate3d\((?!0px)/);
	});

	it("does not portal anything when the same roster is polled again", () => {
		const { feed, push } = stubFeed();
		const { container } = render(<CompanionStage feed={feed} />);

		push([{ sessionId: "a", status: "working" }]);
		push([{ sessionId: "a", status: "working" }]);
		push([{ sessionId: "a", status: "needs_input" }]);

		expect(container.querySelectorAll(".companion-proc-portal")).toHaveLength(0);
	});

	// The mock cast is swapped for the live one by handing the stage a different feed.
	// That is a new feed's FIRST snapshot, not a dozen sessions starting at once.
	it("treats a new feed's first snapshot as a baseline, not as a dozen spawns", () => {
		const first = stubFeed();
		const { container, rerender } = render(<CompanionStage feed={first.feed} />);
		first.push([{ sessionId: "mock-1", status: "working" }]);

		const second = stubFeed();
		rerender(<CompanionStage feed={second.feed} />);
		second.push([
			{ sessionId: "real-1", status: "working" },
			{ sessionId: "real-2", status: "idle" },
		]);

		expect(container.querySelectorAll("[data-proc]")).toHaveLength(2);
		expect(container.querySelectorAll(".companion-proc-portal")).toHaveLength(0);
	});

	// The portal is a lifecycle flourish, not a status channel. A pet on its way out
	// narrating something would be the animation inventing a claim about a session that
	// has already ended — the one thing the bubbles' whole TTL model exists to prevent.
	it("says nothing while a pet is mid-portal", () => {
		const { feed, push } = stubFeed();
		const bubbleFor = () => ({ text: "Running the test suite", tone: "normal" as const, decay: "fresh" as const });
		const { container } = render(<CompanionStage feed={feed} bubbleFor={bubbleFor} />);

		push([
			{ sessionId: "a", status: "working" },
			{ sessionId: "b", status: "working" },
		]);
		expect(container.querySelectorAll(".companion-proc-bubble")).toHaveLength(2);

		push([{ sessionId: "b", status: "working" }]);

		expect(container.querySelectorAll(".companion-proc-bubble")).toHaveLength(1);
		expect(container.querySelector('[data-session="a"] .companion-proc-portal')).not.toBeNull();
	});

	it("cannot be picked up while it is leaving", () => {
		const { feed, push } = stubFeed();
		const { container } = render(<CompanionStage feed={feed} />);
		push([{ sessionId: "a", status: "pr_open" }]);
		push([]);

		fireEvent.pointerDown(container.querySelector("[data-figure] rect")!, { bubbles: true, clientX: 400 });

		expect(container.querySelector("[data-teased]")).toBeNull();
	});

	it("keeps the portal under reduced motion, and fades the pet through it instead", () => {
		prefersReducedMotion(true);
		const { feed, push } = stubFeed();
		const { container } = render(<CompanionStage feed={feed} />);

		push([{ sessionId: "a", status: "working" }]);
		push([
			{ sessionId: "a", status: "working" },
			{ sessionId: "b", status: "working" },
		]);

		const portal = container.querySelector<HTMLElement>('[data-session="b"] .companion-proc-portal');
		const leap = container.querySelector<HTMLElement>('[data-session="b"] .companion-proc-transit');
		// The ring still opens — the event is not hidden from anyone. It is simply over
		// in a quarter of a second, and the pet's own visibility comes from an inline
		// opacity rather than from a keyframe the media query has killed.
		expect(portal?.style.animationDuration).toBe(`${PORTAL_REDUCED_MS}ms`);
		expect(leap?.style.opacity).toBeTruthy();
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

	it("lets a click on a Proc's scenery fall through — a bed is not a pet", () => {
		const onInteractiveChange = vi.fn();
		const { feed, push } = stubFeed();
		const { container } = render(<CompanionStage feed={feed} onInteractiveChange={onInteractiveChange} />);
		push([{ sessionId: "a", status: "todo" }]);

		fireEvent.pointerMove(container.querySelector('[data-slot="ground"] rect')!, { bubbles: true });

		expect(onInteractiveChange).not.toHaveBeenCalled();
	});

	it("hands the pointer back when it leaves the overlay without crossing off a Proc", () => {
		// The pointer leaving the window is the release. It used to be released on the
		// window losing FOCUS as well, as a second chance to notice a missed leave —
		// but a focus change says nothing about where the mouse is, and once a
		// terminal bubble borrows the keyboard and gives it back, `blur` fires in the
		// middle of ordinary use with the pointer still on a Proc. The clock-based
		// re-check (POINTER_REVALIDATE_MS) is the second chance now, and it asks the
		// question a focus change cannot answer: what is under the pointer.
		const onInteractiveChange = vi.fn();
		const { feed, push } = stubFeed();
		const { container } = render(<CompanionStage feed={feed} onInteractiveChange={onInteractiveChange} />);
		push([{ sessionId: "a", status: "working" }]);

		fireEvent.pointerMove(container.querySelector("[data-figure] rect")!, { bubbles: true });
		fireEvent.pointerLeave(document);

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

describe("asking for a different look", () => {
	function pushOne() {
		const { feed, push } = stubFeed();
		const view = render(<CompanionStage feed={feed} onRequestLook={onRequestLook} />);
		push([{ sessionId: "a", status: "pr_open", name: "fix the flaky test", project: "agent-orchestrator" }]);
		return { ...view, push };
	}

	const onRequestLook = vi.fn();

	afterEach(() => {
		onRequestLook.mockReset();
		window.localStorage.clear();
		window.dispatchEvent(new StorageEvent("storage", { key: null }));
	});

	it("opens the library for the Proc that was right-clicked", () => {
		const { container } = pushOne();

		fireEvent.contextMenu(container.querySelector("[data-figure] rect")!, { bubbles: true });

		expect(onRequestLook).toHaveBeenCalledWith("a");
	});

	it("does NOT pick the Proc up, which is what press-drag is for", () => {
		// The reason the gesture is a different BUTTON. If a right-press still grabbed,
		// the pet would be flung across the band while its library opened.
		const { container } = pushOne();

		fireEvent.pointerDown(container.querySelector("[data-figure] rect")!, { bubbles: true, button: 2, clientX: 400 });

		expect(container.querySelector("[data-teased]")).toBeNull();
	});

	it("leaves the desktop's own menu alone anywhere but on a Proc", () => {
		// The band is 150px of mostly-transparent frame per pet. Eating the context
		// menu across all of it would be the click-through bug again, in another form.
		const { container } = pushOne();
		const event = new MouseEvent("contextmenu", { bubbles: true, cancelable: true });

		container.querySelector(".companion-stage")!.dispatchEvent(event);

		expect(onRequestLook).not.toHaveBeenCalled();
		expect(event.defaultPrevented).toBe(false);
	});
});

describe("the look a Proc wears", () => {
	afterEach(() => {
		window.localStorage.clear();
		window.dispatchEvent(new StorageEvent("storage", { key: null }));
	});

	function pushTwo() {
		const { feed, push } = stubFeed();
		const view = render(<CompanionStage feed={feed} />);
		push([
			{ sessionId: "a", status: "pr_open", name: "one", project: "p" },
			{ sessionId: "b", status: "pr_open", name: "two", project: "p" },
		]);
		return { ...view, push };
	}

	// All three read off the SVG ROOT. Reading them off the hat group used to work and
	// no longer can: only the Proc HAS a hat now — the others wear their own accessory,
	// drawn in a place only their own rig knows.
	const rootOf = (container: HTMLElement, session: string) =>
		container.querySelector(`[data-session="${session}"] svg[data-species]`);
	const hatOf = (container: HTMLElement, session: string) => rootOf(container, session)?.getAttribute("data-accessory");
	const paletteOf = (container: HTMLElement, session: string) =>
		rootOf(container, session)?.getAttribute("data-palette");
	const speciesOf = (container: HTMLElement, session: string) =>
		rootOf(container, session)?.getAttribute("data-species");
	/**
	 * The hash colour a session gets, expressed on whatever CREATURE its project is.
	 *
	 * ⚠ The colour axis is per-session and the creature axis is per-PROJECT, and each
	 * creature brings its own six colours — so "the hash colour" is a SLOT in that
	 * creature's set, not the Proc's id for it. Asserting the Proc's id would be
	 * asserting that the project axis does nothing.
	 */
	const hashPaletteOn = (session: string, project: string) =>
		withSpecies(castForSession(session), speciesForProject(project)).palette;
	const hashWornOn = (session: string, project: string) =>
		withSpecies(castForSession(session), speciesForProject(project)).hatId;

	it("wears the hash look when nobody has chosen one", () => {
		const { container } = pushTwo();

		expect(hatOf(container, "a")).toBe(hashWornOn("a", "p"));
		expect(paletteOf(container, "a")).toBe(hashPaletteOn("a", "p"));
	});

	it("draws every session on a project as the SAME creature", () => {
		// The whole point of the project axis, and what took the coloured mark off the
		// name chip: the band groups itself by shape, so which project a pet belongs to
		// is something you see rather than something you decode.
		const { container } = pushTwo();

		expect(speciesOf(container, "a")).toBe(speciesForProject("p"));
		expect(speciesOf(container, "a")).toBe(speciesOf(container, "b"));
	});

	it("wears the creature its PROJECT was given, over the hash", () => {
		storeProjectSpecies("p", "ghost");
		const { container } = pushTwo();

		expect(speciesOf(container, "a")).toBe("ghost");
		expect(speciesOf(container, "b")).toBe("ghost");
	});

	it("keeps each session's own colour and accessory when the project's creature changes", () => {
		// ⚠ The invariant the Pet library's simplification rests on. Colour and accessory
		// are the hash of the SESSION and nobody chooses them; a project's creature changes
		// which BODY they are painted on, never which slot of it they land in.
		storeProjectSpecies("p", "ghost");
		const { container } = pushTwo();

		expect(paletteOf(container, "a")).toBe(withSpecies(castForSession("a"), "ghost").palette);
		expect(hatOf(container, "a")).toBe(withSpecies(castForSession("a"), "ghost").hatId);
	});

	it("repaints when the OTHER window changes it, which is the whole cross-window path", () => {
		const { container } = pushTwo();

		act(() => {
			const value = serializeProjectLooks({ p: "toadstool" });
			window.localStorage.setItem(LOOKS_STORAGE_KEY, value);
			window.dispatchEvent(new StorageEvent("storage", { key: LOOKS_STORAGE_KEY, newValue: value }));
		});

		expect(speciesOf(container, "a")).toBe("toadstool");
		expect(speciesOf(container, "b")).toBe("toadstool");
	});

	it("leaves every other PROJECT exactly as it was", () => {
		// Recognisability: redressing one project must not move anybody else's pets.
		storeProjectSpecies("p", "ghost");
		const { feed, push } = stubFeed();
		const { container } = render(<CompanionStage feed={feed} />);
		push([
			{ sessionId: "a", status: "pr_open", name: "one", project: "p" },
			{ sessionId: "c", status: "pr_open", name: "three", project: "q" },
		]);

		expect(speciesOf(container, "a")).toBe("ghost");
		expect(speciesOf(container, "c")).toBe(speciesForProject("q"));
		expect(paletteOf(container, "c")).toBe(hashPaletteOn("c", "q"));
		expect(hatOf(container, "c")).toBe(hashWornOn("c", "q"));
	});
});

describe("shaking the Orchestrator to call its project in", () => {
	// The end of the gesture the engine cannot see: real pointer events, on a real
	// figure, arriving in the order a hand produces them.
	const ALPHA: CompanionActivity[] = [
		{ sessionId: "lead", status: "working", name: "coordinator", project: "alpha", kind: "orchestrator" },
		{ sessionId: "a1", status: "pr_open", name: "one", project: "alpha" },
		{ sessionId: "b1", status: "pr_open", name: "outsider", project: "beta" },
	];

	function stage() {
		const { feed, push } = stubFeed();
		const view = render(<CompanionStage feed={feed} />);
		push(ALPHA);
		return view;
	}

	const figureOf = (container: HTMLElement, id: string) =>
		container.querySelector(`[data-session="${id}"] [data-figure] rect`)!;
	const procOf = (container: HTMLElement, id: string) => container.querySelector(`[data-session="${id}"]`)!;
	const atX = (node: Element) => Number(/translate3d\((-?[\d.]+)px/.exec(node.getAttribute("style") ?? "")?.[1]);
	/**
	 * How long this Proc's current move takes, in ms. The tell that separates the
	 * three things a Proc can be doing: 0 is standing, a run is capped at
	 * `MEET_RUN_MAX_MS`, and a stroll is never shorter than `WALK_MIN_MS`.
	 */
	const moveMs = (node: Element) =>
		Number(/transition-duration:\s*([\d.]+)ms/.exec(node.getAttribute("style") ?? "")?.[1] ?? "0");

	/** Wiggle the pointer: `legs` fast reversals, which is what a shake actually is. */
	function shakeOver(node: Element, legs: number, amplitude = 40) {
		let x = 600;
		for (let leg = 0; leg < legs; leg++) {
			const step = (leg % 2 === 0 ? amplitude : -amplitude) / 4;
			for (let i = 0; i < 4; i++) {
				x += step;
				fireEvent.pointerMove(node, { bubbles: true, clientX: x, clientY: 300 });
			}
		}
	}

	it("calls the leader's project in, and leaves every other project standing", () => {
		const { container } = stage();
		const outsiderAt = atX(procOf(container, "b1"));

		fireEvent.pointerDown(figureOf(container, "lead"), { bubbles: true, clientX: 600, clientY: 300 });
		shakeOver(container.querySelector(".companion-stage")!, 4);

		expect(procOf(container, "lead").querySelector("[data-rally-call]")).not.toBeNull();
		// Running, not strolling: a rally is an event, and it moves at the meet's pace.
		expect(moveMs(procOf(container, "a1"))).toBeGreaterThan(0);
		expect(moveMs(procOf(container, "a1"))).toBeLessThanOrEqual(MEET_RUN_MAX_MS);
		expect(atX(procOf(container, "b1"))).toBe(outsiderAt);
		expect(moveMs(procOf(container, "b1"))).toBe(0);
	});

	it("keeps the leader in the hand while it is shaken", () => {
		const { container } = stage();

		fireEvent.pointerDown(figureOf(container, "lead"), { bubbles: true, clientX: 600, clientY: 300 });
		shakeOver(container.querySelector(".companion-stage")!, 4);

		expect(container.querySelector("[data-rally-call]")).not.toBeNull();
		expect(container.querySelector("[data-teased]")).not.toBeNull();
	});

	it("does not throw the leader on the release that ends the shake", () => {
		// The wrist speed that fires a rally is the same speed that would fling a Proc
		// across the desktop. Read literally, every successful shake ends in a throw —
		// and then the two gestures are not distinct at all.
		const { container } = stage();
		const stage_ = container.querySelector(".companion-stage")!;

		fireEvent.pointerDown(figureOf(container, "lead"), { bubbles: true, clientX: 600, clientY: 300 });
		shakeOver(stage_, 4);
		const droppedAt = atX(procOf(container, "lead"));
		fireEvent.pointerUp(stage_, { bubbles: true, clientX: 600, clientY: 300 });

		expect(container.querySelector("[data-teased]")).toBeNull();
		// Set down, not launched: no flight to carry it anywhere.
		expect(atX(procOf(container, "lead"))).toBeCloseTo(droppedAt, 0);
	});

	it("still throws on a fling, because a fling is not a shake", () => {
		const { container } = stage();
		const stage_ = container.querySelector(".companion-stage")!;

		fireEvent.pointerDown(figureOf(container, "lead"), { bubbles: true, clientX: 300, clientY: 300 });
		// One long directional run — the throw gesture, untouched.
		for (let x = 340; x <= 900; x += 40) {
			fireEvent.pointerMove(stage_, { bubbles: true, clientX: x, clientY: 260 });
		}

		expect(container.querySelector("[data-rally-call]")).toBeNull();
		expect(container.querySelector("[data-teased]")).not.toBeNull();
	});

	it("does not rally off a worker, however hard it is shaken", () => {
		const { container } = stage();

		fireEvent.pointerDown(figureOf(container, "a1"), { bubbles: true, clientX: 600, clientY: 300 });
		shakeOver(container.querySelector(".companion-stage")!, 6);

		expect(container.querySelector("[data-rally-call]")).toBeNull();
	});

	it("does not rally on a slow drag, which is how a Proc gets repositioned", () => {
		vi.useFakeTimers();
		const { container } = stage();
		const stage_ = container.querySelector(".companion-stage")!;

		fireEvent.pointerDown(figureOf(container, "lead"), { bubbles: true, clientX: 600, clientY: 300 });
		// The same reversals, carried at a walking pace instead of flicked.
		for (const x of [700, 800, 700, 600, 700, 800, 700, 600]) {
			act(() => vi.advanceTimersByTime(500));
			fireEvent.pointerMove(stage_, { bubbles: true, clientX: x, clientY: 300 });
		}

		expect(container.querySelector("[data-rally-call]")).toBeNull();
	});

	it("gathers without running when motion is reduced, and the gesture still works", () => {
		prefersReducedMotion(true);
		const { feed, push } = stubFeed();
		const { container } = render(<CompanionStage feed={feed} />);
		push(ALPHA);

		const before = atX(procOf(container, "a1"));
		fireEvent.pointerDown(figureOf(container, "lead"), { bubbles: true, clientX: 600, clientY: 300 });
		shakeOver(container.querySelector(".companion-stage")!, 4);

		expect(container.querySelector("[data-rally-call]")).not.toBeNull();
		// Gathered, not run in: it is simply already standing at its place in the ring.
		expect(moveMs(procOf(container, "a1"))).toBe(0);
		expect(atX(procOf(container, "a1"))).not.toBe(before);
	});
});

// the click/drag split.
describe("clicking a Proc to talk to its session", () => {
	function staged(onActivate: (sessionId: string, at: { x: number; y: number }) => void) {
		const { feed, push } = stubFeed();
		const view = render(<CompanionStage feed={feed} onActivate={onActivate} />);
		push([{ sessionId: "a", status: "pr_open", name: "fix the flaky test", project: "agent-orchestrator" }]);
		return view;
	}
	// Re-queried every time: picking a Proc up swaps its pose, so the node changes.
	const figure = (container: HTMLElement) => container.querySelector("[data-figure] rect")!;

	it("a press that never moved is a click", () => {
		const onActivate = vi.fn();
		const { container } = staged(onActivate);

		fireEvent.pointerDown(figure(container), { bubbles: true, clientX: 400, clientY: 900 });
		fireEvent.pointerUp(figure(container), { bubbles: true, clientX: 400, clientY: 900 });

		expect(onActivate).toHaveBeenCalledWith("a", { x: 400, y: 900 });
	});

	it("a hand's wobble is still a click", () => {
		const onActivate = vi.fn();
		const { container } = staged(onActivate);

		fireEvent.pointerDown(figure(container), { bubbles: true, clientX: 400, clientY: 900 });
		fireEvent.pointerMove(figure(container), { bubbles: true, clientX: 403, clientY: 901 });
		fireEvent.pointerUp(figure(container), { bubbles: true, clientX: 403, clientY: 901 });

		expect(onActivate).toHaveBeenCalledTimes(1);
	});

	it("a DRAG throws the Proc and opens nothing", () => {
		// The two gestures start identically, so the split has to be made on what the
		// hand did — not on which handler fired.
		const onActivate = vi.fn();
		const { container } = staged(onActivate);

		fireEvent.pointerDown(figure(container), { bubbles: true, clientX: 400, clientY: 900 });
		fireEvent.pointerMove(figure(container), { bubbles: true, clientX: 520, clientY: 840 });
		fireEvent.pointerUp(figure(container), { bubbles: true, clientX: 520, clientY: 840 });

		expect(onActivate).not.toHaveBeenCalled();
	});

	it("a Proc that was picked up and HELD is put down, not opened", () => {
		vi.useFakeTimers();
		try {
			const onActivate = vi.fn();
			const { container } = staged(onActivate);

			fireEvent.pointerDown(figure(container), { bubbles: true, clientX: 400, clientY: 900 });
			act(() => vi.advanceTimersByTime(1_500));
			fireEvent.pointerUp(figure(container), { bubbles: true, clientX: 400, clientY: 900 });

			expect(onActivate).not.toHaveBeenCalled();
		} finally {
			vi.useRealTimers();
		}
	});

	it("a right-click asks for the look, never for a terminal", () => {
		const onActivate = vi.fn();
		const onRequestLook = vi.fn();
		const { feed, push } = stubFeed();
		const { container } = render(<CompanionStage feed={feed} onActivate={onActivate} onRequestLook={onRequestLook} />);
		push([{ sessionId: "a", status: "pr_open", name: "n", project: "p" }]);

		fireEvent.pointerDown(figure(container), { bubbles: true, button: 2, clientX: 400, clientY: 900 });
		fireEvent.contextMenu(figure(container), { bubbles: true });
		fireEvent.pointerUp(figure(container), { bubbles: true, button: 2, clientX: 400, clientY: 900 });

		expect(onRequestLook).toHaveBeenCalledWith("a");
		expect(onActivate).not.toHaveBeenCalled();
	});

	it("a press on empty band is nobody's click", () => {
		const onActivate = vi.fn();
		const { container } = staged(onActivate);
		const band = container.querySelector(".companion-stage")!;

		fireEvent.pointerDown(band, { bubbles: true, clientX: 100, clientY: 900 });
		fireEvent.pointerUp(band, { bubbles: true, clientX: 100, clientY: 900 });

		expect(onActivate).not.toHaveBeenCalled();
	});
});

// the window's click-through state must survive the
// two things that move WITHOUT the pointer moving — losing the keyboard, and the
// scene changing under a resting cursor.
describe("who owns the pointer when the pointer is not the thing that moved", () => {
	function staged() {
		const onInteractiveChange = vi.fn();
		const { feed, push } = stubFeed();
		const view = render(<CompanionStage feed={feed} onInteractiveChange={onInteractiveChange} />);
		push([{ sessionId: "a", status: "pr_open", name: "n", project: "p" }]);
		return { ...view, onInteractiveChange };
	}
	const figure = (container: HTMLElement) => container.querySelector("[data-figure] rect")!;

	it("does NOT give the pointer back just because the window lost the keyboard", () => {
		// The regression this exists for: a terminal bubble borrows the keyboard and
		// gives it back when it closes, which fires `blur` with the pointer still
		// sitting on a Proc. Releasing there made the window click-through under the
		// cursor, so the next click went to the desktop behind instead of the pet —
		// "I have to click twice".
		const { container, onInteractiveChange } = staged();
		fireEvent.pointerMove(figure(container), { bubbles: true, clientX: 400, clientY: 900 });
		expect(onInteractiveChange).toHaveBeenLastCalledWith(true);
		onInteractiveChange.mockClear();
		// jsdom has no layout, so elementFromPoint answers null; the point of the test
		// is that blur does not blindly release, so pin it to "no release happened".
		document.elementFromPoint = () => figure(container) as unknown as Element;

		fireEvent.blur(window);

		expect(onInteractiveChange).not.toHaveBeenCalledWith(false);
	});

	it("still gives the pointer back when the pointer genuinely leaves the window", () => {
		const { container, onInteractiveChange } = staged();
		fireEvent.pointerMove(figure(container), { bubbles: true, clientX: 400, clientY: 900 });
		onInteractiveChange.mockClear();

		fireEvent.pointerLeave(document);

		expect(onInteractiveChange).toHaveBeenLastCalledWith(false);
	});

	it("does not hand the desktop back every time a Proc walks out from under the cursor", () => {
		// With `capture: true` this listener caught the leave of EVERY element in the
		// page, hundreds a minute as the band animates, and each one said "the pointer
		// is gone". The window's click-through state flapped, and a click that landed
		// in the wrong half of a flap went to the desktop instead of the pet.
		const onInteractiveChange = vi.fn();
		const { feed, push } = stubFeed();
		const { container } = render(<CompanionStage feed={feed} onInteractiveChange={onInteractiveChange} />);
		push([{ sessionId: "a", status: "pr_open", name: "n", project: "p" }]);
		const figure = container.querySelector("[data-figure] rect")!;
		fireEvent.pointerMove(figure, { bubbles: true, clientX: 400, clientY: 900 });
		onInteractiveChange.mockClear();

		// A leave from something INSIDE the page, which is not the pointer leaving.
		fireEvent.pointerLeave(figure, { bubbles: false });

		expect(onInteractiveChange).not.toHaveBeenCalled();
	});

	it("re-decides on a clock, so a Proc that walked under a resting cursor is clickable", () => {
		vi.useFakeTimers();
		try {
			const onInteractiveChange = vi.fn();
			const { feed, push } = stubFeed();
			const { container } = render(<CompanionStage feed={feed} onInteractiveChange={onInteractiveChange} />);
			push([{ sessionId: "a", status: "pr_open", name: "n", project: "p" }]);
			// The pointer rests on empty band…
			document.elementFromPoint = () => document.body;
			fireEvent.pointerMove(container.querySelector(".companion-stage")!, { bubbles: true, clientX: 10, clientY: 900 });
			onInteractiveChange.mockClear();

			// …and a Proc walks under it without the pointer moving at all.
			document.elementFromPoint = () => figure(container) as unknown as Element;
			act(() => vi.advanceTimersByTime(POINTER_REVALIDATE_MS * 2));

			expect(onInteractiveChange).toHaveBeenLastCalledWith(true);
		} finally {
			vi.useRealTimers();
		}
	});
});

// the terminal is a WINDOW, and the stage's job is to
// tell the shell where its Proc is so the window can travel with it.
describe("a terminal pinned to a Proc", () => {
	it("reports where its Proc is, so the terminal window can follow", () => {
		const onAttachedAnchorMove = vi.fn();
		const { feed, push } = stubFeed();
		render(<CompanionStage feed={feed} attachedSession="a" onAttachedAnchorMove={onAttachedAnchorMove} />);
		push([
			{ sessionId: "a", status: "working", name: "one", project: "p" },
			{ sessionId: "b", status: "working", name: "two", project: "p" },
		]);

		expect(onAttachedAnchorMove).toHaveBeenCalled();
		const anchor = onAttachedAnchorMove.mock.calls.at(-1)![0];
		expect(Number.isFinite(anchor.x)).toBe(true);
		expect(Number.isFinite(anchor.y)).toBe(true);
	});

	it("says the Proc has gone when its session ends, so the terminal can close", () => {
		// A terminal floating over nobody points at a session that is not there.
		vi.useFakeTimers();
		const onAttachedGone = vi.fn();
		const { feed, push } = stubFeed();
		render(<CompanionStage feed={feed} attachedSession="a" onAttachedGone={onAttachedGone} />);
		push([
			{ sessionId: "a", status: "working", name: "one", project: "p" },
			{ sessionId: "b", status: "working", name: "two", project: "p" },
		]);
		expect(onAttachedGone).not.toHaveBeenCalled();

		// "a" ends: it leaves through its portal and is off the band afterwards.
		push([{ sessionId: "b", status: "working", name: "two", project: "p" }]);
		act(() => void vi.advanceTimersByTime(PORTAL_OUT_MS + 1_000));

		expect(onAttachedGone).toHaveBeenCalled();
	});

	it("does not call it before the band has anybody on it at all", () => {
		// An empty world at mount is "nothing has arrived yet", not "your session ended".
		const onAttachedGone = vi.fn();
		const { feed } = stubFeed();
		render(<CompanionStage feed={feed} attachedSession="a" onAttachedGone={onAttachedGone} />);

		expect(onAttachedGone).not.toHaveBeenCalled();
	});

	it("says nothing when the session it belongs to has left the band", () => {
		const onAttachedAnchorMove = vi.fn();
		const { feed, push } = stubFeed();
		render(<CompanionStage feed={feed} attachedSession="gone" onAttachedAnchorMove={onAttachedAnchorMove} />);
		push([{ sessionId: "a", status: "working", name: "one", project: "p" }]);

		expect(onAttachedAnchorMove).not.toHaveBeenCalled();
	});
});
