import { expect, test } from "@playwright/test";

const DESCRIPTION =
	"รันเทสต์ทั้งหมดของแพ็กเกจ webhook พร้อมตรวจ race condition แล้วรวมผลลัพธ์ของทุกเคสไว้ในไฟล์เดียวเพื่อให้ตรวจย้อนหลังได้ง่ายขึ้น จากนั้นเปรียบเทียบเวลาที่ใช้ของแต่ละเคสกับรอบก่อนหน้าเพื่อหาเคสที่ช้าลงผิดปกติและสรุปให้อ่านง่ายในตอนท้าย";

const VIEWPORTS = [
	{ width: 1920, height: 1080 },
	{ width: 2560, height: 1440 },
];

for (const viewport of VIEWPORTS) {
	test.describe(`live activity of a folded group at ${viewport.width}x${viewport.height}`, () => {
		test.use({ viewport, colorScheme: "dark" });

		test("names the running command in the folded header, and again on the in-flight line", async ({ page }, info) => {
			await page.goto("/#/projects/ao-demo/sessions/demo-chat-activity");
			const stream = page.getByTestId("chat-stream");
			const head = stream.locator("[data-group] .chat-live-row");
			await expect(head).toHaveCount(1);

			const verb = head.locator(".chat-live__verb");
			await expect(verb).toHaveText(DESCRIPTION);
			await expect(head.locator(".chat-live__detail")).toHaveCount(0);
			await expect(head.locator(".chat-live__time")).toHaveText(/^\s*\d+(s|m \d+s)$/);

			const box = (locator: typeof head) => locator.evaluate((el) => el.getBoundingClientRect().toJSON());
			const headBox = await box(head);
			const verbBox = await box(verb);
			const timeBox = await box(head.locator(".chat-live__time"));
			const chevBox = await box(head.locator(".chat-chev"));
			expect(
				await verb.evaluate((el) => el.scrollWidth > el.clientWidth),
				"a long description is cut, not wrapped",
			).toBe(true);
			expect(verbBox.height, "one line").toBeLessThan(24);
			expect(timeBox.right).toBeLessThanOrEqual(headBox.right);
			expect(chevBox.right).toBeLessThanOrEqual(headBox.right + 1);
			expect(verbBox.right).toBeLessThanOrEqual(timeBox.left + 1);
			await page.screenshot({ path: info.outputPath(`chat-live-activity-folded-${viewport.width}.png`) });

			await head.click();
			await expect(head).toHaveAttribute("aria-expanded", "true");
			const line = stream.locator("[data-group] .chat-group__running");
			await expect(line).toHaveCount(1);
			await expect(line.locator(".chat-live__verb")).toHaveText(DESCRIPTION);
			const lineBox = await box(line);
			expect(lineBox.height, "one line").toBeLessThan(24);
			await page.screenshot({ path: info.outputPath(`chat-live-activity-open-${viewport.width}.png`) });
		});
	});
}
