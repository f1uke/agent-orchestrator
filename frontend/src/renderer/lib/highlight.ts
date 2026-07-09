import hljs from "highlight.js/lib/core";
import bash from "highlight.js/lib/languages/bash";
import c from "highlight.js/lib/languages/c";
import cpp from "highlight.js/lib/languages/cpp";
import csharp from "highlight.js/lib/languages/csharp";
import css from "highlight.js/lib/languages/css";
import go from "highlight.js/lib/languages/go";
import java from "highlight.js/lib/languages/java";
import javascript from "highlight.js/lib/languages/javascript";
import json from "highlight.js/lib/languages/json";
import kotlin from "highlight.js/lib/languages/kotlin";
import markdown from "highlight.js/lib/languages/markdown";
import objectivec from "highlight.js/lib/languages/objectivec";
import php from "highlight.js/lib/languages/php";
import python from "highlight.js/lib/languages/python";
import ruby from "highlight.js/lib/languages/ruby";
import rust from "highlight.js/lib/languages/rust";
import scss from "highlight.js/lib/languages/scss";
import sql from "highlight.js/lib/languages/sql";
import swift from "highlight.js/lib/languages/swift";
import typescript from "highlight.js/lib/languages/typescript";
import xml from "highlight.js/lib/languages/xml";
import yaml from "highlight.js/lib/languages/yaml";

const LANGS: Record<string, unknown> = {
	bash,
	c,
	cpp,
	csharp,
	css,
	go,
	java,
	javascript,
	json,
	kotlin,
	markdown,
	objectivec,
	php,
	python,
	ruby,
	rust,
	scss,
	sql,
	swift,
	typescript,
	xml,
	yaml,
};
for (const [name, def] of Object.entries(LANGS)) hljs.registerLanguage(name, def as never);

// File extension → registered hljs language name.
const EXT_LANG: Record<string, string> = {
	swift: "swift",
	ts: "typescript",
	tsx: "typescript",
	mts: "typescript",
	cts: "typescript",
	js: "javascript",
	jsx: "javascript",
	mjs: "javascript",
	cjs: "javascript",
	go: "go",
	py: "python",
	rb: "ruby",
	java: "java",
	kt: "kotlin",
	kts: "kotlin",
	m: "objectivec",
	mm: "objectivec",
	h: "cpp",
	hpp: "cpp",
	c: "c",
	cc: "cpp",
	cpp: "cpp",
	cxx: "cpp",
	cs: "csharp",
	rs: "rust",
	php: "php",
	json: "json",
	yaml: "yaml",
	yml: "yaml",
	sh: "bash",
	bash: "bash",
	zsh: "bash",
	sql: "sql",
	html: "xml",
	xml: "xml",
	css: "css",
	scss: "scss",
	sass: "scss",
	md: "markdown",
	markdown: "markdown",
};

export function languageForPath(path: string): string | null {
	const base = path.split("/").pop() ?? "";
	if (!base.includes(".")) return null;
	const ext = base.split(".").pop()?.toLowerCase() ?? "";
	return EXT_LANG[ext] ?? null;
}

function escapeHtml(s: string): string {
	return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

// Returns HTML-safe markup for one code line. Both branches escape, so the
// result is safe for dangerouslySetInnerHTML. Per-line: multi-line tokens
// (block comments, multi-line strings) do not carry across lines.
export function highlightLine(text: string, language: string | null): string {
	if (!language) return escapeHtml(text);
	try {
		return hljs.highlight(text, { language, ignoreIllegals: true }).value;
	} catch {
		return escapeHtml(text);
	}
}
