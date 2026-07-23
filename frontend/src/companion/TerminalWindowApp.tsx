import { useCallback, useEffect, useRef, useState } from "react";
import { displaySessionName } from "../renderer/lib/session-title";
import type { SessionStatus } from "../renderer/types/workspace";
import { SessionTerminalCard, type TailSide } from "./SessionTerminalCard";

// What the TERMINAL WINDOW renders: one session's live terminal, and nothing else.
//
// The same bundle as the overlay, told by its query string to be one thing instead
// of the other — so the card, the xterm, the preload and the CSP are the ones
// already proven, and there is no second copy of any of them to keep in step. What
// it is NOT is the overlay: no band, no Procs, no click-through, no pointer-region
// bookkeeping. It is a window with a terminal in it.
//
// The ATTACHMENT is the board's own `useTerminalSession`: reattach with backoff,
// open timeouts, exit and error handling, all of it. There is one way to hold a
// pane open in this app, and this window uses it.

type TerminalBridge = {
	daemonUrl?(): Promise<string | null>;
	/** Which way this window's Proc is, so the card's tail points at it. */
	terminalTail?(): Promise<"left" | "centre" | "right">;
	closeTerminal?(): void;
	noteTerminalActivity?(): void;
	onDetachRequest?(listener: () => void): () => void;
	reportDetached?(): void;
};

const bridge = (window as unknown as { aoCompanion?: TerminalBridge }).aoCompanion;

/** How often to re-ask for the daemon URL while it is not up yet. */
const DAEMON_WAIT_MS = 2_000;

/** How often at most the window tells the main process it is being used. */
const ACTIVITY_REPORT_MS = 30_000;

type SessionFacts = { name: string; status: SessionStatus | undefined };

/**
 * The session's name and state, read the same way the band reads them.
 *
 * A window titled with a raw session id would be the only place in the app that
 * calls a session by its id, so it asks the daemon for the same row the board and
 * the overlay use and derives the name with the board's own helper.
 */
async function readSession(daemonUrl: string, sessionId: string): Promise<SessionFacts | null> {
	try {
		const response = await fetch(`${daemonUrl}/api/v1/sessions`);
		if (!response.ok) return null;
		const body = (await response.json()) as {
			sessions?: Array<{ id?: string; displayName?: string; issueId?: string; status?: string }>;
		};
		const row = body.sessions?.find((session) => session.id === sessionId);
		if (!row) return null;
		return {
			name: displaySessionName({ displayName: row.displayName, issueId: row.issueId, id: sessionId }),
			status: row.status as SessionStatus | undefined,
		};
	} catch {
		return null;
	}
}

export function TerminalWindowApp({ sessionId, handleId }: { sessionId: string; handleId: string }) {
	const [daemonUrl, setDaemonUrl] = useState<string | null>(null);
	const [facts, setFacts] = useState<SessionFacts | null>(null);
	const [tail, setTail] = useState<TailSide>("centre");
	// Which way its Proc is. Asked once, on the way up: the window is placed over
	// the Proc and a Proc with an open terminal stands still, so the answer holds.
	useEffect(() => {
		void bridge
			?.terminalTail?.()
			.then((side) => setTail(side))
			.catch(() => undefined);
	}, []);
	const detachRef = useRef<(() => void) | null>(null);

	useEffect(() => {
		let cancelled = false;
		let timer: ReturnType<typeof setTimeout> | null = null;
		const ask = async () => {
			const url = await bridge?.daemonUrl?.().catch(() => null);
			if (cancelled) return;
			if (!url) {
				timer = setTimeout(ask, DAEMON_WAIT_MS);
				return;
			}
			setDaemonUrl(url);
			const read = await readSession(url, sessionId);
			if (!cancelled && read) setFacts(read);
		};
		void ask();
		return () => {
			cancelled = true;
			if (timer) clearTimeout(timer);
		};
	}, [sessionId]);

	// Closing is the main process's job: it owns the window, and a page cannot
	// destroy the thing it is drawn in. Everything that ends a terminal — the ✕,
	// Escape, the session ending — comes through here.
	const close = useCallback(() => bridge?.closeTerminal?.(), []);

	/**
	 * "Let the pane go, now" — the hand-off, answered rather than assumed.
	 *
	 * The main process asks before it destroys this window, because destroying it
	 * kills the renderer where it stands and the pane would never be told. Detaching
	 * here and answering is what makes "never attached in two places" true by order.
	 */
	useEffect(
		() =>
			bridge?.onDetachRequest?.(() => {
				detachRef.current?.();
				bridge?.reportDetached?.();
			}),
		[],
	);

	// The window is in use, so it is not idle. Throttled hard: this is a heartbeat,
	// not a keystroke log, and the main process only needs to know the human is here.
	const lastReport = useRef(0);
	const onActivity = useCallback(() => {
		const now = Date.now();
		if (now - lastReport.current < ACTIVITY_REPORT_MS) return;
		lastReport.current = now;
		bridge?.noteTerminalActivity?.();
	}, []);

	if (!daemonUrl) return null;
	return (
		<SessionTerminalCard
			handleId={handleId}
			title={facts?.name ?? sessionId}
			status={facts?.status}
			daemonUrl={daemonUrl}
			onClose={close}
			onActivity={onActivity}
			tail={tail}
			registerDetach={(detach) => {
				detachRef.current = detach;
			}}
		/>
	);
}
