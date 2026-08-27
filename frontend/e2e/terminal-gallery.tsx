import { useCallback, useState } from "react";
import { createRoot } from "react-dom/client";
import { XtermTerminal } from "../src/renderer/components/XtermTerminal";
import type { AttachableTerminal } from "../src/renderer/hooks/useTerminalSession";
import { findSessionLinks } from "../src/renderer/lib/session-ref";
import { findExternalRefLinks } from "../src/renderer/lib/terminal-scm-links";
import { findFileLinks, type FileLinkMatch } from "../src/renderer/lib/terminal-file-links";
import "@xterm/xterm/css/xterm.css";
import "../src/renderer/styles.css";

// One real XtermTerminal, wired to the SAME resolvers TerminalPane wires in the
// app, fed a fixture that contains one of every clickable the terminal makes.
// The spec drives it with a real pointer, so nothing here may simulate a click.

/** The session's own remotes, as TerminalPane derives them from live workspace data. */
const REMOTES = {
	githubRepoBase: "https://github.com/aoagents/agent-orchestrator",
	gitlabProjectBase: "https://gitlab.example.com/acme/mobility",
	jiraBrowseBase: "https://acme.atlassian.net",
};

const KNOWN_SESSION_IDS = new Set(["ao-demo-12"]);

// Every fixture row, in buffer order — the spec addresses rows by this index.
// One clickable per row, each from a different mechanism, plus a control row
// with no link in it at all.
export const ROWS = [
	// Claude Code / Superpowers emit OSC 8 hyperlinks: ESC ] 8 ;; URI ST text ESC ] 8 ;; ST.
	{ label: "osc8", text: "\x1b]8;;https://example.com/osc8\x1b\\OSC8 hyperlink\x1b]8;;\x1b\\", token: "OSC8" },
	// A bare https URL in plain text — WebLinksAddon's auto-detection.
	{ label: "weblink", text: "see https://example.com/plain for details", token: "https://example.com/plain" },
	// The user-reported case: a bare Jira key, linkified against the session's browse base.
	{ label: "jira", text: "picked up MOBILITY-4765 from the board", token: "MOBILITY-4765" },
	// GitHub PR/issue ref.
	{ label: "github", text: "opened #262 against main", token: "#262" },
	// GitLab MR ref.
	{ label: "gitlab", text: "review !2961 when you can", token: "!2961" },
	// A file reference — opens the in-app viewer, never a browser.
	{ label: "file", text: "edited frontend/src/renderer/components/XtermTerminal.tsx", token: "frontend/src" },
	// An AO session reference — navigates inside the app, never a browser.
	{ label: "session", text: "handed off to @ao-demo-12 for review", token: "@ao-demo-12" },
	// Control: no clickable anywhere on this row.
	{ label: "plain", text: "nothing on this line is a link", token: "nothing" },
] as const;

/** Enable SGR mouse tracking, the way an agent TUI (Claude Code, tmux) does. */
const ENABLE_MOUSE_TRACKING = "\x1b[?1000h\x1b[?1002h\x1b[?1006h";

function encode(text: string): Uint8Array {
	return new TextEncoder().encode(text);
}

type Recorded = { kind: "file" | "session"; value: string };

declare global {
	interface Window {
		/** Links handed to the OS browser (window.open) or to shell.openExternal. */
		__aoOpened?: { via: string; url: string }[];
		/** Text the terminal copied to the clipboard — how a selection proves itself. */
		__aoCopied?: string[];
		/** Terminal geometry, so the spec can map a fixture row/column to pixels. */
		__aoTerminal?: { cols: number; rows: number };
		/** Link activations that stay INSIDE the app (file refs, session refs). */
		__aoActivated?: Recorded[];
		/** Bytes the terminal sent back to the PTY, tagged by source (mouse reports, keys). */
		__aoInput?: { data: string; source: string }[];
	}
}

function TerminalGallery() {
	const [error, setError] = useState<string | null>(null);
	// `?mouse=on` runs the pane the way an agent TUI does — mouse tracking active,
	// so a plain click is a report the app consumes rather than a local gesture.
	const query = new URLSearchParams(window.location.search);
	const mouseTracking = query.get("mouse") === "on";
	const repainting = query.get("repaint") === "on";

	const onReady = useCallback(
		(terminal: AttachableTerminal) => {
			window.__aoActivated = [];
			window.__aoInput = [];
			terminal.onUserInput((data, source) => {
				window.__aoInput?.push({ data, source });
			});
			if (mouseTracking) terminal.write(encode(ENABLE_MOUSE_TRACKING));
			// `?repaint=on` redraws the whole screen on a timer, the way an agent TUI
			// does while it works. That repaint is what destroys and rebuilds the link
			// under the pointer, so this is the mode the reported bug lives in.
			if (repainting) {
				window.setInterval(() => {
					terminal.write(encode("\x1b[H"));
					for (const row of ROWS) terminal.write(encode(`${row.text}\r\n`));
				}, 60);
			}
			for (const row of ROWS) terminal.write(encode(`${row.text}\r\n`));
			// The handle itself, not a snapshot: cols/rows are live getters, and the
			// pane keeps re-fitting after mount until the grid stops changing.
			window.__aoTerminal = terminal;
		},
		[mouseTracking, repainting],
	);

	return (
		<div style={{ position: "fixed", inset: 0, background: "var(--bg)" }}>
			{error && <div data-testid="terminal-error">{error}</div>}
			<XtermTerminal
				ariaLabel="Terminal link gallery"
				className="h-full w-full"
				theme="dark"
				fontSize={13}
				sessionLinkResolver={(line) => findSessionLinks(line, { knownIds: KNOWN_SESSION_IDS })}
				onSessionLinkActivate={(sessionId) => window.__aoActivated?.push({ kind: "session", value: sessionId })}
				externalRefResolver={(line) => findExternalRefLinks(line, REMOTES)}
				fileLinkResolver={findFileLinks}
				onFileLinkActivate={(match: FileLinkMatch) => window.__aoActivated?.push({ kind: "file", value: match.ref })}
				onReady={onReady}
				onError={(cause) => setError(String(cause))}
			/>
		</div>
	);
}

createRoot(document.getElementById("root")!).render(<TerminalGallery />);
