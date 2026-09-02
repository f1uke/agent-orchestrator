import { useCallback, useEffect, useState } from "react";
import { useTerminalSession, type AttachableTerminal } from "../hooks/useTerminalSession";
import { XtermTerminal } from "./XtermTerminal";
import type { Theme } from "../stores/ui-store";

/**
 * The Wiki's terminal.
 *
 * 🗝 There is NO session behind this pane. `useTerminalSession` takes only a
 * `terminalHandleId` (its `AttachableSession` is narrow on purpose), so a bare
 * runtime handle attaches exactly like a worker's — which is what makes the
 * Wiki possible without touching the session lifecycle at all.
 *
 * It is a separate component from `TerminalPane` rather than a mode of it:
 * TerminalPane's whole surface (file-reference linkification, SCM and Jira
 * links, restore-after-terminate, split controls) is session-shaped and would
 * have to be defeated field by field. This is the composition the hook's own
 * doc comment describes.
 */

export function WikiTerminal({
	handleId,
	theme,
	fontSize,
	daemonReady,
	onOutput,
}: {
	handleId: string;
	theme: Theme;
	fontSize: number;
	daemonReady: boolean;
	/** The pane produced output — the Wiki's "the agent is thinking" signal. */
	onOutput?: () => void;
}) {
	const [terminal, setTerminal] = useState<AttachableTerminal | null>(null);
	const { attach, state, error } = useTerminalSession({ terminalHandleId: handleId }, { daemonReady, onOutput });

	const handleReady = useCallback((handle: AttachableTerminal) => setTerminal(handle), []);

	// Each state says what is actually happening. "Connecting" for a dropped
	// socket would tell the reader the agent is starting when it is already
	// running, and for a dead daemon it would blame the wrong thing entirely.
	const banner =
		state === "connecting"
			? "Connecting to the agent…"
			: state === "reattaching"
				? daemonReady
					? "Reconnecting to the agent…"
					: "Waiting for the AO daemon…"
				: state === "error"
					? (error ?? "Could not attach to the agent.")
					: null;

	useEffect(() => {
		if (!terminal) return;
		return attach(terminal);
	}, [terminal, attach]);

	return (
		<div className="wiki-terminal">
			{banner !== null && <div className="wiki-terminal__banner">{banner}</div>}
			<div className="wiki-terminal__surface">
				<XtermTerminal
					ariaLabel="Wiki agent terminal"
					active
					autoFocus
					fontSize={fontSize}
					onReady={handleReady}
					theme={theme}
				/>
			</div>
		</div>
	);
}
