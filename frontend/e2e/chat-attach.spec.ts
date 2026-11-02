import { expect, type Page, test } from "@playwright/test";

const VIEWPORTS = [
	{ width: 1920, height: 1080 },
	{ width: 2560, height: 1440 },
];

const SAVED = "/Users/dev/.ao/sessions/demo-chat-images/attachments";
const PDF = Buffer.from("%PDF-1.4\n1 0 obj\n<<>>\nendobj\n");
const JSON_FILE = Buffer.from('{"events":[1,2,3]}');

type Upload = { name: string; type: string; body: Buffer };
type Sent = { text: string; images?: Array<{ mediaType: string; base64: string }> };

/** The preview serves an in-memory chat; these specs need the real routes, so the page talks HTTP to the stubs below. */
async function useHttpChat(page: Page) {
	await page.route("**/hooks/useChatActions.ts*", async (route) => {
		const res = await route.fetch();
		const body = (await res.text()).replace(/const defaultApi\b[^;]*;/, "const defaultApi = httpChatApi;");
		await route.fulfill({ response: res, body });
	});
}

async function stubDaemon(page: Page, behave: (name: string) => "ok" | "fail" | "hold", release: Promise<void>) {
	const uploads: Upload[] = [];
	const sent: Sent[] = [];
	await page.route("**/chat/attachments?*", async (route) => {
		const request = route.request();
		const name = new URL(request.url()).searchParams.get("name") ?? "";
		uploads.push({
			name,
			type: request.headers()["content-type"] ?? "",
			body: request.postDataBuffer() ?? Buffer.alloc(0),
		});
		const how = behave(name);
		if (how === "hold") await release;
		if (how === "fail") {
			return route.fulfill({ status: 500, json: { code: "INTERNAL", message: "disk full on the daemon" } });
		}
		const size = request.postDataBuffer()?.length ?? 0;
		return route.fulfill({ json: { path: `${SAVED}/ab12/${name}`, name, size } });
	});
	await page.route("**/chat/messages", async (route) => {
		sent.push(route.request().postDataJSON() as Sent);
		await route.fulfill({ status: 200, json: {} });
	});
	return { uploads, sent };
}

async function openComposer(page: Page) {
	await useHttpChat(page);
	await page.goto("/#/projects/ao-demo/sessions/demo-chat-images");
	await expect(page.getByTestId("chat-stream")).toBeVisible();
	// An empty base URL, not null, is what lets the daemon client send in the browser preview.
	await page.evaluate(async (module) => {
		const client = await import(/* @vite-ignore */ module);
		client.setApiBaseUrl("");
	}, "/src/renderer/lib/api-client.ts");
	const input = page.getByRole("textbox", { name: "Message Claude" });
	await expect(input).toBeVisible();
	return input;
}

async function sketchPng(page: Page): Promise<Buffer> {
	const base64 = await page.evaluate(() => {
		const canvas = document.createElement("canvas");
		canvas.width = 320;
		canvas.height = 200;
		const ctx = canvas.getContext("2d")!;
		const gradient = ctx.createLinearGradient(0, 0, 320, 200);
		gradient.addColorStop(0, "#1e2a44");
		gradient.addColorStop(1, "#2c3f2f");
		ctx.fillStyle = gradient;
		ctx.fillRect(0, 0, 320, 200);
		ctx.fillStyle = "#f2f93c";
		for (let i = 0; i < 6; i++) ctx.fillRect(30 + i * 45, 170 - i * 22, 28, i * 22 + 10);
		return canvas.toDataURL("image/png").split(",")[1];
	});
	return Buffer.from(base64, "base64");
}

