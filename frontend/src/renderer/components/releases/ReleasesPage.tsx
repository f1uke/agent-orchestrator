import { useNavigate, useSearch } from "@tanstack/react-router";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useReleaseBoard } from "../../hooks/useReleaseBoard";
import { focusTrain, makeLens } from "../../lib/releases/lens";
import { sourceIndex } from "../../lib/releases/sources";
import { locate, sheetReveal, type RevealView } from "../../lib/releases/locate";
import type { Board, Subject } from "../../lib/releases/types";
import {
	DEFAULT_LAYERS,
	parseReleasesSearch,
	releasesSearch,
	type Layers,
	sheetParam,
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
	reveal: (s: Subject) => void;
	whereIs: (s: Subject) => string | null;
	openPart: (s: SheetEntry, part: string | null) => void;
};

export function ReleasesPage() {
	const raw = useSearch({ strict: false }) as Record<string, unknown>;
	const url = useMemo(() => parseReleasesSearch(raw), [raw]);
	const navigate = useNavigate();
	const query = useReleaseBoard();
	const response = query.data;
	const board = response?.board ?? null;
	const stored = useReleasesUi((s) => s.layers);
	const layers = url.layers ?? stored;
	const storedHidden = useReleasesUi((s) => s.hidden);
	const hiddenBoards = url.hideBoards ?? storedHidden;
	const { pushBack, popBack, clearBack, setLayers, setHidden, openFolds, setPending } = useReleasesUi();
	useEffect(() => saveSearch(releasesSearch(url)), [url]);
	const view: RevealView = useMemo(
		() => ({
			layers,
			hiddenBoards,
			apps: url.apps,
			focus: board ? (focusTrain(board, url.focus)?.id ?? null) : null,
			me: url.me,
		}),
		[layers, hiddenBoards, url.apps, url.focus, url.me, board],
	);

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
			reveal: (subject) => {
				if (!board) return;
				const r = locate(subject, board, view);
				const patch: Partial<ReleasesSearch> = {};
				if (r.notMine && url.me) patch.me = false;
				const nextLayers = { ...layers, ...Object.fromEntries(r.layers.map((k) => [k, true])) };
				if (r.layers.some((k) => !layers[k])) {
					setLayers(nextLayers);
					if (url.layers) patch.layers = nextLayers;
				}
				if (r.unhide.some((k) => hiddenBoards.has(k))) {
					const keys = new Set([...hiddenBoards].filter((k) => !r.unhide.includes(k)));
					setHidden(keys);
					if (url.hideBoards) patch.hideBoards = keys;
				}
				if (url.apps && r.apps.some((a) => !url.apps?.has(a))) patch.apps = new Set([...url.apps, ...r.apps]);
				if (r.hotfixes && !url.hotfixes) patch.hotfixes = true;
				if (r.focus && r.focus !== view.focus) patch.focus = r.focus;
				openFolds(r.folds);
				setPending(r);
				if (Object.keys(patch).length) setSearch(patch);
			},
			whereIs: (subject) => (board ? locate(subject, board, view).fallback : null),
			openPart: (s, part) => {
				const here = url.sheet && sheetParam(url.sheet) === sheetParam(s);
				if (!here) {
					if (url.sheet) pushBack(url.sheet);
					setSearch({ sheet: s });
				}
				if (part) setPending(sheetReveal(part));
			},
		}),
		[
			setSearch,
			url.sheet,
			url.layers,
			url.hideBoards,
			url.apps,
			url.hotfixes,
			url.me,
			view,
			board,
			layers,
			hiddenBoards,
			pushBack,
			popBack,
			clearBack,
			setLayers,
			setHidden,
			openFolds,
			setPending,
		],
	);

	const fetchError = query.isError
		? query.error instanceof Error
			? query.error.message
			: "the daemon did not answer"
		: null;

	return (
		<ReleaseMotionRoot>
			<div className="rel" data-testid="releases-page" data-src={layers.src || undefined}>
				<div className="rel-halos" aria-hidden="true" />
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
