// Click-time resolution for a terminal file reference. Detection (which token is
// a file) happens on the xterm hot path in terminal-file-links.ts; actually
// finding the file in the workspace happens here, on click, via an injected
// resolver (backed by the backend /workspace/resolve endpoint). The three
// outcomes map to the three UI responses: one candidate opens directly, several
// open a disambiguation picker, none shows a non-blocking toast. Resolution is
// confined to the session's workspace by the backend; a failure degrades to
// not-found so a click can never error out the terminal.

/** A workspace file to open in the viewer. */
export type WorkspaceFileOpen = { path: string; line?: number };

export type OpenWorkspaceFileOptions = {
	sessionId: string;
	/** The raw file reference text from the terminal token. */
	ref: string;
	/** Optional 1-based line to scroll to (from a `:<line>` suffix). */
	line?: number;
	/** Resolve a ref to candidate workspace-relative paths (injected for testing). */
	resolve: (sessionId: string, ref: string) => Promise<string[]>;
	/** Open the single resolved file. */
	onOpen: (file: WorkspaceFileOpen) => void;
	/** Present a picker for multiple candidates. */
	onDisambiguate: (candidates: string[], line?: number) => void;
	/** No candidate resolved (or resolution failed) — show a non-blocking toast. */
	onNotFound: (ref: string) => void;
};

export async function openWorkspaceFileRef(opts: OpenWorkspaceFileOptions): Promise<void> {
	const { sessionId, ref, line, resolve, onOpen, onDisambiguate, onNotFound } = opts;
	let candidates: string[];
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
		onOpen({ path: candidates[0], line });
		return;
	}
	onDisambiguate(candidates, line);
}
