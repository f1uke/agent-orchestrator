import { access, readFile } from "node:fs/promises";
import path from "node:path";
import { PRETTIER_LANGUAGES } from "../../shared/formatter-languages";
import { resolveEditorConfig } from "./editorconfig";

export { PRETTIER_LANGUAGES };

/**
 * What a project says about formatting one of its files: which formatter
 * config applies, and the indentation it implies.
 *
 * 🗝 Indentation is decided in the order the formatters themselves decide it,
 * so typing and formatting never fight over the same line. swift-format
 * ignores `.editorconfig` entirely; Prettier reads it only for what its own
 * config leaves unset; gofmt uses tabs, full stop. Anything else follows
 * `.editorconfig`.
 */

export type IndentStyle = {
	/** Absent when the project names a width but not tabs-or-spaces: the file's own indentation decides. */
	insertSpaces?: boolean;
	/** Columns per level (spaces); for tabs, the level is a tab. */
	indentSize?: number;
	/** How wide a tab is drawn. */
	tabSize?: number;
	/** Which file (or convention) decided, for a tooltip or a log line. */
	source: string;
};

export type Fs = {
	readText(file: string): Promise<string | null>;
	exists(file: string): Promise<boolean>;
};

export const nodeFs: Fs = {
	async readText(file) {
		try {
			return await readFile(file, "utf8");
		} catch {
			return null;
		}
	},
	async exists(file) {
		try {
			await access(file);
			return true;
		} catch {
			return false;
		}
	},
};

const PRETTIER_CONFIG_FILES = [
	".prettierrc",
	".prettierrc.json",
	".prettierrc.yaml",
	".prettierrc.yml",
	".prettierrc.json5",
	".prettierrc.js",
	".prettierrc.cjs",
	".prettierrc.mjs",
	".prettierrc.ts",
	".prettierrc.cts",
	".prettierrc.mts",
	".prettierrc.toml",
	"prettier.config.js",
	"prettier.config.cjs",
	"prettier.config.mjs",
	"prettier.config.ts",
	"prettier.config.cts",
	"prettier.config.mts",
];

/** The directories from `fromDir` up to `stopDir` (inclusive), or to the filesystem root when it is not an ancestor. */
export function ancestors(fromDir: string, stopDir?: string): string[] {
	const dirs: string[] = [];
	const stop = stopDir ? path.resolve(stopDir) : null;
	const bounded = stop !== null && (path.resolve(fromDir) + path.sep).startsWith(stop + path.sep);
	let dir = path.resolve(fromDir);
	for (;;) {
		dirs.push(dir);
		if (bounded && dir === stop) break;
		const parent = path.dirname(dir);
		if (parent === dir) break;
		dir = parent;
	}
	return dirs;
}

/** The nearest `.swift-format`, searched up to the filesystem root the way swift-format does. */
export async function findSwiftFormatConfig(filePath: string, fs: Fs = nodeFs): Promise<string | null> {
	for (const dir of ancestors(path.dirname(filePath))) {
		const candidate = path.join(dir, ".swift-format");
		if (await fs.exists(candidate)) return candidate;
	}
	return null;
}

export type PrettierConfig = {
	path: string;
	/** The two options indentation needs, when the config is a format this can read. */
	useTabs?: boolean;
	tabWidth?: number;
};

/**
 * The project's Prettier config, if it has one. Searched only up to the
 * workspace root: "the project configures Prettier" means THIS project, not a
 * stray `.prettierrc` in a parent directory of someone's checkout.
 */
export async function findPrettierConfig(
	filePath: string,
	workspaceRoot: string | undefined,
	fs: Fs = nodeFs,
): Promise<PrettierConfig | null> {
	for (const dir of ancestors(path.dirname(filePath), workspaceRoot)) {
		for (const name of PRETTIER_CONFIG_FILES) {
			const candidate = path.join(dir, name);
			const text = await fs.readText(candidate);
			if (text === null) continue;
			return { path: candidate, ...readPrettierOptions(text) };
		}
		const pkg = await fs.readText(path.join(dir, "package.json"));
		if (pkg !== null) {
			try {
				const parsed = JSON.parse(pkg) as { prettier?: unknown };
				if (parsed.prettier !== undefined) {
					const inline = typeof parsed.prettier === "object" && parsed.prettier !== null ? parsed.prettier : {};
					return { path: path.join(dir, "package.json"), ...pickPrettierOptions(inline as Record<string, unknown>) };
				}
			} catch {
				// A package.json that does not parse configures nothing.
			}
		}
	}
	return null;
}

