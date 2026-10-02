import { expect, type Page, test } from "@playwright/test";
import { MESSY_GO, MESSY_SWIFT } from "./editor-formatting-fixtures";

/**
 * Formatting in the code editor, driven with real key presses in a real
 * browser: Return and a closing bracket indenting as you type, a paste
 * re-indented to fit, ⌃I re-indenting a selection, ⌃⇧I formatting the
 * document - and ⌘Z taking each back in ONE step.
 *
 * Browser-only for the same reason the other editor specs are: every assertion
 * is about what Monaco's model holds after a keystroke, and when its undo step
 * closes. The formatter itself is a fake (`editor-gallery-format-stub.ts`) -
 * the gallery has no main process to run gofmt - but everything the editor
 * does with a formatter's answer is the shipped code.
 */

const GO = "/e2e/editor-gallery.html?width=1240&line=1&path=messy.go";
const SWIFT = "/e2e/editor-gallery.html?width=1240&line=1&path=Sources/Messy.swift";

/** Lines 5-13 of MESSY_GO, as gofmt indents them. */
const GO_REINDENTED = [
	"func messy(xs []int) int{",
	"\ttotal := 0",
	"\tfor _, x := range xs {",
	"\t\tif x > 0 {",
	"\t\t\ttotal += x",
	"\t\t}",
	"\t}",
	"\treturn total",
	"}",
];

async function open(page: Page, url: string): Promise<void> {
	await page.goto(url);
	await expect(page.getByTestId("monaco-file-editor")).toHaveAttribute("data-editable", "true");
	await expect.poll(() => text(page)).not.toBe("");
}

/** The buffer, as the editor's model holds it. */
function text(page: Page): Promise<string> {
	return page.evaluate(() => {
		const monaco = (window as unknown as { __monaco: typeof import("monaco-editor") }).__monaco;
		return (
			monaco.editor
				.getModels()
				.find((m) => m.uri.scheme === "ao-file")
				?.getValue() ?? ""
		);
	});
}

async function line(page: Page, n: number): Promise<string> {
	return (await text(page)).split("\n")[n - 1];
}

/** Put the caret at the end of a 1-based line, through a real click on the editor first. */
async function caretAtEnd(page: Page, n: number): Promise<void> {
	await page.locator(".monaco-editor .view-lines").first().click();
	await expect(page.locator(".monaco-editor.focused").first()).toBeVisible();
	await page.evaluate((n) => {
		const monaco = (window as unknown as { __monaco: typeof import("monaco-editor") }).__monaco;
		const editor = monaco.editor.getEditors()[0];
		const model = editor.getModel();
		if (!model) throw new Error("no model");
		editor.setPosition({ lineNumber: n, column: model.getLineMaxColumn(n) });
	}, n);
}

async function selectLines(page: Page, from: number, to: number): Promise<void> {
	await caretAtEnd(page, from);
	await page.evaluate(
		([from, to]) => {
			const monaco = (window as unknown as { __monaco: typeof import("monaco-editor") }).__monaco;
			const editor = monaco.editor.getEditors()[0];
			const model = editor.getModel();
			if (!model) throw new Error("no model");
			editor.setSelection(new monaco.Selection(from, 1, to, model.getLineMaxColumn(to)));
		},
		[from, to],
	);
}

test.describe("indenting as you type", () => {
	test("Go: Return indents one tab inside a block, splits a brace pair, and } finds its opener", async ({ page }) => {
		await open(page, GO);
		await caretAtEnd(page, 16);
		await page.keyboard.press("Enter");
		await page.keyboard.type("if ok {");
		await page.keyboard.press("Enter");
		// Monaco closed the brace; Return split the pair, and the caret line is one in.
		expect(await line(page, 18)).toBe("\t\t");
		expect(await line(page, 19)).toBe("\t}");
		await page.keyboard.type("x()");
		await page.keyboard.press("Enter");
		await page.keyboard.type("}");
		// Typed one level too deep, and moved back to the `if` it closes.
		expect(await line(page, 19)).toBe("\t}");
	});

	test("Return and its indentation are ONE undo step", async ({ page }) => {
		await open(page, GO);
		await caretAtEnd(page, 15);
		await page.keyboard.press("Enter");
		expect(await line(page, 16)).toBe("\t");
		await page.keyboard.press("ControlOrMeta+KeyZ");
		expect(await text(page)).toBe(MESSY_GO);
	});

	test("Swift: four spaces in, and the closing brace back out", async ({ page }) => {
		await open(page, SWIFT);
		await caretAtEnd(page, 10);
		await page.keyboard.press("Enter");
		await page.keyboard.type("if items.count > 3 {");
		await page.keyboard.press("Enter");
		expect(await line(page, 12)).toBe("            ");
		await page.keyboard.type("items.removeFirst()");
		await page.keyboard.press("Enter");
		await page.keyboard.type("}");
		expect(await line(page, 13)).toBe("        }");
	});

	test("a pasted block is re-indented to fit, and ⌘Z gives back the paste as it was", async ({ page }) => {
		await open(page, SWIFT);
		await caretAtEnd(page, 10);
		await page.keyboard.press("Enter");
		const pasted = 'if items.isEmpty {\nprint("empty")\n}';
		await page.evaluate((pasted) => {
			const monaco = (window as unknown as { __monaco: typeof import("monaco-editor") }).__monaco;
			// Monaco's own paste command - the path a ⌘V takes once the clipboard
			// has been read - so onDidPaste fires exactly as it does for a person.
			monaco.editor.getEditors()[0].trigger("keyboard", "paste", { text: pasted });
		}, pasted);
		expect((await text(page)).split("\n").slice(10, 13)).toEqual([
			"        if items.isEmpty {",
			'            print("empty")',
			"        }",
		]);
		await page.keyboard.press("ControlOrMeta+KeyZ");
		expect((await text(page)).split("\n").slice(10, 13)).toEqual(["        if items.isEmpty {", 'print("empty")', "}"]);
	});
});