for (const viewport of VIEWPORTS) {
	test.describe(`chat attachments at ${viewport.width}x${viewport.height}`, () => {
		test.use({ viewport, colorScheme: "dark" });

		test("attaches an image and a PDF, uploads the PDF, and sends both with the path in the text", async ({
			page,
		}, info) => {
			const shot = (name: string) => page.screenshot({ path: info.outputPath(`${name}-${viewport.width}.png`) });
			let release: () => void = () => undefined;
			const held = new Promise<void>((resolve) => (release = resolve));
			const daemon = await stubDaemon(page, (name) => (name === "events.json" ? "hold" : "ok"), held);
			const input = await openComposer(page);
			const picker = page.getByTestId("chat-file-input");
			const png = await sketchPng(page);

			await input.fill("Summarise the report and the events, and compare with this chart.");
			await picker.setInputFiles([
				{ name: "chart.png", mimeType: "image/png", buffer: png },
				{ name: "report.pdf", mimeType: "application/pdf", buffer: PDF },
				{ name: "events.json", mimeType: "application/json", buffer: JSON_FILE },
			]);

			const chips = page.getByTestId("chat-attachment");
			await expect(chips).toHaveCount(3);
			await expect(page.getByRole("img", { name: "chart.png" })).toBeVisible();
			const pdf = page.getByRole("group", { name: "Attachment report.pdf" });
			await expect(pdf).toHaveAttribute("data-state", "ready");
			await expect(pdf).toContainText("PDF");
			const events = page.getByRole("group", { name: "Attachment events.json" });
			await expect(events).toHaveAttribute("data-state", "uploading");
			await expect(events).toContainText("JSON");
			await expect(page.getByTestId("chat-pane").getByRole("button", { name: "Send", exact: true })).toBeDisabled();
			await shot("after-chips");

			release();
			await expect(events).toHaveAttribute("data-state", "ready");
			await expect(page.getByTestId("chat-pane").getByRole("button", { name: "Send", exact: true })).toBeEnabled();
			await input.press("Enter");
			await expect(chips).toHaveCount(0);

			expect(daemon.uploads.map((u) => u.name).sort()).toEqual(["events.json", "report.pdf"]);
			const uploaded = daemon.uploads.find((u) => u.name === "report.pdf")!;
			expect(uploaded.type).toBe("application/octet-stream");
			expect(uploaded.body.equals(PDF)).toBe(true);

			expect(daemon.sent).toHaveLength(1);
			expect(daemon.sent[0].text).toBe(
				`Summarise the report and the events, and compare with this chart.\nAttached: ${SAVED}/ab12/report.pdf\nAttached: ${SAVED}/ab12/events.json`,
			);
			expect(daemon.sent[0].images).toEqual([{ mediaType: "image/png", base64: png.toString("base64") }]);
		});

		test("a failed upload shows an error chip, holds Send until Retry works, and sends a file with no text", async ({
			page,
		}, info) => {
			let failures = 1;
			const daemon = await stubDaemon(page, () => (failures-- > 0 ? "fail" : "ok"), Promise.resolve());
			const input = await openComposer(page);

			await page.getByTestId("chat-file-input").setInputFiles({
				name: "crash.log",
				mimeType: "text/plain",
				buffer: Buffer.from("panic: nil map\n"),
			});
			const chip = page.getByRole("group", { name: "Attachment crash.log" });
			await expect(chip).toHaveAttribute("data-state", "failed");
			await expect(chip).toContainText("disk full on the daemon");
			await expect(page.getByTestId("chat-pane").getByRole("button", { name: "Send", exact: true })).toBeDisabled();
			await input.focus();
			await page.screenshot({ path: info.outputPath(`after-error-${viewport.width}.png`) });

			await page.getByRole("button", { name: "Retry crash.log" }).click();
			await expect(chip).toHaveAttribute("data-state", "ready");
			await page.getByTestId("chat-pane").getByRole("button", { name: "Send", exact: true }).click();
			await expect(page.getByTestId("chat-attachment")).toHaveCount(0);

			expect(daemon.uploads.map((u) => u.name)).toEqual(["crash.log", "crash.log"]);
			expect(daemon.sent).toEqual([{ text: `Attached: ${SAVED}/ab12/crash.log` }]);
		});
	});
}
