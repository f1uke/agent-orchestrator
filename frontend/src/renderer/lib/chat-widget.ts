import { isObject } from "./chat-event";
import type { ToolCallItem } from "./chat-fold";

export const SHOW_WIDGET = "mcp__ao__show_widget";
export const READ_ME = "mcp__ao__read_me";

export type WidgetSpec = { title: string; code: string; mode: "svg" | "html"; loading: string[] };

const MAX_LOADING_MESSAGES = 4;

/** The visual a `show_widget` call carries, or undefined when it has no code to draw. */
export function parseWidget(input: unknown): WidgetSpec | undefined {
	if (!isObject(input) || typeof input.widget_code !== "string" || input.widget_code.trim() === "") return undefined;
	const code = input.widget_code;
	const loading = Array.isArray(input.loading_messages)
		? input.loading_messages.filter((m): m is string => typeof m === "string" && m.trim() !== "")
		: [];
	return {
		title: typeof input.title === "string" ? input.title : "",
		code,
		mode: /^<svg/i.test(code.trim()) ? "svg" : "html",
		loading: loading.slice(0, MAX_LOADING_MESSAGES),
	};
}

/** The visual to draw for a call, or undefined when the call is not one, failed, or was refused. */
export function widgetOf(call: ToolCallItem): WidgetSpec | undefined {
	if (call.name !== SHOW_WIDGET || call.result?.isError) return undefined;
	if (call.permission?.status === "denied" || call.permission?.status === "cancelled") return undefined;
	return parseWidget(call.input);
}

export type WidgetMode = "dark" | "light";

/** The CSS variables a widget sees (names from the widget guide), each resolved to a colour or length. */
export type WidgetTheme = { mode: WidgetMode; vars: Record<string, string> };

// App token behind each variable the widget guide names. Fonts and radius are
// declared in an inline @theme, which may not be emitted as variables, so they fall back.
const TOKEN: ReadonlyArray<readonly [widgetVar: string, appVar: string, fallback: string]> = [
	["--text-primary", "--fg", ""],
	["--text-secondary", "--fg-muted", ""],
	["--text-muted", "--fg-passive", ""],
	["--surface-0", "--bg", ""],
	["--surface-1", "--bg-1", ""],
	["--surface-2", "--bg-2", ""],
	["--border", "--border-1", ""],
	["--accent", "--accent", ""],
	["--success", "--green", ""],
	["--danger", "--red", ""],
	["--warning", "--amber", ""],
	["--font-sans", "--font-sans", "-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif"],
	["--font-mono", "--font-mono", "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace"],
	["--radius", "--radius-lg", "8px"],
];

/** Reads the app's resolved tokens off `root` once. Variables the app does not define are left out. */
export function widgetTheme(root: HTMLElement, mode: WidgetMode): WidgetTheme {
	const style = getComputedStyle(root);
	const vars: Record<string, string> = {};
	for (const [name, source, fallback] of TOKEN) {
		const value = style.getPropertyValue(source).trim() || fallback;
		if (value !== "") vars[name] = value;
	}
	const ink = vars["--text-primary"];
	if (ink) vars["--border-strong"] = `color-mix(in srgb, ${ink} 28%, transparent)`;
	return { mode, vars };
}

const CDN = "https://cdnjs.cloudflare.com https://cdn.jsdelivr.net https://unpkg.com https://esm.sh";

// Loopback and every host outside the four CDNs (and Google Fonts) stay blocked:
// the daemon listens on 127.0.0.1 and a widget must not reach it.
const WIDGET_CSP = [
	"default-src 'none'",
	`script-src 'unsafe-inline' ${CDN}`,
	`style-src 'unsafe-inline' ${CDN} https://fonts.googleapis.com`,
	`font-src https://fonts.gstatic.com ${CDN}`,
	`img-src data: blob: ${CDN}`,
	`connect-src ${CDN}`,
	"base-uri 'none'",
	"form-action 'none'",
	"frame-src 'none'",
].join("; ");

const VAR_NAME = /^--[a-z-]+$/;

// A theme value ends up inside a <style> rule, so it may not close the rule or the element.
const safeCssValue = (value: string) => !/[<>{};]/.test(value);

function cssVars(vars: Readonly<Record<string, string>>): string {
	return Object.entries(vars)
		.filter(([name, value]) => VAR_NAME.test(name) && safeCssValue(value))
		.map(([name, value]) => `${name}:${value}`)
		.join(";");
}

// Runs before the widget code. The frame's opaque origin means the only way out
// is postMessage to the parent, and the only message it accepts comes from the parent.
const BOOTSTRAP = `
window.sendPrompt = function (text) {
  parent.postMessage({ type: "ao-widget/prompt", text: String(text) }, "*");
};
new ResizeObserver(function () {
  var height = Math.ceil(document.documentElement.getBoundingClientRect().height);
  parent.postMessage({ type: "ao-widget/size", height: height }, "*");
}).observe(document.documentElement);
window.addEventListener("message", function (event) {
  var data = event.data;
  if (event.source !== parent || !data || data.type !== "ao-widget/theme") return;
  var root = document.documentElement;
  root.setAttribute("data-mode", data.mode === "light" ? "light" : "dark");
  var vars = data.vars || {};
  Object.keys(vars).forEach(function (name) {
    if (/^--[a-z-]+$/.test(name)) root.style.setProperty(name, String(vars[name]));
  });
});
`;

/** The `srcdoc` of a widget's frame: CSP first, then the theme, then the bridge, then the widget's own code. */
export function widgetDocument(spec: WidgetSpec, theme: WidgetTheme): string {
	const body = spec.mode === "svg" ? `<div class="ao-svg">${spec.code}</div>` : spec.code;
	return [
		"<!doctype html>",
		`<html data-mode="${theme.mode}">`,
		"<head>",
		`<meta http-equiv="Content-Security-Policy" content="${WIDGET_CSP}">`,
		`<style>:root{${cssVars(theme.vars)}}`,
		":root[data-mode=dark]{color-scheme:dark}:root[data-mode=light]{color-scheme:light}",
		"html,body{margin:0;background:transparent}",
		"body{font-family:var(--font-sans);font-size:14px;line-height:1.5;color:var(--text-primary)}",
		".ao-svg{display:flex;justify-content:center}.ao-svg>svg{max-width:100%;height:auto}</style>",
		`<script>${BOOTSTRAP}</script>`,
		"</head>",
		`<body>${body}</body>`,
		"</html>",
	].join("\n");
}

export type WidgetMessage = { type: "prompt"; text: string } | { type: "size"; height: number };

export const MAX_PROMPT_CHARS = 4000;
export const MIN_WIDGET_HEIGHT = 40;
export const MAX_WIDGET_HEIGHT = 4000;

/** What a frame may ask of the chat. Anything that is not exactly one of these two requests is dropped. */
export function parseWidgetMessage(data: unknown): WidgetMessage | undefined {
	if (!isObject(data)) return undefined;
	if (data.type === "ao-widget/prompt") {
		if (typeof data.text !== "string") return undefined;
		const text = data.text.trim().slice(0, MAX_PROMPT_CHARS);
		return text === "" ? undefined : { type: "prompt", text };
	}
	if (data.type === "ao-widget/size") {
		if (typeof data.height !== "number" || !Number.isFinite(data.height)) return undefined;
		return { type: "size", height: Math.min(MAX_WIDGET_HEIGHT, Math.max(MIN_WIDGET_HEIGHT, data.height)) };
	}
	return undefined;
}