test.describe("⌃I re-indent", () => {
	test("re-indents a messy Go selection as gofmt would, and one ⌘Z restores it", async ({ page }) => {
		await open(page, GO);
		await selectLines(page, 5, 13);
		await page.keyboard.press("Control+KeyI");
		expect((await text(page)).split("\n").slice(4, 13)).toEqual(GO_REINDENTED);
		// Nothing outside the selection moved.
		expect((await text(page)).split("\n").slice(14)).toEqual(MESSY_GO.split("\n").slice(14));
		await page.keyboard.press("ControlOrMeta+KeyZ");
		expect(await text(page)).toBe(MESSY_GO);
	});

	test("re-indents just the caret's line when nothing is selected", async ({ page }) => {
		await open(page, SWIFT);
		await caretAtEnd(page, 7);
		await page.keyboard.press("Control+KeyI");
		expect(await line(page, 7)).toBe("        guard !item.isEmpty else {");
		expect(await line(page, 8)).toBe("return");
	});

	test("re-indents a messy Swift method", async ({ page }) => {
		await open(page, SWIFT);
		await selectLines(page, 6, 11);
		await page.keyboard.press("Control+KeyI");
		expect((await text(page)).split("\n").slice(5, 12)).toEqual([
			"    func add(_ item: String) {",
			"        guard !item.isEmpty else {",
			"            return",
			"        }",
			"        items.append(item)",
			"    }",
			"",
		]);
	});
});

test("the right-click menu offers both, labelled with their shortcuts, and runs them", async ({ page }) => {
	await open(page, GO);
	await selectLines(page, 5, 13);
	// Inside the selection, or the right-click moves the caret and drops it.
	const at = await page.evaluate(() => {
		const monaco = (window as unknown as { __monaco: typeof import("monaco-editor") }).__monaco;
		const editor = monaco.editor.getEditors()[0];
		const pos = editor.getScrolledVisiblePosition({ lineNumber: 7, column: 4 });
		const box = editor.getDomNode()?.getBoundingClientRect();
		if (!pos || !box) throw new Error("line 7 is not on screen");
		return { x: box.left + pos.left + 2, y: box.top + pos.top + pos.height / 2 };
	});
	await page.mouse.click(at.x, at.y, { button: "right" });
	const item = page.locator(".action-item", { hasText: "Re-Indent" });
	await expect(item).toBeVisible();
	await expect(page.locator(".action-item", { hasText: "Format Document" })).toBeVisible();
	await expect(item.locator(".keybinding")).toHaveText(/I/);
	// Hover gives the item the menu's focus; Return runs it, as from the keyboard.
	await item.hover();
	await page.keyboard.press("Enter");
	await expect.poll(async () => (await text(page)).split("\n").slice(4, 13)).toEqual(GO_REINDENTED);
});

test.describe("⌃⇧I format document", () => {
	test("hands the file to its formatter and applies the answer as one undo step", async ({ page }) => {
		await open(page, `${GO}&format=ok`);
		await caretAtEnd(page, 16);
		await page.keyboard.press("Control+Shift+KeyI");
		await expect.poll(() => line(page, 5)).toBe("func messy(xs []int) int {");
		expect((await text(page)).split("\n").slice(5, 13)).toEqual(GO_REINDENTED.slice(1));
		const asked = await page.evaluate(
			() => (window as unknown as { __aoFormatAsked: { languageId: string; filePath: string }[] }).__aoFormatAsked,
		);
		expect(asked).toHaveLength(1);
		expect(asked[0]).toMatchObject({ languageId: "go", filePath: "/gallery/workspace/messy.go" });
		await page.keyboard.press("ControlOrMeta+KeyZ");
		expect(await text(page)).toBe(MESSY_GO);
	});

	test("a formatter that refuses leaves the buffer untouched and says why at the caret", async ({ page }) => {
		await open(page, `${GO}&format=fail`);
		await caretAtEnd(page, 16);
		await page.keyboard.press("Control+Shift+KeyI");
		await expect(page.locator(".monaco-editor-overlaymessage")).toContainText(
			"Couldn't format with gofmt: messy.go:9:1: expected '}', found 'EOF'",
		);
		expect(await text(page)).toBe(MESSY_GO);
	});

	test("with no formatter to run, a bracket language is re-indented and the caret says so", async ({ page }) => {
		await open(page, SWIFT);
		await caretAtEnd(page, 3);
		await page.keyboard.press("Control+Shift+KeyI");
		await expect(page.locator(".monaco-editor-overlaymessage")).toContainText("Re-indented.");
		expect(await line(page, 7)).toBe("        guard !item.isEmpty else {");
		await page.keyboard.press("ControlOrMeta+KeyZ");
		expect(await text(page)).toBe(MESSY_SWIFT);
	});

	test("format on save formats first", async ({ page }) => {
		await open(page, `${GO}&format=ok&formatOnSave=1`);
		await caretAtEnd(page, 16);
		await page.keyboard.type(" ");
		await page.keyboard.press("ControlOrMeta+KeyS");
		await expect.poll(() => line(page, 5)).toBe("func messy(xs []int) int {");
		const asked = await page.evaluate(
			() => (window as unknown as { __aoFormatAsked: unknown[] }).__aoFormatAsked.length,
		);
		expect(asked).toBe(1);
	});
});
