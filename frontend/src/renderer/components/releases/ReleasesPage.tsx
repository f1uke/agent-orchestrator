import { useNavigate, useSearch } from "@tanstack/react-router";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useReleaseBoard } from "../../hooks/useReleaseBoard";
import { makeLens } from "../../lib/releases/lens";
import { sourceIndex } from "../../lib/releases/sources";
import type { Board } from "../../lib/releases/types";
import {
	DEFAULT_LAYERS,
	parseReleasesSearch,
	releasesSearch,
	type Layers,
	type ReleasesSearch,
	type SheetEntry,
} from "../../lib/releases/url";
import { saveSearch, useReleasesUi } from "../../stores/releases-ui";
import { ReleaseMotionRoot } from "./ReleaseMotion";
import { SheetStack } from "./SheetStack";
import { SourcesProvider } from "./Sources";
import { SummaryStrip } from "./SummaryStrip";
import { Timeline } from "./Timeline";
import { TopBar } from "./TopBar";
import "./releases.css";

export type PageActions = {
	setSearch: (patch: Partial<ReleasesSearch>) => void;
	openSheet: (s: SheetEntry, from?: "push") => void;
	closeSheet: () => void;
	back: () => void;
	setLayers: (layers: Layers) => void;
	setHiddenBoards: (keys: ReadonlySet<string>) => void;
};

export function ReleasesPage() {
	const raw = useSearch({ strict: false }) as Record<string, unknown>;
	const url = useMemo(() => parseReleasesSearch(raw), [raw]);
	const navigate = useNavigate();
	const query = useReleaseBoard();
	const stored = useReleasesUi((s) => s.layers);
	const layers = url.layers ?? stored;
	const storedHidden = useReleasesUi((s) => s.hidden);
	const hiddenBoards = url.hideBoards ?? storedHidden;
	const { pushBack, popBack, clearBack, setLayers, setHidden } = useReleasesUi();
	useEffect(() => saveSearch(releasesSearch(url)), [url]);

	const setSearch = useCallback(
		(patch: Partial<ReleasesSearch>) =>
			void navigate({
				to: "/releases",
				search: releasesSearch({ ...url, ...patch }),
				replace: true,
				resetScroll: false,
			}),
		[navigate, url],
	);
	const actions: PageActions = useMemo(
		() => ({
			setSearch,
			openSheet: (s, from) => {
				if (from === "push" && url.sheet) pushBack(url.sheet);
				else clearBack();
				setSearch({ sheet: s });
			},
			closeSheet: () => {
				clearBack();
				setSearch({ sheet: null });
			},
			back: () => setSearch({ sheet: popBack() ?? null }),
			setLayers: (l) => {
				setLayers(l);
				if (url.layers) setSearch({ layers: l });
			},
			setHiddenBoards: (keys) => {
				setHidden(keys);
				if (url.hideBoards) setSearch({ hideBoards: keys });
			},
		}),
		[setSearch, url.sheet, url.layers, url.hideBoards, pushBack, popBack, clearBack, setLayers, setHidden],
	);

	const response = query.data;
	const fetchError = query.isError
		? query.error instanceof Error
			? query.error.message
			: "the daemon did not answer"
		: null;

	return (
		<ReleaseMotionRoot>
			<div className="rel" data-testid="releases-page" data-src={layers.src || undefined}>
				{!response ? (
					<div className="rel-empty">{fetchError ? `Release trains: ${fetchError}` : "Loading release trains…"}</div>
				) : !response.board ? (
					<NoConfig state={response.config.state} message={response.config.message} path={response.config.path} />
				) : (
					<Loaded
						board={response.board}
						url={url}
						layers={layers ?? DEFAULT_LAYERS}
						hiddenBoards={hiddenBoards}
						actions={actions}
						fetchError={fetchError}
						updatedAt={query.dataUpdatedAt}
					/>
				)}
			</div>
		</ReleaseMotionRoot>
	);
}

function Loaded({
	board,
	url,
	layers,
	hiddenBoards,
	actions,
	fetchError,
	updatedAt,
}: {
	board: Board;
	url: ReleasesSearch;
	layers: ReleasesSearch["layers"] & object;
	hiddenBoards: ReadonlySet<string>;
	actions: PageActions;
	fetchError: string | null;
	updatedAt: number;
}) {
	const lens = useMemo(() => makeLens(board, url, hiddenBoards), [board, url, hiddenBoards]);
	const now = useNow(30_000);
	const boardError = fetchError
		? `${fetchError}; board last read ${Math.max(0, Math.round((now - updatedAt) / 60_000))}m ago`
		: null;
	const sources = useMemo(
		() => ({ on: layers.src, idx: sourceIndex(board, now, boardError) }),
		[board, now, boardError, layers.src],
	);
	return (
		<SourcesProvider value={sources}>
			<TopBar board={board} url={url} lens={lens} actions={actions} idx={sources.idx} />
			<SummaryStrip board={board} lens={lens} history={layers.past} actions={actions} />
			<Timeline board={board} url={url} lens={lens} layers={layers} actions={actions} />
			<SheetStack board={board} url={url} lens={lens} actions={actions} />
		</SourcesProvider>
	);
}

function useNow(ms: number): number {
	const [now, setNow] = useState(() => Date.now());
	useEffect(() => {
		const id = window.setInterval(() => setNow(Date.now()), ms);
		return () => window.clearInterval(id);
	}, [ms]);
	return now;
}

function NoConfig({ state, message, path }: { state: string; message?: string; path: string }) {
	return (
		<div className="rel-empty">
			<div>
				<p style={{ fontWeight: 600, color: "var(--fg)", marginBottom: 8 }}>
					{state === "invalid" ? "The release train config has a problem" : "No release train config yet"}
				</p>
				{message && <p style={{ marginBottom: 8 }}>{message}</p>}
				<p>
					{state === "invalid" ? "Fix it, then check it with " : "Create one with "}
					<code>{state === "invalid" ? "ao train config validate" : "ao train config init"}</code>, then fill in{" "}
					<code>{path}</code>.
				</p>
			</div>
		</div>
	);
}
