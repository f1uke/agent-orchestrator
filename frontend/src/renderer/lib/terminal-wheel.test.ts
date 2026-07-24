import { describe, expect, it, vi } from "vitest";
import { createWheelForwarder, pageKeyReport, sgrWheelReport } from "./terminal-wheel";

// A fake terminal exposing only what the forwarder reads. `mouseTracking` and
// `bufferType` are the two facts that decide which kind of scroll the wheel means.
function fakeTerm(opts: { mouseTracking?: string; bufferType?: string; rows?: number } = {}) {
	const scrollLines = vi.fn();
	return {
		term: {
			modes: { mouseTrackingMode: opts.mouseTracking ?? "none" },
			buffer: { active: { type: opts.bufferType ?? "normal" } },
			scrollLines,
			rows: opts.rows ?? 24,
			options: { fontSize: 12, lineHeight: 1 },
		} as unknown as Parameters<typeof createWheelForwarder>[0],
		scrollLines,
	};
}

const wheel = (partial: Partial<WheelEvent>): WheelEvent => partial as WheelEvent;

describe("createWheelForwarder", () => {
	it("sends SGR wheel reports to a mouse-tracking pane (tmux copy-mode, Claude Code)", () => {
		// This is the branch the human's Claude Code needs: the pane tracks the mouse,
		// so the wheel becomes reports it scrolls on itself.
		const emit = vi.fn();
		const { term } = fakeTerm({ mouseTracking: "sgr", bufferType: "alternate" });
		const handler = createWheelForwarder(term, { paneScrollsByKeyboard: () => false, emit });

		// 36px up at 12px/row ⇒ 3 lines up ⇒ three wheel-up reports.
		expect(handler(wheel({ deltaY: -36, deltaMode: 0 }))).toBe(false);
		expect(emit).toHaveBeenCalledWith(sgrWheelReport(64, 3), "wheel");
	});

	it("scrolls the terminal's own scrollback for a plain shell (normal buffer, no tracking)", () => {
		const emit = vi.fn();
		const { term, scrollLines } = fakeTerm({ mouseTracking: "none", bufferType: "normal" });
		const handler = createWheelForwarder(term, { paneScrollsByKeyboard: () => false, emit });

		handler(wheel({ deltaY: 24, deltaMode: 0 }));

		expect(scrollLines).toHaveBeenCalledWith(2);
		expect(emit).not.toHaveBeenCalled();
	});

	it("sends page keys for a pane that scrolls its transcript by keyboard (opencode)", () => {
		const emit = vi.fn();
		const { term } = fakeTerm({ mouseTracking: "sgr", bufferType: "alternate" });
		const handler = createWheelForwarder(term, { paneScrollsByKeyboard: () => true, emit });

		handler(wheel({ deltaY: -12, deltaMode: 0 }));

		expect(emit).toHaveBeenCalledWith(pageKeyReport(-1), "wheel");
	});

	it("honours line- and page-mode wheels, not just pixel deltas", () => {
		const emit = vi.fn();
		const { term } = fakeTerm({ mouseTracking: "sgr", bufferType: "alternate", rows: 10 });
		const handler = createWheelForwarder(term, { paneScrollsByKeyboard: () => false, emit });

		handler(wheel({ deltaY: -1, deltaMode: 1 /* line */ }));
		expect(emit).toHaveBeenLastCalledWith(sgrWheelReport(64, 1), "wheel");

		handler(wheel({ deltaY: 1, deltaMode: 2 /* page */ }));
		expect(emit).toHaveBeenLastCalledWith(sgrWheelReport(65, 10), "wheel");
	});

	it("leaves Ctrl/Cmd wheel alone — that is the font-size zoom", () => {
		const emit = vi.fn();
		const { term, scrollLines } = fakeTerm();
		const handler = createWheelForwarder(term, { paneScrollsByKeyboard: () => false, emit });

		expect(handler(wheel({ deltaY: -50, ctrlKey: true }))).toBe(false);
		expect(handler(wheel({ deltaY: -50, metaKey: true }))).toBe(false);
		expect(emit).not.toHaveBeenCalled();
		expect(scrollLines).not.toHaveBeenCalled();
	});

	it("does nothing for a sub-line pixel delta until it accumulates", () => {
		const emit = vi.fn();
		const { term, scrollLines } = fakeTerm({ mouseTracking: "none", bufferType: "normal" });
		const handler = createWheelForwarder(term, { paneScrollsByKeyboard: () => false, emit });

		handler(wheel({ deltaY: 5, deltaMode: 0 })); // < one 12px row
		expect(scrollLines).not.toHaveBeenCalled();
		handler(wheel({ deltaY: 8, deltaMode: 0 })); // 13px total ⇒ one row
		expect(scrollLines).toHaveBeenCalledWith(1);
	});
});
