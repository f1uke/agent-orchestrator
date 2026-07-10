// Integration check against the REAL @xterm/xterm (deliberately NOT mocked, unlike
// XtermTerminal.test.tsx). The click-passthrough fix hinges on one load-bearing
// assumption: xterm emits its mouse reports in a shape our SGR_MOUSE_REPORT filter
// matches (SGR/SGR-Pixels via onData) or on onBinary (DEFAULT encoding). This pins
// that against the installed xterm version so a future upgrade that changes the
// wire format can't silently break mouse forwarding.
import { describe, expect, it } from "vitest";
import { Terminal } from "@xterm/xterm";
import { SGR_MOUSE_REPORT } from "./XtermTerminal";

type MouseCore = {
	coreMouseService: {
		areMouseEventsActive: boolean;
		triggerMouseEvent: (event: {
			col: number;
			row: number;
			x: number;
			y: number;
			button: number;
			action: number;
			ctrl: boolean;
			alt: boolean;
			shift: boolean;
		}) => boolean;
	};
};

// CoreMouseAction values, confirmed empirically against the installed xterm:
// a DOWN (press) encodes with a trailing "M", an UP (release) with a trailing "m".
const DOWN = 1;
const UP = 0;

function openTerminal() {
	const term = new Terminal({ allowProposedApi: true });
	const host = document.createElement("div");
	document.body.appendChild(host);
	term.open(host);
	return { term, core: (term as unknown as { _core: MouseCore })._core };
}

describe("real xterm mouse report format", () => {
	it("emits SGR press/release reports through onData that match SGR_MOUSE_REPORT", async () => {
		const { term, core } = openTerminal();
		const onData: string[] = [];
		term.onData((d) => onData.push(d));

		// Enable mouse tracking (1000) + SGR encoding (1006) — what Claude Code sets.
		await new Promise<void>((resolve) => term.write("\x1b[?1000h\x1b[?1006h", resolve));
		expect(core.coreMouseService.areMouseEventsActive).toBe(true);

		const opts = { col: 11, row: 6, x: 11, y: 6, ctrl: false, alt: false, shift: false };
		core.coreMouseService.triggerMouseEvent({ ...opts, button: 0, action: DOWN });
		core.coreMouseService.triggerMouseEvent({ ...opts, button: 0, action: UP });

		const reports = onData.filter((d) => SGR_MOUSE_REPORT.test(d));
		// A press (M) and a release (m) both matched and carried through onData.
		expect(reports.length).toBe(2);
		expect(reports[0]).toMatch(/M$/);
		expect(reports[1]).toMatch(/m$/);
	});

	it("emits DEFAULT-encoding reports on onBinary (mouse-only channel) which we forward wholesale", async () => {
		const { term, core } = openTerminal();
		const onData: string[] = [];
		const onBinary: string[] = [];
		term.onData((d) => onData.push(d));
		term.onBinary((d) => onBinary.push(d));

		// Mouse tracking (1000) WITHOUT SGR → DEFAULT encoding → onBinary path.
		await new Promise<void>((resolve) => term.write("\x1b[?1000h", resolve));
		core.coreMouseService.triggerMouseEvent({
			col: 3,
			row: 2,
			x: 3,
			y: 2,
			button: 0,
			action: DOWN,
			ctrl: false,
			alt: false,
			shift: false,
		});

		expect(onBinary.length).toBe(1);
		expect(onBinary[0].startsWith("\x1b[M")).toBe(true);
		// It is NOT an SGR report, so onData's filter would have dropped it — onBinary is why we forward it.
		expect(SGR_MOUSE_REPORT.test(onBinary[0])).toBe(false);
	});
});
