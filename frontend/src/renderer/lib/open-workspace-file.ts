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

/** A workspace file to open in the viewer. */
export type WorkspaceFileOpen = {
	path: string;
	line?: number;
	/** Carried through so the caller can decide whether to reveal it in the tree. */
	inWorkspace?: boolean;
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
		onOpen({ path: candidates[0].path, line, inWorkspace: candidates[0].inWorkspace });
		return;
	}
	onDisambiguate(candidates, line);
}
