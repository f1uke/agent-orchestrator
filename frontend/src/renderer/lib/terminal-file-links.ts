// Terminal linkifier for FILE references — the third token family after the
// `@`session refs (session-ref.ts) and `#`/`!`/Jira SCM refs
// (terminal-scm-links.ts). When the agent prints a path or filename, the token
// becomes clickable and opens the file in the code viewer (resolved and read
// INTERNALLY by the daemon — never the OS browser). Four shapes are recognised:
// an absolute path, a `~/` home path, a workspace-relative path, and a bare
// filename with a known code extension.
//
// Absolute and `~/` refs open the file WHEREVER it lives on disk — outside the
// session's worktree included (an approved product decision; the backend
// deliberately does not confine them, and `~` is expanded there, where the
// daemon's `$HOME` is authoritative). Relative and bare refs stay scoped to the
// session's workspace, since such a ref has no meaning outside one.
//
// Detection is deliberately CONSERVATIVE and runs on shape alone (this is the
// xterm link-provider hot path; the real existence check happens on click, via
// the backend resolve endpoint). A token linkifies only when its basename ends
// in a known code extension AND it sits at a path-like boundary — so a dotted
// symbol (`Money.formatted`), a method call (`obj.method()`), a package name
// (`com.example.Pkg`), a version (`v1.2.3`), or a path inside an http(s) URL is
// never linkified.

/** A file reference found on one line of terminal text. */
export type FileLinkMatch = {
	/** 0-based char offset of the token's first char within the line. */
	startIndex: number;
	/** 0-based char offset one past the token's last char (incl. any :line[:col]). */
	endIndex: number;
	/** The path/filename text (without any trailing :line[:col] suffix). */
	ref: string;
	/** 1-based line number from a trailing `:<line>` (or `:<line>:<col>`), if present. */
	line?: number;
};

// Known code-ish file extensions (lowercased). A token whose basename does not
// end in one of these never linkifies — this is the main false-positive guard,
// keeping dotted identifiers, package names, and versions out.
const CODE_EXTENSIONS = new Set([
	"swift",
	"ts",
	"tsx",
	"js",
	"jsx",
	"mjs",
	"cjs",
	"go",
	"py",
	"rb",
	"rs",
	"java",
	"kt",
	"kts",
	"c",
	"h",
	"cc",
	"cpp",
	"cxx",
	"hpp",
	"hh",
	"m",
	"mm",
	"cs",
	"php",
	"scala",
	"sh",
	"bash",
	"zsh",
	"fish",
	"sql",
	"css",
	"scss",
	"sass",
	"less",
	"html",
	"htm",
	"xml",
	"json",
	"jsonc",
	"yaml",
	"yml",
	"toml",
	"ini",
	"cfg",
	"conf",
	"md",
	"markdown",
	"mdx",
	"txt",
	"proto",
	"gradle",
	"dockerfile",
	"vue",
	"svelte",
	"dart",
	"lua",
	"r",
	"pl",
	"ex",
	"exs",
	"erl",
	"clj",
	"hs",
	"ml",
	"tf",
	"env",
	"gitignore",
]);

// Leading boundary: the token must start at line-start or right after whitespace
// or an opening delimiter — NOT after a path char (`/`, `.`, `:`, alnum). This
// keeps a URL's path segments (preceded by `/` or `:`) and word-internal
// fragments from being matched. Path chars include `+` (Swift `A+B.swift`), `-`,
// `_`, `.`, and `/`. A `~/` is allowed only as the token's very first segment,
// so `backup~/x.md` (a `~` mid-word) still does not start a link. The optional
// `:<line>[:<col>]` suffix is captured separately.
const FILE_TOKEN_RE = /(^|[\s(['"`=,>|{])((?:~\/)?[A-Za-z0-9._+/-]+)(:\d+(?::\d+)?)?/g;

/** The four ref shapes the backend resolver distinguishes. */
export type FileRefShape = "absolute" | "tilde" | "relative" | "bare";

/**
 * The shape of a file reference. `absolute` and `tilde` name a location
 * globally and open anywhere on disk; `relative` and `bare` are resolved inside
 * the session's workspace. Mirrors the backend's `refTarget` split — keep the
 * two in step.
 */
export function classifyFileRef(ref: string): FileRefShape {
	const trimmed = ref.trim();
	if (trimmed === "~" || trimmed.startsWith("~/")) return "tilde";
	if (trimmed.startsWith("/")) return "absolute";
	return trimmed.includes("/") ? "relative" : "bare";
}

// Trailing characters trimmed from a captured token — sentence punctuation the
// path char class greedily absorbed (a run can end in `.` or `-`).
function trimTrailingPunctuation(token: string): string {
	return token.replace(/[.\-]+$/, "");
}

// The lowercased extension of a path's basename, or undefined when the basename
// has no dot (no extension). The basename is the segment after the last slash.
function basenameExtension(pathToken: string): string | undefined {
	const slash = pathToken.lastIndexOf("/");
	const base = slash >= 0 ? pathToken.slice(slash + 1) : pathToken;
	const dot = base.lastIndexOf(".");
	if (dot <= 0 || dot === base.length - 1) return undefined; // no ext, or dotfile-with-no-ext, or trailing dot
	return base.slice(dot + 1).toLowerCase();
}

/**
 * Every file reference on one line of terminal text, with its char range
 * (consumed by the xterm link provider). Matches are ordered by position. The
 * returned `ref` is the raw path/filename; resolution to a real workspace file
 * (and confinement) happens later, on click, via the backend.
 */
export function findFileLinks(line: string): FileLinkMatch[] {
	const matches: FileLinkMatch[] = [];
	const re = new RegExp(FILE_TOKEN_RE.source, "g");
	let m: RegExpExecArray | null;
	while ((m = re.exec(line)) !== null) {
		const lead = m[1];
		const rawToken = m[2];
		const lineSuffix = m[3];
		const tokenStart = m.index + lead.length;

		const path = trimTrailingPunctuation(rawToken);
		if (path === "") continue;

		const ext = basenameExtension(path);
		if (!ext || !CODE_EXTENSIONS.has(ext)) continue;

		let endIndex = tokenStart + path.length;
		let lineNo: number | undefined;
		if (lineSuffix && path.length === rawToken.length) {
			// Only honour the :line suffix when nothing was trimmed off the token
			// (so a trimmed trailing dot can't be confused with a line separator).
			const parts = lineSuffix.slice(1).split(":");
			lineNo = Number.parseInt(parts[0], 10);
			endIndex += lineSuffix.length;
		}

		matches.push({ startIndex: tokenStart, endIndex, ref: path, line: lineNo });
	}
	return matches;
}