/** `useTabs` / `tabWidth` from a JSON or flat-YAML rc file; nothing from a JS one. */
function readPrettierOptions(text: string): { useTabs?: boolean; tabWidth?: number } {
	try {
		const json = JSON.parse(text) as unknown;
		if (typeof json === "object" && json !== null) return pickPrettierOptions(json as Record<string, unknown>);
	} catch {
		// Not JSON - try the flat YAML form below.
	}
	const yaml: Record<string, unknown> = {};
	for (const line of text.split(/\r?\n/)) {
		const match = /^(useTabs|tabWidth)\s*:\s*([^#\s]+)/.exec(line.trim());
		if (!match) continue;
		yaml[match[1]] = match[2] === "true" ? true : match[2] === "false" ? false : Number(match[2]);
	}
	return pickPrettierOptions(yaml);
}

function pickPrettierOptions(options: Record<string, unknown>): { useTabs?: boolean; tabWidth?: number } {
	const out: { useTabs?: boolean; tabWidth?: number } = {};
	if (typeof options.useTabs === "boolean") out.useTabs = options.useTabs;
	if (typeof options.tabWidth === "number" && options.tabWidth > 0) out.tabWidth = options.tabWidth;
	return out;
}

/** A project-local binary (`node_modules/.bin/<name>`), nearest first, up to the workspace root. */
export async function findLocalBin(
	name: string,
	filePath: string,
	workspaceRoot: string | undefined,
	fs: Fs = nodeFs,
): Promise<string | null> {
	for (const dir of ancestors(path.dirname(filePath), workspaceRoot)) {
		const candidate = path.join(dir, "node_modules", ".bin", name);
		if (await fs.exists(candidate)) return candidate;
	}
	return null;
}

function positive(value: string | undefined): number | undefined {
	const n = Number(value);
	return Number.isInteger(n) && n > 0 ? n : undefined;
}

/** The indentation the project asks for this file, or null when it asks for nothing. */
export async function resolveIndentStyle(
	input: { filePath: string; languageId: string; workspaceRoot?: string },
	fs: Fs = nodeFs,
): Promise<IndentStyle | null> {
	const { filePath, languageId, workspaceRoot } = input;
	const ec = await resolveEditorConfig(filePath, fs.readText);
	const ecTabWidth = positive(ec.tab_width);
	const ecSize = ec.indent_size === "tab" ? ecTabWidth : positive(ec.indent_size);
	const ecStyle = ec.indent_style === "tab" ? false : ec.indent_style === "space" ? true : undefined;

	if (languageId === "go") {
		return { insertSpaces: false, tabSize: ecTabWidth ?? ecSize, source: "gofmt (Go is indented with tabs)" };
	}

	if (languageId === "swift") {
		const config = await findSwiftFormatConfig(filePath, fs);
		if (config) {
			try {
				const parsed = JSON.parse((await fs.readText(config)) ?? "") as {
					indentation?: { spaces?: number; tabs?: number };
					tabWidth?: number;
				};
				const spaces = parsed.indentation?.spaces;
				const tabs = parsed.indentation?.tabs;
				if (typeof spaces === "number" && spaces > 0) {
					return { insertSpaces: true, indentSize: spaces, tabSize: parsed.tabWidth, source: config };
				}
				if (typeof tabs === "number" && tabs > 0) {
					return { insertSpaces: false, tabSize: parsed.tabWidth ?? 4, source: config };
				}
				// A config that sets no indentation gets swift-format's own default.
				return { insertSpaces: true, indentSize: 2, tabSize: parsed.tabWidth, source: config };
			} catch {
				// Unreadable: swift-format will say so when it is run; indent by the rest.
			}
		}
	}

	if (PRETTIER_LANGUAGES.has(languageId)) {
		const config = await findPrettierConfig(filePath, workspaceRoot, fs);
		if (config) {
			const insertSpaces = config.useTabs !== undefined ? !config.useTabs : (ecStyle ?? true);
			const width = config.tabWidth ?? ecSize ?? 2;
			return { insertSpaces, indentSize: width, tabSize: width, source: config.path };
		}
	}

	if (ecStyle === undefined && ecSize === undefined && ecTabWidth === undefined) return null;
	return {
		insertSpaces: ecStyle,
		indentSize: ecSize,
		tabSize: ecTabWidth ?? (ecStyle === false ? ecSize : undefined),
		source: ".editorconfig",
	};
}
