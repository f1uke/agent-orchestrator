import { expect, test, type Page } from "@playwright/test";

// Guards every clickable the terminal makes, against a real pointer in a real
// browser. #57 fixed terminal clickables once; nothing has held them since, and
// they broke again — silently, because a dead link looks exactly like a live one.
//
// jsdom cannot make these assertions at all: xterm's Linkifier resolves a
// pointer to a buffer cell through the measured cell box, and jsdom measures
// every element as 0×0, so no position ever hit-tests onto a link and nothing is
// ever hovered. The harness is e2e/terminal-gallery.html.

/**
 * What each fixture row renders, and where its clickable sits in that rendered
 * text. `rendered` is the row AFTER escape sequences are consumed — the OSC 8
 * row's URI never occupies a cell.
 */
const TARGETS = [
	{ label: "osc8", row: 0, rendered: "OSC8 hyperlink", token: "OSC8" },
	{ label: "weblink", row: 1, rendered: "see https://example.com/plain for details", token: "example.com/plain" },
	{ label: "jira", row: 2, rendered: "picked up MOBILITY-4765 from the board", token: "MOBILITY-4765" },
	{ label: "github", row: 3, rendered: "opened #262 against main", token: "#262" },
	{ label: "gitlab", row: 4, rendered: "review !2961 when you can", token: "!2961" },
	{
		label: "file",
		row: 5,
		rendered: "edited frontend/src/renderer/components/XtermTerminal.tsx",
		token: "renderer/components",
	},
	{ label: "session", row: 6, rendered: "handed off to @ao-demo-12 for review", token: "@ao-demo-12" },
	{ label: "plain", row: 7, rendered: "nothing on this line is a link", token: "is a link" },
] as const;

type Target = (typeof TARGETS)[number];

function targetFor(label: Target["label"]): Target {
	const target = TARGETS.find((candidate) => candidate.label === label);
	if (!target) throw new Error(`no fixture row labelled ${label}`);
	return target;
}

/**
 * Open the harness and wait until the grid has stopped re-fitting.
 *
 * `repainting` is the mode the reported bug lives in: an agent redrawing its
 * screen several times a second. Each redraw destroys and rebuilds the link
 * under the pointer, and xterm 5.5 will only complete a click whose press and
 * release saw the very same link OBJECT — so without the pane's own sticky
 * activation, a human-paced click during output never opens anything.
 */
async function openGallery(page: Page, options?: { mouseTracking?: boolean; repainting?: boolean }): Promise<void> {
	const query = [options?.mouseTracking ? "mouse=on" : "", options?.repainting ? "repaint=on" : ""]
		.filter(Boolean)
		.join("&");
	await page.goto(`/e2e/terminal-gallery.html${query ? `?${query}` : ""}`);
	await expect(page.locator(".xterm-screen")).toBeVisible();
	await expect(page.getByTestId("terminal-error")).toHaveCount(0);
	// XtermTerminal re-fits on several triggers after mount; a click computed
	// against a grid that is still moving lands on the wrong cell.
	await page.waitForFunction(() => {
		const term = window.__aoTerminal;
		if (!term) return false;
		const key = `${term.cols}x${term.rows}`;
		const seen = (window as unknown as { __aoGrid?: string }).__aoGrid;
		(window as unknown as { __aoGrid?: string }).__aoGrid = key;
		return seen === key;
	});
}

/** The centre of the cell holding the middle of `token` on its fixture row. */
async function tokenPoint(page: Page, target: Target): Promise<{ x: number; y: number }> {
	const column = target.rendered.indexOf(target.token) + Math.floor(target.token.length / 2);
	if (column < 0) throw new Error(`token ${target.token} is not in row ${target.label}`);
	const box = await page.locator(".xterm-screen").boundingBox();
	if (!box) throw new Error("terminal screen has no box");
	const grid = await page.evaluate(() => ({
		cols: window.__aoTerminal!.cols,
		rows: window.__aoTerminal!.rows,
	}));
	return {
		x: box.x + ((column + 0.5) * box.width) / grid.cols,
		y: box.y + ((target.row + 0.5) * box.height) / grid.rows,
	};
}

/** How long a real finger holds a mouse button down. */
const CLICK_HOLD_MS = 90;

/**
 * ⌘+click a fixture row's clickable, the way the reporter does. The pointer
 * moves onto the cell first, because that hover is what makes a link current,
 * and the button is HELD for as long as a hand holds it — the bug this guards
 * lives in the window between press and release, so a synthetic instant click
 * would slip through it and prove nothing.
 */
async function commandClick(page: Page, label: Target["label"]): Promise<void> {
	const point = await tokenPoint(page, targetFor(label));
	await page.mouse.move(point.x, point.y);
	await page.keyboard.down("Meta");
	await page.mouse.down();
	await page.waitForTimeout(CLICK_HOLD_MS);
	await page.mouse.up();
	await page.keyboard.up("Meta");
}

