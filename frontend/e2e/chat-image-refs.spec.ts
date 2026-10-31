import { expect, type Page, test } from "@playwright/test";
import {
	REFS_DIRECTORY,
	REFS_GLOB,
	REFS_MISSING,
	REFS_OPTION_IMAGE,
	refsPng,
} from "../src/renderer/lib/chat-mock-refs";

const VIEWPORTS = [
	{ width: 1920, height: 1080 },
	{ width: 2560, height: 1440 },
];
const SHOTS = process.env.AO_REFS_SHOTS;
const SESSION = "/#/projects/ao-demo/sessions/demo-chat-image-refs";
const DIRECTORY = REFS_DIRECTORY;
const GLOB = REFS_GLOB;

const file = (path: string) => ({
	root: "knowledge",
	path,
	kind: "image",
	mime: "image/png",
	size: 2_048,
	modifiedAt: "2026-10-31T10:00:00Z",
});

const PNGS: Record<string, string> = {
	[`${DIRECTORY}clip-before-1280.png`]: refsPng(320, 300),
	[`${DIRECTORY}clip-before-1920.png`]: refsPng(320, 250),
	[`${DIRECTORY}clip-before-2560.png`]: refsPng(320, 200),
	[REFS_OPTION_IMAGE]: refsPng(320, 310),
};

// Like the daemon: a bare glob resolves at the roots and finds nothing; joined to its directory it finds the files.
const KNOWN: Record<string, ReturnType<typeof file>[]> = {
	[`${DIRECTORY}${GLOB}`]: [
		`${DIRECTORY}clip-before-1280.png`,
		`${DIRECTORY}clip-before-1920.png`,
		`${DIRECTORY}clip-before-2560.png`,
	].map(file),
	[REFS_OPTION_IMAGE]: [file(REFS_OPTION_IMAGE)],
};

/** The daemon's two chat file routes. `confirmed: false` is a daemon that cannot confirm any path. */
async function stubDaemon(page: Page, confirmed: boolean) {
	const resolveBodies: string[][] = [];
	await page.route("**/chat/files/resolve", async (route) => {
		const { refs } = route.request().postDataJSON() as { refs: string[] };
		resolveBodies.push(refs);
		await route.fulfill({
			json: { files: refs.map((ref) => ({ ref, matches: confirmed ? (KNOWN[ref] ?? []) : [] })) },
		});
	});
	await page.route("**/chat/files/knowledge/**", async (route) => {
		const path = decodeURIComponent(new URL(route.request().url()).pathname.split("/chat/files/knowledge/")[1]);
		const base64 = PNGS[path];
		if (!base64) return route.fulfill({ status: 404, json: { code: "CHAT_FILE_NOT_FOUND" } });
		return route.fulfill({ status: 200, contentType: "image/png", body: Buffer.from(base64, "base64") });
	});
	return resolveBodies;
}

for (const viewport of VIEWPORTS) {
	test.describe(`chat image refs at ${viewport.width}x${viewport.height}`, () => {
		test.use({ viewport, colorScheme: "dark" });

		const shot = (page: Page, info: { outputPath: (name: string) => string }, name: string) =>
			page.screenshot({
				path: SHOTS ? `${SHOTS}/${name}-${viewport.width}.png` : info.outputPath(`${name}-${viewport.width}.png`),
			});

		test("shows the images a reply and a question name, opens them in the lightbox and leaves a missing path as text", async ({
			page,
		}, info) => {
			const resolved = await stubDaemon(page, true);
			await page.goto(SESSION);
			const stream = page.getByTestId("chat-stream");
			await expect(stream).toBeVisible();

			const reply = stream.locator('[data-row="assistant_text"]');
			await expect(reply).toContainText(REFS_MISSING);
			const row = reply.getByTestId("ref-images");
			await expect(row.getByRole("img")).toHaveCount(3);
			await expect(row.getByRole("img").first()).toHaveAttribute("src", /^blob:/);
			await expect(row.getByText("clip-before-1920.png")).toHaveAttribute("title", `${DIRECTORY}clip-before-1920.png`);
			await expect(reply.locator("code", { hasText: REFS_MISSING })).toBeVisible();
			const thumb = await row.getByRole("img").first().boundingBox();
			expect(thumb!.height).toBeLessThanOrEqual(160);
			expect(thumb!.width).toBeLessThanOrEqual(240);
			await reply.evaluate((el) => el.scrollIntoView({ block: "start" }));
			await shot(page, info, "after-reply");

			const card = page.locator('[data-pending="question"]');
			await expect(card).toBeVisible();
			await expect(card.getByTestId("ref-images").getByRole("img")).toHaveCount(1);
			await expect(
				card.getByRole("radio", { name: new RegExp(REFS_OPTION_IMAGE.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")) }),
			).toBeVisible();
			await card.scrollIntoViewIfNeeded();
			await shot(page, info, "after-question");

			expect(resolved.flat().sort()).toEqual([GLOB, `${DIRECTORY}${GLOB}`, REFS_MISSING, REFS_OPTION_IMAGE].sort());

			await row.getByRole("button", { name: "View clip-before-1920.png" }).click();
			const dialog = page.getByRole("dialog");
			await expect(dialog).toBeVisible();
			await expect(dialog.getByText("2 / 3")).toBeVisible();
			await expect(dialog.getByRole("img", { name: "clip-before-1920.png" })).toBeVisible();
			await shot(page, info, "after-lightbox");
			await page.keyboard.press("ArrowRight");
			await expect(dialog.getByText("3 / 3")).toBeVisible();
			await page.keyboard.press("Escape");
			await expect(dialog).toHaveCount(0);
		});

		test("draws no image and no error when the daemon confirms nothing", async ({ page }, info) => {
			const resolved = await stubDaemon(page, false);
			await page.goto(SESSION);
			const stream = page.getByTestId("chat-stream");
			await expect(stream.locator('[data-row="assistant_text"]')).toContainText(REFS_MISSING);
			await expect(page.locator('[data-pending="question"]')).toBeVisible();
			await expect.poll(() => resolved.length).toBeGreaterThan(0);
			await expect(page.getByTestId("ref-images")).toHaveCount(0);
			await expect(page.getByRole("alert")).toHaveCount(0);
			await page.locator('[data-pending="question"]').scrollIntoViewIfNeeded();
			await shot(page, info, "before-question");
			await stream.locator('[data-row="assistant_text"]').evaluate((el) => el.scrollIntoView({ block: "start" }));
			await shot(page, info, "before-reply");
		});
	});
}
