import { useCallback, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import "./companion.css";
import type { World } from "./behaviour";
import { castForSession, withSpecies } from "./cast";
import { CompanionStage } from "./CompanionStage";
import { ConceptSheet } from "./ConceptSheet";
import { createManualFeed, type ManualFeed } from "./dev-feed";
import { DevPanel } from "./DevPanel";
import type { CompanionFeed } from "./feed";
import { createLiveFeed, type LiveFeed } from "./live-feed";
import { refreshProjectLooks } from "./look-store-live";
import { createHttpTransport } from "./live-transport";
import { mockActivitiesAt, createMockFeed } from "./mock-feed";
import { speciesForProject, type SpeciesId } from "./species";
import { protoNote, startProtoTrace } from "./proto-trace";
import { TerminalWindowApp } from "./TerminalWindowApp";

// Entry point for the overlay window. Deliberately tiny and separate from the main
// renderer: the overlay has no router, no query client, no daemon connection and no
// design-system CSS. It is a transparent page with Procs on it, and everything it
// needs to know arrives through the feed.

type CompanionBridge = {
	setInteractive(interactive: boolean): void;
	daemonUrl?(): Promise<string | null>;
	requestLook?(sessionId: string): void;
	onLooksChanged?(listener: () => void): () => void;
	// PROTOTYPE (terminal bubble)
	activateSession?(input: {
		sessionId: string;
		handleId: string;
		anchor: { x: number; y: number };
	}): Promise<"app" | "bubble" | "unavailable">;
	moveTerminal?(anchor: { x: number; y: number }): void;
	closeTerminal?(): void;
	onTerminalClosed?(listener: () => void): () => void;
	onMainWindowOpened?(listener: () => void): () => void;
};

const bridge = (window as unknown as { aoCompanion?: CompanionBridge }).aoCompanion;

/** How often to re-ask for the daemon URL while it is not up yet. */
const DAEMON_WAIT_MS = 5_000;

/**
 * Waits for the daemon, then runs on real sessions.
 *
 * Until the daemon answers there is nothing true to show, so the overlay shows the
 * MOCK cast rather than an empty band — an empty desktop would read as "the
 * companion is broken" when it only means "the daemon is still starting". The
 * moment a real roster arrives the mock is replaced wholesale.
 */
/**
 * PROTOTYPE (terminal bubble) harness switch: `companion.html?protoHandle=<tmux>`.
 *
 * The prototype runs against an ISOLATED daemon that has no sessions in it, so
 * there would be no Proc to click and no pane to attach. With this set, the band
 * keeps its mock cast and every click attaches to the named tmux pane — which is
 * a REAL pane, reached through the REAL daemon mux. Absent (every packaged
 * overlay), nothing on this path runs.
 */
const PROTO_HANDLE = new URLSearchParams(window.location.search).get("protoHandle");

// The instrument for the human-driven bug hunt. Only in the prototype harness.
if (PROTO_HANDLE) startProtoTrace();

/** Whose terminal window is up. The window itself lives in the main process. */
type OpenTerminal = { sessionId: string };

async function terminalHandleFor(daemonUrl: string, sessionId: string): Promise<string | null> {
	if (PROTO_HANDLE) return PROTO_HANDLE;
	try {
		const response = await fetch(`${daemonUrl}/api/v1/sessions`);
		if (!response.ok) return null;
		const body = (await response.json()) as { sessions?: Array<{ id?: string; terminalHandleId?: string }> };
		return body.sessions?.find((session) => session.id === sessionId)?.terminalHandleId ?? null;
	} catch {
		return null;
	}
}

function Overlay() {
	const [live, setLive] = useState<LiveFeed | null>(null);
	const [mock] = useState<CompanionFeed>(() => createMockFeed());
	const [daemonUrl, setDaemonUrl] = useState<string | null>(null);
	const [terminal, setTerminal] = useState<OpenTerminal | null>(null);

	useEffect(() => {
		let cancelled = false;
		let timer: ReturnType<typeof setTimeout> | null = null;

		const attach = async () => {
			const url = await bridge?.daemonUrl?.().catch(() => null);
			if (cancelled) return;
			if (!url) {
				timer = setTimeout(attach, DAEMON_WAIT_MS);
				return;
			}
			setDaemonUrl(url);
			if (PROTO_HANDLE) return; // keep the mock cast; see PROTO_HANDLE.
			setLive(createLiveFeed({ ...createHttpTransport(url), now: () => Date.now() }));
		};

		void attach();
		return () => {
			cancelled = true;
			if (timer) clearTimeout(timer);
		};
	}, []);

	// The looks travel by `storage` event, because both windows are one origin. This
	// is the same message arriving the other way, so a change lands even if the
	// event never does; both paths do nothing but re-read localStorage.
	useEffect(() => bridge?.onLooksChanged?.(() => refreshProjectLooks()), []);

	// However the terminal went — its own ✕, Escape, the board window coming up —
	// the band hears about it from the one place that knows: the process that owns
	// the window. The Proc it belonged to is free to wander again.
	useEffect(() => bridge?.onTerminalClosed?.(() => setTerminal(null)), []);

	const onActivate = useCallback(
		(sessionId: string, at: { x: number; y: number }) => {
			void (async () => {
				if (!daemonUrl) return;
				const handleId = await terminalHandleFor(daemonUrl, sessionId);
				if (!handleId) return;
				// Screen coordinates, because the window that has to be placed is not
				// this page: this page just happens to know where the Proc is.
				const anchor = { x: window.screenX + at.x, y: window.screenY + at.y };
				const where = (await bridge?.activateSession?.({ sessionId, handleId, anchor })) ?? "unavailable";
				protoNote("activate", `${sessionId} → ${where}`);
				setTerminal(where === "bubble" ? { sessionId } : null);
			})();
		},
		[daemonUrl],
	);

	// The Proc carries its terminal with it: the card is a window now, so "riding
	// the Proc" means moving that window as the Proc moves. It only moves when the
	// Proc is picked up and thrown — a Proc with an open terminal stands still.
	const onAnchorMove = useCallback((anchor: { x: number; y: number }) => {
		bridge?.moveTerminal?.({ x: window.screenX + anchor.x, y: window.screenY + anchor.y });
	}, []);

	const onInteractiveChange = useCallback((interactive: boolean) => {
		protoNote("window takes the mouse", String(interactive));
		bridge?.setInteractive(interactive);
	}, []);
	const onRequestLook = useCallback((sessionId: string) => bridge?.requestLook?.(sessionId), []);

	return (
		<CompanionStage
			feed={live ?? mock}
			bubbleFor={live ? (id) => live.bubbleFor(id) : undefined}
			onInteractiveChange={onInteractiveChange}
			onRequestLook={onRequestLook}
			onActivate={onActivate}
			attachedSession={terminal?.sessionId}
			onAttachedAnchorMove={onAnchorMove}
		/>
	);
}

/**
 * The playground: `companion.html` opened in a plain browser during development.
 *
 * Guarded by BOTH conditions, and it has to be both. There is no bridge in a
 * browser, so that alone would let a packaged overlay that failed to preload show
 * a debug panel on the user's desktop; `import.meta.env.DEV` alone would put it on
 * a developer's real overlay window. Together they mean exactly "a browser tab
 * pointed at the dev server", which is the only place it belongs.
 */
const IS_LAB = import.meta.env.DEV && !bridge;

function Lab() {
	const [feed] = useState<ManualFeed>(() => createManualFeed(mockActivitiesAt(0)));
	const [setWorld, setSetWorld] = useState<React.Dispatch<React.SetStateAction<World>> | null>(null);
	const [reducedMotion, setReducedMotion] = useState(false);
	const [species, setSpecies] = useState<SpeciesId | "mixed">("mixed");
	// `#concepts` opens the sheet on load, so `ao preview` can be pointed straight at
	// the art instead of asking whoever is looking to find a button first.
	const [sheet, setSheet] = useState(() => window.location.hash === "#concepts");
	const onStage = useCallback((api: { setWorld: React.Dispatch<React.SetStateAction<World>> }) => {
		setSetWorld(() => api.setWorld);
	}, []);

	// The lab's creature switcher. `mixed` is the DEFAULT here because it is what the
	// real thing does now — the creature comes from the PROJECT, so a band of several
	// projects is several creatures without anybody choosing anything. Picking a single
	// creature overrides that, to look at one body across every state.
	const castFor = useCallback(
		(sessionId: string, project?: string) => {
			const base = castForSession(sessionId);
			if (species === "mixed") return withSpecies(base, speciesForProject(project));
			return withSpecies(base, species);
		},
		[species],
	);

	return (
		<>
			<CompanionStage
				feed={feed}
				bubbleFor={(id) => feed.bubbleFor(id)}
				reducedMotion={reducedMotion}
				onStage={onStage}
				castFor={castFor}
			/>
			<DevPanel
				feed={feed}
				setWorld={setWorld}
				reducedMotion={reducedMotion}
				onReducedMotion={setReducedMotion}
				species={species}
				onSpecies={setSpecies}
				onConceptSheet={() => setSheet(true)}
			/>
			{sheet ? <ConceptSheet onClose={() => setSheet(false)} /> : null}
		</>
	);
}

const params = new URLSearchParams(window.location.search);
const TERMINAL_FOR = params.get("terminalFor");
const TERMINAL_HANDLE = params.get("handle");

const container = document.getElementById("companion-root");
if (container) {
	// One bundle, two windows: the band, or one session's terminal.
	const page =
		TERMINAL_FOR && TERMINAL_HANDLE ? (
			<TerminalWindowApp sessionId={TERMINAL_FOR} handleId={TERMINAL_HANDLE} />
		) : IS_LAB ? (
			<Lab />
		) : (
			<Overlay />
		);
	createRoot(container).render(page);
}
