import { describe, expect, it } from "vitest";
import {
	MAX_PROMPT_CHARS,
	parseWidget,
	parseWidgetMessage,
	widgetDocument,
	type WidgetSpec,
	widgetTheme,
} from "./chat-widget";

const THEME = { mode: "dark" as const, vars: { "--text-primary": "#f4f5f7", "--surface-0": "#0a0b0d" } };
const spec = (over: Partial<WidgetSpec> = {}): WidgetSpec => ({
	title: "t",
	code: "<div id=x>hi</div>",
	mode: "html",
	loading: [],
	...over,
});
const parsed = (html: string) => new DOMParser().parseFromString(html, "text/html");

describe("parseWidget", () => {
	it("reads the call input and calls code that starts with <svg an svg, whatever its case or leading space", () => {
		expect(
			parseWidget({ title: "flow", widget_code: "  <SVG viewBox='0 0 1 1'></SVG>", loading_messages: ["a", "b"] }),
		).toEqual({
			title: "flow",
			code: "  <SVG viewBox='0 0 1 1'></SVG>",
			mode: "svg",
			loading: ["a", "b"],
		});
		expect(parseWidget({ title: "calc", widget_code: "<div><svg></svg></div>" })?.mode).toBe("html");
	});

	it("yields nothing for a call with no code to draw", () => {
		for (const input of [
			undefined,
			null,
			"x",
			[],
			{},
			{ widget_code: "" },
			{ widget_code: " \n" },
			{ widget_code: 3 },
		]) {
			expect(parseWidget(input)).toBeUndefined();
		}
	});

	it("keeps only string loading messages, at most four", () => {
		const loading = parseWidget({ widget_code: "<p>x", loading_messages: ["a", 2, "", "b", "c", "d", "e"] })?.loading;
		expect(loading).toEqual(["a", "b", "c", "d"]);
	});
});

describe("widgetDocument", () => {
	it("puts the CSP meta first in the head, ahead of the style, the bootstrap and the widget code", () => {
		const doc = parsed(widgetDocument(spec({ code: "<script>window.x=1</script><p>body</p>" }), THEME));
		const first = doc.head.firstElementChild!;
		expect(first.tagName).toBe("META");
		expect(first.getAttribute("http-equiv")).toBe("Content-Security-Policy");
		expect(doc.head.querySelector("script")).not.toBeNull();
		expect(doc.body.querySelector("p")?.textContent).toBe("body");
	});

	it("allows the four CDNs and fonts, and blocks loopback and every other host", () => {
		const csp = parsed(widgetDocument(spec(), THEME)).head.querySelector("meta")!.getAttribute("content")!;
		const directive = (name: string) =>
			csp
				.split(";")
				.map((d) => d.trim())
				.find((d) => d.startsWith(`${name} `))!;
		for (const host of ["cdnjs.cloudflare.com", "cdn.jsdelivr.net", "unpkg.com", "esm.sh"]) {
			expect(directive("script-src")).toContain(`https://${host}`);
			expect(directive("connect-src")).toContain(`https://${host}`);
		}
		expect(directive("font-src")).toContain("https://fonts.gstatic.com");
		expect(directive("style-src")).toContain("https://fonts.googleapis.com");
		expect(csp).toContain("default-src 'none'");
		expect(csp).toContain("frame-src 'none'");
		expect(csp).toContain("form-action 'none'");
		expect(csp).toContain("base-uri 'none'");
		expect(csp).not.toMatch(/127\.0\.0\.1|localhost|\[::1\]|http:|\*|'self'/);
		expect(directive("connect-src")).not.toContain("data:");
	});

	it("sets the theme on the root and exposes its variables", () => {
		const doc = parsed(widgetDocument(spec(), { mode: "light", vars: THEME.vars }));
		expect(doc.documentElement.getAttribute("data-mode")).toBe("light");
		const style = doc.head.querySelector("style")!.textContent!;
		expect(style).toContain("--text-primary:#f4f5f7");
		expect(style).toContain("color-scheme:light");
	});

	it("exposes every variable the widget guide names, the numbered surfaces included", () => {
		const names = ["--text-primary", "--text-secondary", "--text-muted", "--surface-0", "--surface-1", "--surface-2"];
		const vars = Object.fromEntries(names.map((n, i) => [n, `#00000${i}`]));
		const style = parsed(widgetDocument(spec(), { mode: "dark", vars })).head.querySelector("style")!.textContent!;
		for (const [name, value] of Object.entries(vars)) expect(style).toContain(`${name}:${value}`);
	});

	it("lets the bootstrap apply a theme message to a numbered variable too", () => {
		const script = parsed(widgetDocument(spec(), THEME)).head.querySelector("script")!.textContent!;
		const guard = /if \((\/.+\/)\.test\(name\)\)/.exec(script);
		expect(new RegExp(guard![1].slice(1, -1)).test("--surface-0")).toBe(true);
	});

	it("drops a theme value that could close the style rule or element", () => {
		const style = parsed(
			widgetDocument(spec(), {
				mode: "dark",
				vars: { "--accent": "red;}</style><script>x()</script>", "--ok": "blue" },
			}),
		).head.querySelector("style")!.textContent!;
		expect(style).toContain("--ok:blue");
		expect(style).not.toContain("--accent");
		expect(parsed(widgetDocument(spec(), THEME)).querySelectorAll("script")).toHaveLength(1);
	});

	it("wraps svg code in a centring box and leaves html code as it is", () => {
		const svg = parsed(widgetDocument(spec({ mode: "svg", code: "<svg><circle r='1'/></svg>" }), THEME));
		expect(svg.body.querySelector(".ao-svg > svg")).not.toBeNull();
		const html = parsed(widgetDocument(spec({ code: "<button id=b>Go</button>" }), THEME));
		expect(html.body.firstElementChild?.id).toBe("b");
	});

	it("keeps widget code out of any script string or attribute", () => {
		const code = `<p title="a&quot;b">"quoted" </script> text</p>`;
		const doc = parsed(widgetDocument(spec({ code }), THEME));
		expect(doc.body.querySelector("p")?.getAttribute("title")).toBe('a"b');
		expect(doc.head.querySelector("script")!.textContent).not.toContain("quoted");
	});
});