async function opened(page: Page): Promise<{ via: string; url: string }[]> {
	return page.evaluate(() => window.__aoOpened ?? []);
}

async function activated(page: Page): Promise<{ kind: string; value: string }[]> {
	return page.evaluate(() => window.__aoActivated ?? []);
}

// The reported gesture, on the reported link shape, with the pane in the mode
// Claude Code actually runs it in (mouse tracking on).
test("⌘+click opens a Claude-emitted OSC 8 hyperlink in the browser", async ({ page }) => {
	await openGallery(page, { mouseTracking: true, repainting: true });
	await commandClick(page, "osc8");
	await expect.poll(() => opened(page)).toEqual([{ via: "window.open", url: "https://example.com/osc8" }]);
});

test("⌘+click opens a bare Jira key against the session's browse base", async ({ page }) => {
	await openGallery(page, { mouseTracking: true, repainting: true });
	await commandClick(page, "jira");
	await expect
		.poll(() => opened(page))
		.toEqual([{ via: "window.open", url: "https://acme.atlassian.net/browse/MOBILITY-4765" }]);
});

test("⌘+click opens a #<num> GitHub ref and a !<num> GitLab ref", async ({ page }) => {
	await openGallery(page, { mouseTracking: true, repainting: true });
	await commandClick(page, "github");
	await expect
		.poll(() => opened(page))
		.toEqual([{ via: "window.open", url: "https://github.com/aoagents/agent-orchestrator/pull/262" }]);
	await commandClick(page, "gitlab");
	await expect
		.poll(() => opened(page))
		.toEqual([
			{ via: "window.open", url: "https://github.com/aoagents/agent-orchestrator/pull/262" },
			{ via: "window.open", url: "https://gitlab.example.com/acme/mobility/-/merge_requests/2961" },
		]);
});

test("⌘+click opens an auto-detected plain https URL", async ({ page }) => {
	await openGallery(page, { mouseTracking: true, repainting: true });
	await commandClick(page, "weblink");
	await expect.poll(() => opened(page)).toEqual([{ via: "window.open", url: "https://example.com/plain" }]);
});

// #127's file references open the IN-APP viewer. A fix that routed every
// clickable through the browser would regress them, so this pins the split.
test("⌘+click on a file reference stays in the app, never the browser", async ({ page }) => {
	await openGallery(page, { mouseTracking: true, repainting: true });
	await commandClick(page, "file");
	await expect
		.poll(() => activated(page))
		.toEqual([{ kind: "file", value: "frontend/src/renderer/components/XtermTerminal.tsx" }]);
	expect(await opened(page)).toEqual([]);
});

test("⌘+click on a session reference navigates in-app, never the browser", async ({ page }) => {
	await openGallery(page, { mouseTracking: true, repainting: true });
	await commandClick(page, "session");
	await expect.poll(() => activated(page)).toEqual([{ kind: "session", value: "ao-demo-12" }]);
	expect(await opened(page)).toEqual([]);
});

test("⌘+click on text that is not a link opens nothing", async ({ page }) => {
	await openGallery(page, { mouseTracking: true, repainting: true });
	await commandClick(page, "plain");
	await page.waitForTimeout(200);
	expect(await opened(page)).toEqual([]);
	expect(await activated(page)).toEqual([]);
});

// A pane with no mouse-tracking app in it (a plain shell, scrollback) linkifies
// the same text, and the same gesture must open it.
test("⌘+click opens links in a quiet pane with no mouse-tracking app", async ({ page }) => {
	await openGallery(page);
	await commandClick(page, "jira");
	await expect
		.poll(() => opened(page))
		.toEqual([{ via: "window.open", url: "https://acme.atlassian.net/browse/MOBILITY-4765" }]);
});

// The terminal must stay a terminal: a plain click still reaches the agent TUI
// as a mouse report (that is what makes Claude Code's own clickables work), and
// a drag still selects text locally.
test("a plain click still forwards a mouse report to the agent", async ({ page }) => {
	await openGallery(page, { mouseTracking: true, repainting: true });
	const point = await tokenPoint(page, targetFor("plain"));
	await page.mouse.move(point.x, point.y);
	await page.mouse.down();
	await page.mouse.up();
	await expect
		.poll(async () => (await page.evaluate(() => window.__aoInput ?? [])).filter((entry) => entry.source === "mouse"))
		.not.toEqual([]);
});

test("dragging still selects terminal text", async ({ page }) => {
	await openGallery(page);
	const start = await tokenPoint(page, targetFor("plain"));
	await page.mouse.move(start.x - 40, start.y);
	await page.mouse.down();
	await page.mouse.move(start.x + 40, start.y, { steps: 8 });
	await page.mouse.up();
	// The selection lives in xterm's own model (the renderer paints it onto a
	// canvas, so the DOM has no Selection to read); the pane copies it, which is
	// both observable and the thing a selection is actually for.
	await expect.poll(() => page.evaluate(() => window.__aoCopied ?? [])).not.toEqual([]);
});
