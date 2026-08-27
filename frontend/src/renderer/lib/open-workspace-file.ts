// Click-time resolution for a terminal file reference. Detection (which token is
// a file) happens on the xterm hot path in terminal-file-links.ts; actually
// finding the file in the workspace happens here, on click, via an injected
// resolver (backed by the backend /workspace/resolve endpoint). The three
// outcomes map to the three UI responses: one candidate opens directly, several
// open a disambiguation picker, none shows a non-blocking toast. The backend
// scopes a relative/bare ref to the session's workspace but resolves an
// absolute or `~/` ref anywhere on disk (deliberately unconfined); a failure
// degrades to not-found so a click can never error out the terminal.

/** One path a reference resolved to, with the backend's containment verdict. */
export type ResolvedCandidate = {
	/** Workspace-relative inside the workspace, absolute outside it. */
	path: string;
	/**
	 * Whether the file lives inside the session's workspace, as decided by the
	 * SERVER. Only the server can decide this correctly — it compares
	 * symlink-resolved paths — so never re-derive it from the path's shape here.
	 * The Files tab reveals a reference in its tree only when this is true.
	 */
	inWorkspace: boolean;
};

/**
 * A workspace file to open, optionally at a location.
 *
 * THE SEAM. Everything that opens a file — a clicked terminal reference, the
 * ⌘⇧O palette, and later a go-to-definition jump — describes what it wants with
 * this type and hands it to the single `openWorkspaceFile` callback in
 * `SessionView`. Nothing on either side of that callback knows which viewer is
 * mounted, which is what lets the editor slices repoint it (at Monaco, at a
 * definition target) without touching a single caller.
 */
export type WorkspaceFileOpen = {
	/** Workspace-relative inside the workspace, absolute outside it. */
	path: string;
	/** 1-based line to scroll to. */
	line?: number;
	/**
	 * 1-based column. Nothing sets it yet: a terminal ref carries no column and
	 * ⌘⇧O over files opens at the top. It is here so that go-to-definition, which
	 * lands on a symbol rather than a line, has a place to say where without
	 * every caller of this type changing shape on that day.
	 */
	column?: number;
	/**
	 * Where to land when no explicit `line` is given. "first-hunk" opens on what
	 * this branch changed rather than on line 1 — a Changes row means "show me
	 * this file's changes", and line 1 is almost never where they are. An
	 * explicit `line` always wins: a terminal `:42` and a go-to-definition target
	 * both name a line the reader asked for.
	 */
	focus?: "first-hunk";
	/** Carried through so the caller can decide whether to reveal it in the tree. */
	inWorkspace?: boolean;
	/**
	 * How hard to insist the rail shows this file.
	 *
	 * - "follow" (the default, and what every jump uses): the Files tree marks
	 *   the file, opens its ancestor folders and scrolls it in — but only if the
	 *   Files tab is already on screen. Chasing definitions must not keep
	 *   yanking the rail out from under the reader.
	 * - "focus": select the Files tab, open the rail if it is shut, and ring the
	 *   row. Reserved for the gesture that is explicitly ABOUT the file's
	 *   location — a clicked terminal reference.
	 */
	reveal?: "follow" | "focus";
};

export type OpenWorkspaceFileOptions = {
	sessionId: string;
	/** The raw file reference text from the terminal token. */
	ref: string;
	/** Optional 1-based line to scroll to (from a `:<line>` suffix). */
	line?: number;
	/** Resolve a ref to candidates (injected for testing). */
	resolve: (sessionId: string, ref: string) => Promise<ResolvedCandidate[]>;
	/** Open the single resolved file. */
	onOpen: (file: WorkspaceFileOpen) => void;
	/** Present a picker for multiple candidates. */
	onDisambiguate: (candidates: ResolvedCandidate[], line?: number) => void;
	/** No candidate resolved (or resolution failed) — show a non-blocking toast. */
	onNotFound: (ref: string) => void;
};

export async function openWorkspaceFileRef(opts: OpenWorkspaceFileOptions): Promise<void> {
	const { sessionId, ref, line, resolve, onOpen, onDisambiguate, onNotFound } = opts;
	let candidates: ResolvedCandidate[];
	try {
		candidates = await resolve(sessionId, ref);
	} catch {
		onNotFound(ref);
		return;
	}
	if (candidates.length === 0) {
		onNotFound(ref);
		return;
	}
	if (candidates.length === 1) {
		// A clicked terminal reference is the one gesture that is about WHERE the
		// file is, so it is the one that takes the rail.
		onOpen({ path: candidates[0].path, line, inWorkspace: candidates[0].inWorkspace, reveal: "focus" });
		return;
	}
	onDisambiguate(candidates, line);
}