describe("widgetTheme", () => {
	it("reads the app tokens once and maps them to the names the widget guide gives", () => {
		const root = document.createElement("div");
		document.body.append(root);
		root.style.setProperty("--fg", "#111111");
		root.style.setProperty("--bg-1", "#ffffff");
		root.style.setProperty("--green", "#1a7f37");
		const theme = widgetTheme(root, "light");
		expect(theme.mode).toBe("light");
		expect(theme.vars["--text-primary"]).toBe("#111111");
		expect(theme.vars["--surface-1"]).toBe("#ffffff");
		expect(theme.vars["--success"]).toBe("#1a7f37");
		expect(theme.vars["--font-sans"]).not.toBe("");
		expect(theme.vars["--radius"]).toBe("8px");
		expect(theme.vars["--surface-0"]).toBeUndefined();
		root.remove();
	});
});

describe("parseWidgetMessage", () => {
	it("reads a prompt, trimmed", () => {
		expect(parseWidgetMessage({ type: "ao-widget/prompt", text: "  Explain why  " })).toEqual({
			type: "prompt",
			text: "Explain why",
		});
	});

	it("cuts an over-long prompt instead of rejecting it, and rejects an empty or non-text one", () => {
		const long = parseWidgetMessage({ type: "ao-widget/prompt", text: "x".repeat(MAX_PROMPT_CHARS + 500) });
		expect(long).toEqual({ type: "prompt", text: "x".repeat(MAX_PROMPT_CHARS) });
		expect(parseWidgetMessage({ type: "ao-widget/prompt", text: "   " })).toBeUndefined();
		expect(parseWidgetMessage({ type: "ao-widget/prompt", text: 4 })).toBeUndefined();
		expect(parseWidgetMessage({ type: "ao-widget/prompt" })).toBeUndefined();
	});

	it("clamps a height to 40..4000 and rejects one that is not a finite number", () => {
		expect(parseWidgetMessage({ type: "ao-widget/size", height: 320 })).toEqual({ type: "size", height: 320 });
		expect(parseWidgetMessage({ type: "ao-widget/size", height: 2 })).toEqual({ type: "size", height: 40 });
		expect(parseWidgetMessage({ type: "ao-widget/size", height: 1e9 })).toEqual({ type: "size", height: 4000 });
		for (const height of [Number.NaN, Number.POSITIVE_INFINITY, "300", null, undefined]) {
			expect(parseWidgetMessage({ type: "ao-widget/size", height })).toBeUndefined();
		}
	});

	it("drops anything that is not one of the two requests, including the theme message a frame only receives", () => {
		for (const data of [
			null,
			"ao-widget/prompt",
			[],
			{},
			{ type: "ao-widget/theme", mode: "dark" },
			{ type: "x", text: "y" },
		]) {
			expect(parseWidgetMessage(data)).toBeUndefined();
		}
	});
});
