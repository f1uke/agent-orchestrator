import { expect, type Page, test } from "@playwright/test";
import {
	DOCS_ARTIFACT_URL,
	DOCS_HTML,
	DOCS_JSON,
	DOCS_LOG,
	DOCS_MISSING,
	DOCS_PATCH,
	DOCS_PDF,
	DOCS_PLAN_MD,
	DOCS_PLAN_PATH,
	DOCS_SURVEY,
} from "../src/renderer/lib/chat-mock-docs";

const VIEWPORTS = [
	{ width: 1920, height: 1080 },
	{ width: 2560, height: 1440 },
];
const SHOTS = process.env.AO_DOCS_SHOTS;
const SESSION = "/#/projects/ao-demo/sessions/demo-chat-docs";

type Kind = "markdown" | "html" | "pdf" | "json" | "text" | "log" | "diff";
type Root = "worktree" | "knowledge";

const SURVEY_MD = [
	"# Chat attachments survey",
	"",
	"2026-10-30 · demo",
	"",
	"## What we have",
	"",
	"Read results for images already carry the base64 bytes in the session event log, and the chat channel ships them to the renderer.",
	"",
	"## PR plan",
	"",
	"| PR | What | Backend |",
	"| --- | --- | --- |",
	"| A | Inline images from Read results | no |",
	"| B | shiki in tool cards and diffs | no |",
	"| C | Chat file endpoint, image paths | yes |",
	"| D | Document cards and panel | small |",
	"| E | Attach any file on send | small |",
	"",
	"## Safety",
	"",
	"Agent HTML runs in a sandboxed native view with no script and no same-origin access.",
].join("\n");

const BATCH_JSON = JSON.stringify({
	batch: 3,
	status: "rate_limited",
	error: { code: 429, retry_after: 30, message: "Too Many Requests" },
	items: Array.from({ length: 4 }, (_, i) => ({ id: i, ok: i % 2 === 0 })),
});

const LOG = ["12:00:01 start batch 3", "12:00:04 post /v1/events", "12:00:05 error: 429 Too Many Requests"].join("\n");
const PATCH = [
	"--- a/sender.go",
	"+++ b/sender.go",
	"@@ -10,3 +10,4 @@",
	" attempt := 0",
	"-time.Sleep(2 * time.Second)",
	"+sleepCtx(ctx, budget.Delay(attempt))",
].join("\n");

type Served = { root: Root; kind: Kind; mime: string; body: string; size: number };
const served = (root: Root, kind: Kind, mime: string, body: string, size = body.length): Served => ({
	root,
	kind,
	mime,
	body,
	size,
});

// What the daemon would confirm, keyed by the ref as the chat names it, and what it serves for the path it answers with.
const FILES: Record<string, Served & { path: string }> = {
	[DOCS_PLAN_PATH]: { ...served("worktree", "markdown", "text/markdown", DOCS_PLAN_MD), path: "docs/plan.md" },
	[DOCS_SURVEY]: { ...served("knowledge", "markdown", "text/markdown", SURVEY_MD, 14_200), path: DOCS_SURVEY },
	[DOCS_JSON]: { ...served("worktree", "json", "application/json", BATCH_JSON, 8_100), path: DOCS_JSON },
	[DOCS_LOG]: { ...served("worktree", "log", "text/plain", LOG, 2_140_000), path: DOCS_LOG },
	[DOCS_PATCH]: { ...served("worktree", "diff", "text/plain", PATCH, 1_024), path: DOCS_PATCH },
	[DOCS_PDF]: { ...served("worktree", "pdf", "application/pdf", "%PDF-1.4", 412_000), path: DOCS_PDF },
	[DOCS_HTML]: { ...served("worktree", "html", "text/html", "<h1>Preview</h1>", 3_300), path: DOCS_HTML },
};

