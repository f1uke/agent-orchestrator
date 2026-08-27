import { useEffect, useState } from "react";
import {
	type CompletionCapability,
	createLspClient,
	type LspClient,
	type LspResultOutcome,
	type LspTransport,
	type SemanticTokensLegend,
} from "./lsp-client";

/**
 * Attach a pane to the language server for its (workspace, language), and keep
 * that attachment honest.
 *
 * 🗝 `unavailable` is a real state, not an error. It means this pane will never
 * have intelligence, because there is no Electron bridge (the browser-preview
 * path) or because this app ships no server for the language. Saying so is the
 * whole point: what this stack does wrong is answer nothing while looking fine.
 */
export type LspState = "starting" | "initializing" | "indexing" | "ready" | "failed" | "stopped";

export type LanguageServerHandle = {
	client: LspClient | null;
	state: LspState | "unavailable";
	detail?: string;
	/**
	 * Configured enough to run, but a feature will find nothing - an Xcode build
	 * that produced compile settings and no index, say. Distinct from `detail`
	 * because a reader has to be able to act on it, and distinct from `failed`
	 * because the server is genuinely working.
	 */
	warning?: string;
};

type Bridge = {
	attach(input: { root: string; languageId: string }): Promise<{
		handleId: string;
		state: LspState;
		detail?: string;
		documentRoot?: string;
		warning?: string;
		semanticTokens?: SemanticTokensLegend | null;
		completion?: CompletionCapability | null;
	}>;
	detach(handleId: string): void;
	send(handleId: string, message: Record<string, unknown>): void;
	noteResult(handleId: string, outcome: LspResultOutcome): void;
	onMessage(cb: (e: { handleId: string; message: Record<string, unknown> }) => void): () => void;
	onState(cb: (e: { handleId: string; key: string; state: LspState; detail?: string }) => void): () => void;
};

function bridge(): Bridge | null {
	return (globalThis as unknown as { ao?: { lsp?: Bridge } }).ao?.lsp ?? null;
}

/**
 * Whether this renderer can have a language server AT ALL.
 *
 * A property of the environment, not of any pane or state, so a caller can skip
 * wiring up machinery that could never answer - the browser-preview build and
 * the e2e harness have no main process, and there a provider registered against
 * a server that will never exist is pure cost.
 */
export function hasLanguageServers(): boolean {
	return bridge() !== null;
}

/** The IPC channel as the client sees it, or null where there is no main process. */
export function lspTransport(): LspTransport | null {
	const api = bridge();
	if (!api) return null;
	return {
		send: (handleId, message) => api.send(handleId, message),
		noteResult: (handleId, outcome) => api.noteResult(handleId, outcome),
		onMessage: (cb) => api.onMessage(cb),
	};
}

export function useLanguageServer(workspaceRoot: string | undefined, languageId: string | null): LanguageServerHandle {
	const [handle, setHandle] = useState<LanguageServerHandle>({ client: null, state: "unavailable" });
	// Bumped to force a re-attach after the server underneath us went away.
	const [generation, setGeneration] = useState(0);

	useEffect(() => {
		const api = bridge();
		if (!workspaceRoot || !languageId || !api) {
			setHandle({ client: null, state: "unavailable" });
			return;
		}
		let cancelled = false;
		let attachedId: string | null = null;
		let client: LspClient | null = null;

		setHandle({ client: null, state: "starting" });

		const unsubscribeState = api.onState((event) => {
			if (event.handleId !== attachedId) return;
			if (event.state === "stopped") {
				// Idle stop or an eviction: the workspace is still fine, the server is
				// just gone. Re-attach with a FRESH client - the replacement server
				// knows nothing about the documents the old one had open, so carrying
				// the old client forward is what left the spike's pane silently dead.
				setHandle({ client: null, state: "stopped", detail: event.detail });
				setGeneration((n) => n + 1);
				return;
			}
			if (event.state === "failed") {
				// Deliberately does NOT re-attach. A gopls that is missing or crashing
				// would otherwise become an invisible respawn loop; the reader gets a
				// reason instead, and a re-mount is what retries.
				setHandle({ client: null, state: "failed", detail: event.detail });
				return;
			}
			setHandle((prev) => ({ ...prev, state: event.state, detail: event.detail }));
		});

		void api
			.attach({ root: workspaceRoot, languageId })
			.then((attachment) => {
				if (cancelled) {
					api.detach(attachment.handleId);
					return;
				}
				attachedId = attachment.handleId;
				const transport = lspTransport();
				if (!transport) return;
				client = createLspClient(attachment.handleId, transport, {
					workspaceRoot,
					// Older bridges (and the browser-preview stub) do not send one; the
					// workspace root is right for every language but Swift, and Swift
					// cannot attach without a main process anyway.
					documentRoot: attachment.documentRoot ?? workspaceRoot,
					// Absent from older bridges and from the browser-preview stub, which
					// is the same thing as a server that advertised no legend: the
					// provider then has nothing to decode against and stays quiet.
					semanticTokens: attachment.semanticTokens ?? null,
					// Absent from an older bridge and from the browser-preview stub,
					// which is the same thing as a server that advertised no completion
					// provider: no provider is registered and nothing pretends to answer.
					completion: attachment.completion ?? null,
				});
				setHandle({ client, state: attachment.state, detail: attachment.detail, warning: attachment.warning });
			})
			.catch((err: unknown) => {
				if (cancelled) return;
				// Never silence. A spawn that failed because gopls is not on PATH has
				// to say so, or the whole slice looks like it was never built.
				const detail = err instanceof Error ? err.message : String(err);
				console.error(`[lsp] attach failed for ${languageId} at ${workspaceRoot}: ${detail}`);
				setHandle({ client: null, state: "failed", detail });
			});

		return () => {
			cancelled = true;
			unsubscribeState();
			client?.dispose();
			if (attachedId) api.detach(attachedId);
		};
	}, [workspaceRoot, languageId, generation]);

	return handle;
}
