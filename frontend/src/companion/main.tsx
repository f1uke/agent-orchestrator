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

// Entry point for the overlay window. Deliberately tiny and separate from the main
// renderer: the overlay has no router, no query client, no daemon connection and no
// design-system CSS. It is a transparent page with Procs on it, and everything it
// needs to know arrives through the feed.

type CompanionBridge = {
	setInteractive(interactive: boolean): void;
	daemonUrl?(): Promise<string | null>;
	requestLook?(sessionId: string): void;
	onLooksChanged?(listener: () => void): () => void;
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
function Overlay() {
	const [live, setLive] = useState<LiveFeed | null>(null);
	const [mock] = useState<CompanionFeed>(() => createMockFeed());

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

	return (
		<CompanionStage
			feed={live ?? mock}
			bubbleFor={live ? (id) => live.bubbleFor(id) : undefined}
			onInteractiveChange={(interactive) => bridge?.setInteractive(interactive)}
			onRequestLook={(sessionId) => bridge?.requestLook?.(sessionId)}
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
	const [sheet, setSheet] = useState(false);
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

const container = document.getElementById("companion-root");
if (container) {
	createRoot(container).render(IS_LAB ? <Lab /> : <Overlay />);
}