/** The daemon's two chat file routes. */
async function stubDaemon(page: Page) {
	const bytes: string[] = [];
	await page.route("**/chat/files/resolve", async (route) => {
		const { refs } = route.request().postDataJSON() as { refs: string[] };
		await route.fulfill({
			json: {
				files: refs.map((ref) => {
					const f = FILES[ref];
					const matches = f
						? [
								{
									root: f.root,
									path: f.path,
									kind: f.kind,
									mime: f.mime,
									size: f.size,
									modifiedAt: "2026-10-31T10:00:00Z",
								},
							]
						: [];
					return { ref, matches };
				}),
			},
		});
	});
	await page.route(/\/chat\/files\/(worktree|knowledge)\//, async (route) => {
		const { pathname } = new URL(route.request().url());
		const path = decodeURIComponent(pathname.split(/\/chat\/files\/(?:worktree|knowledge)\//)[1]);
		bytes.push(path);
		const f = Object.values(FILES).find((x) => x.path === path);
		if (!f) return route.fulfill({ status: 404, json: { code: "CHAT_FILE_NOT_FOUND" } });
		return route.fulfill({ status: 200, contentType: f.mime, body: Buffer.from(f.body) });
	});
	return bytes;
}

for (const viewport of VIEWPORTS) {
	test.describe(`chat documents at ${viewport.width}x${viewport.height}`, () => {
		test.use({ viewport, colorScheme: "dark" });

		const shot = (page: Page, info: { outputPath: (name: string) => string }, name: string) =>
			page.screenshot({
				path: SHOTS ? `${SHOTS}/${name}-${viewport.width}.png` : info.outputPath(`${name}-${viewport.width}.png`),
			});

		test("draws a card for each confirmed document, opens Markdown and JSON in the Document tab, and links an artifact out", async ({
			page,
		}, info) => {
			const fetched = await stubDaemon(page);
			await page.goto(SESSION);
			const stream = page.getByTestId("chat-stream");
			await expect(stream).toBeVisible();

			const strip = stream.getByTestId("chat-docs");
			await expect(strip.getByRole("group")).toHaveCount(8);
			await expect(
				strip.getByRole("group").evaluateAll((els) => els.map((e) => e.getAttribute("aria-label"))),
			).resolves.toEqual([
				"plan.md",
				"demo--survey.md",
				"batch-3-response.json",
				"export.log",
				"rate-limit.patch",
				"billing-export-spec.pdf",
				"preview.html",
				"Demo design system",
			]);
			await expect(stream.locator("code", { hasText: DOCS_MISSING })).toBeVisible();
			await expect(strip.getByText(DOCS_MISSING)).toHaveCount(0);
			await strip.scrollIntoViewIfNeeded();
			await shot(page, info, "after-cards");

			const link = strip.getByRole("link", { name: /Open in browser/ });
			await expect(link).toHaveAttribute("href", DOCS_ARTIFACT_URL);
			await expect(link).toHaveAttribute("target", "_blank");
			await expect(strip.getByText(/needs your claude\.ai login/)).toBeVisible();

			const inspector = page.getByRole("complementary", { name: "Session inspector" });
			await strip.getByRole("button", { name: "Open demo--survey.md" }).click();
			await expect(inspector.getByRole("tab", { name: "Document" })).toHaveAttribute("aria-selected", "true");
			await expect(inspector.getByRole("heading", { name: "demo--survey.md" })).toBeVisible();
			await expect(inspector.getByRole("heading", { name: "Chat attachments survey" })).toBeVisible();
			await expect(inspector.getByRole("heading", { name: "PR plan" })).toBeVisible();
			await expect(inspector.getByRole("table")).toBeVisible();
			await expect(strip.getByRole("group", { name: "demo--survey.md" })).toHaveAttribute("data-open", "true");
			expect(fetched).toContain(DOCS_SURVEY);
			await shot(page, info, "after-document-md");

			await strip.getByRole("button", { name: "Open batch-3-response.json" }).click();
			await expect(inspector.getByRole("heading", { name: "batch-3-response.json" })).toBeVisible();
			const lines = inspector.locator(".chat-codebox__text");
			await expect(lines.first()).toHaveText("{");
			await expect(lines.filter({ hasText: '"code": 429' })).toBeVisible();
			await expect(inspector.locator(".chat-codebox__text span[style*='color']").first()).toBeVisible();
			await expect(inspector.getByRole("heading", { name: "Chat attachments survey" })).toHaveCount(0);
			await shot(page, info, "after-document-json");

			await inspector.getByRole("button", { name: "Close document" }).click();
			await expect(inspector.getByText(/Open a document from the chat/)).toBeVisible();
			await expect(strip.locator('[data-open="true"]')).toHaveCount(0);
		});

		test("opens a Write row's file from the row, without expanding it", async ({ page }) => {
			await stubDaemon(page);
			await page.goto(SESSION);
			const stream = page.getByTestId("chat-stream");
			await stream.getByRole("button", { name: /^Worked for/ }).click();
			const write = stream.locator('[data-tool="Write"]');
			await write.getByRole("button", { name: "Open plan.md" }).click();
			const inspector = page.getByRole("complementary", { name: "Session inspector" });
			await expect(inspector.getByRole("heading", { name: "Retry budget plan" })).toBeVisible();
			await expect(write.locator(".chat-tool__body")).toHaveCount(0);
		});

		test("shows an html and a pdf card as a notice with a link in the browser build, and leaves the Browser tab alone", async ({
			page,
		}) => {
			await stubDaemon(page);
			await page.goto(SESSION);
			const inspector = page.getByRole("complementary", { name: "Session inspector" });
			const browserBefore = await inspector.getByRole("tab", { name: "Browser" }).getAttribute("aria-selected");

			await page.getByTestId("chat-docs").getByRole("button", { name: "Open preview.html" }).click();
			await expect(inspector.getByText(/opens in the desktop app/i)).toBeVisible();
			const open = inspector.getByRole("link", { name: /Open in browser/ });
			await expect(open).toHaveAttribute("href", /\/chat\/files\/worktree\/docs\/preview\.html$/);
			await expect(inspector.locator("iframe")).toHaveCount(0);

			await page.getByTestId("chat-docs").getByRole("button", { name: "Open billing-export-spec.pdf" }).click();
			await expect(open).toHaveAttribute("href", /\/chat\/files\/worktree\/docs\/billing-export-spec\.pdf$/);

			expect(browserBefore).toBe("false");
			await expect(inspector.getByRole("tab", { name: "Browser" })).toHaveAttribute("aria-selected", "false");
			await inspector.getByRole("tab", { name: "Browser" }).click();
			await expect(inspector.getByLabel("Browser URL")).toHaveValue("");
		});

		test("draws no card, and no error, when the daemon confirms nothing", async ({ page }, info) => {
			await page.route("**/chat/files/resolve", async (route) => {
				const { refs } = route.request().postDataJSON() as { refs: string[] };
				await route.fulfill({ json: { files: refs.map((ref) => ({ ref, matches: [] })) } });
			});
			await page.goto(SESSION);
			const stream = page.getByTestId("chat-stream");
			await expect(stream.locator('[data-row="assistant_text"]')).toContainText(DOCS_SURVEY);
			await expect(stream.getByTestId("chat-docs").getByRole("group")).toHaveCount(1);
			await expect(page.getByRole("alert")).toHaveCount(0);
			await shot(page, info, "after-unconfirmed");
		});
	});
}
