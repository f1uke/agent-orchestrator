import { readFile } from "node:fs/promises";
import path from "node:path";

/**
 * The `.editorconfig` properties that apply to one file, resolved the way the
 * spec (editorconfig.org) says: every `.editorconfig` from the file's
 * directory up to the first one marked `root = true`, the nearer file winning,
 * and within a file the later section winning.
 *
 * Hand-rolled rather than a dependency: the reference `editorconfig` package
 * ships a WASM INI parser, and the editor needs exactly three properties.
 */
export type EditorConfigProperties = Record<string, string>;

export async function resolveEditorConfig(
	filePath: string,
	readText: (file: string) => Promise<string | null> = readTextOrNull,
): Promise<EditorConfigProperties> {
	const configs: { dir: string; sections: Section[] }[] = [];
	let dir = path.dirname(path.resolve(filePath));
	for (;;) {
		const text = await readText(path.join(dir, ".editorconfig"));
		if (text !== null) {
			const parsed = parseEditorConfig(text);
			configs.push({ dir, sections: parsed.sections });
			if (parsed.root) break;
		}
		const parent = path.dirname(dir);
		if (parent === dir) break;
		dir = parent;
	}
	// Farthest first, so nearer files overwrite what they also set.
	const merged: EditorConfigProperties = {};
	for (const config of configs.reverse()) {
		const relative = path.relative(config.dir, path.resolve(filePath)).split(path.sep).join("/");
		for (const section of config.sections) {
			if (!globMatches(section.glob, relative)) continue;
			Object.assign(merged, section.properties);
		}
	}
	return merged;
}

type Section = { glob: string; properties: EditorConfigProperties };

export function parseEditorConfig(text: string): { root: boolean; sections: Section[] } {
	let root = false;
	const sections: Section[] = [];
	let current: Section | null = null;
	for (const raw of text.split(/\r?\n/)) {
		const line = raw.trim();
		if (line === "" || line.startsWith("#") || line.startsWith(";")) continue;
		const header = /^\[(.*)\]$/.exec(line);
		if (header) {
			current = { glob: header[1], properties: {} };
			sections.push(current);
			continue;
		}
		const eq = line.indexOf("=");
		if (eq < 0) continue;
		const key = line.slice(0, eq).trim().toLowerCase();
		const value = line.slice(eq + 1).trim();
		if (current) current.properties[key] = value.toLowerCase();
		else if (key === "root") root = value.toLowerCase() === "true";
	}
	return { root, sections };
}

/**
 * Whether a section glob matches a path relative to its `.editorconfig`. A
 * glob without a `/` matches the file name at any depth; one with a `/` is
 * anchored to the config's own directory.
 */
export function globMatches(glob: string, relativePath: string): boolean {
	const anchored = glob.includes("/");
	const body = globToRegex(glob.startsWith("/") ? glob.slice(1) : glob);
	try {
		return new RegExp(anchored ? `^${body}$` : `^(?:.*/)?${body}$`).test(relativePath);
	} catch {
		return false;
	}
}

function escapeRegex(ch: string): string {
	return /[\\^$.*+?()[\]{}|/]/.test(ch) ? `\\${ch}` : ch;
}

/** editorconfig glob → RegExp source: `*` `**` `?` `[…]` `[!…]` `{a,b}` `{1..3}` and `\` escapes. */
export function globToRegex(glob: string): string {
	let out = "";
	for (let i = 0; i < glob.length; i++) {
		const c = glob[i];
		if (c === "\\" && i + 1 < glob.length) {
			out += escapeRegex(glob[++i]);
		} else if (c === "*") {
			if (glob[i + 1] === "*") {
				out += ".*";
				i++;
			} else {
				out += "[^/]*";
			}
		} else if (c === "?") {
			out += "[^/]";
		} else if (c === "[") {
			const close = glob.indexOf("]", i + 1);
			if (close < 0) {
				out += "\\[";
				continue;
			}
			const inner = glob.slice(i + 1, close);
			const negated = inner.startsWith("!");
			const chars = (negated ? inner.slice(1) : inner).replace(/[\\\]^]/g, (m) => `\\${m}`);
			out += negated ? `[^/${chars}]` : `[${chars}]`;
			i = close;
		} else if (c === "{") {
			const close = matchingBrace(glob, i);
			if (close < 0) {
				out += "\\{";
				continue;
			}
			const inner = glob.slice(i + 1, close);
			const range = /^(-?\d+)\.\.(-?\d+)$/.exec(inner);
			const alternatives = splitTopLevel(inner);
			if (range) {
				const lo = Math.min(Number(range[1]), Number(range[2]));
				const hi = Math.max(Number(range[1]), Number(range[2]));
				out += hi - lo <= 1000 ? `(?:${Array.from({ length: hi - lo + 1 }, (_, k) => lo + k).join("|")})` : "-?\\d+";
			} else if (alternatives.length > 1) {
				out += `(?:${alternatives.map(globToRegex).join("|")})`;
			} else {
				// `{single}` is not a set - it means itself.
				out += `\\{${globToRegex(inner)}\\}`;
			}
			i = close;
		} else {
			out += escapeRegex(c);
		}
	}
	return out;
}

function matchingBrace(glob: string, open: number): number {
	let depth = 0;
	for (let i = open; i < glob.length; i++) {
		if (glob[i] === "\\") {
			i++;
			continue;
		}
		if (glob[i] === "{") depth++;
		else if (glob[i] === "}" && --depth === 0) return i;
	}
	return -1;
}

function splitTopLevel(inner: string): string[] {
	const parts: string[] = [];
	let depth = 0;
	let from = 0;
	for (let i = 0; i < inner.length; i++) {
		const c = inner[i];
		if (c === "\\") {
			i++;
			continue;
		}
		if (c === "{") depth++;
		else if (c === "}") depth--;
		else if (c === "," && depth === 0) {
			parts.push(inner.slice(from, i));
			from = i + 1;
		}
	}
	parts.push(inner.slice(from));
	return parts;
}

async function readTextOrNull(file: string): Promise<string | null> {
	try {
		return await readFile(file, "utf8");
	} catch {
		return null;
	}
}
