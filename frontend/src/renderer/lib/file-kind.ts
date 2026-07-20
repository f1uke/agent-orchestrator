/**
 * Coarse file-type buckets, used to pick a row icon in the file tree.
 *
 * Deliberately a handful of buckets rather than one icon per language: at the
 * rail's 11px icon size a Go gopher and a Swift bird are the same smudge, and
 * what actually helps a reader scan is "code vs config vs docs vs asset".
 */
export type FileKind = "code" | "data" | "doc" | "style" | "image" | "config" | "other";

const BY_EXTENSION: Record<string, FileKind> = {
	// code
	ts: "code",
	tsx: "code",
	js: "code",
	jsx: "code",
	mjs: "code",
	cjs: "code",
	go: "code",
	swift: "code",
	py: "code",
	rb: "code",
	rs: "code",
	java: "code",
	kt: "code",
	c: "code",
	h: "code",
	cc: "code",
	cpp: "code",
	hpp: "code",
	cs: "code",
	php: "code",
	sh: "code",
	bash: "code",
	zsh: "code",
	sql: "code",
	// structured data
	json: "data",
	yaml: "data",
	yml: "data",
	toml: "data",
	xml: "data",
	csv: "data",
	proto: "data",
	// prose
	md: "doc",
	mdx: "doc",
	txt: "doc",
	rst: "doc",
	adoc: "doc",
	// styling and markup
	css: "style",
	scss: "style",
	sass: "style",
	less: "style",
	html: "style",
	vue: "style",
	svelte: "style",
	// assets
	png: "image",
	jpg: "image",
	jpeg: "image",
	gif: "image",
	svg: "image",
	webp: "image",
	ico: "image",
	avif: "image",
	pdf: "image",
};

/** Dotfiles and bare names that are configuration regardless of extension. */
const CONFIG_NAMES = new Set([
	"dockerfile",
	"makefile",
	"procfile",
	"gemfile",
	"rakefile",
	"go.mod",
	"go.sum",
	"package.json",
	"package-lock.json",
	"pnpm-lock.yaml",
	"tsconfig.json",
	"flake.nix",
	"flake.lock",
]);

/** Which bucket a path belongs to, from its file name alone. */
export function fileKindFor(path: string): FileKind {
	const name = path.slice(path.lastIndexOf("/") + 1).toLowerCase();
	if (CONFIG_NAMES.has(name)) return "config";
	// A leading dot means the "extension" is really the whole name (.gitignore),
	// and those are configuration.
	if (name.startsWith(".")) return "config";
	const dot = name.lastIndexOf(".");
	if (dot <= 0) return "other";
	return BY_EXTENSION[name.slice(dot + 1)] ?? "other";
}
