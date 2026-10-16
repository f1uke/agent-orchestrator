import { useNavigate, useSearch } from "@tanstack/react-router";
import { useCallback, useEffect, useMemo } from "react";
import { useReleaseBoard } from "../../hooks/useReleaseBoard";
import { makeLens } from "../../lib/releases/lens";
import type { Board } from "../../lib/releases/types";
import {
	DEFAULT_LAYERS,
	parseReleasesSearch,
	releasesSearch,
	type ReleasesSearch,
	type SheetEntry,
} from "../../lib/releases/url";
import { saveSearch, useReleasesUi } from "../../stores/releases-ui";
import { ReleaseMotionRoot } from "./ReleaseMotion";
import { SheetStack } from "./SheetStack";
import { SummaryStrip } from "./SummaryStrip";
import { Timeline } from "./Timeline";
import { TopBar } from "./TopBar";
import "./releases.css";

export type PageActions = {
	setSearch: (patch: Partial<ReleasesSearch>) => void;
	openSheet: (s: SheetEntry, from?: "push") => void;
	closeSheet: () => void;
	back: () => void;
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
	const { pushBack, popBack, clearBack } = useReleasesUi();
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
		}),
		[setSearch, url.sheet, pushBack, popBack, clearBack],
	);

	const response = query.data;
	const fetchError = query.isError
		? query.error instanceof Error
			? query.error.message
			: "the daemon did not answer"
		: null;

	return (
		<ReleaseMotionRoot>
			<div className="rel" data-testid="releases-page">
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
	return (
		<>
			<TopBar board={board} url={url} lens={lens} actions={actions} fetchError={fetchError} updatedAt={updatedAt} />
			<SummaryStrip board={board} lens={lens} history={layers.past} actions={actions} />
			<Timeline board={board} url={url} lens={lens} layers={layers} actions={actions} />
			<SheetStack board={board} url={url} lens={lens} actions={actions} />
		</>
	);
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
