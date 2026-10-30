import { useVirtualizer } from "@tanstack/react-virtual";
import { Code, CloudOff, type LucideIcon, MessageSquare, SquareTerminal } from "lucide-react";
import { memo, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useChatActions } from "../hooks/useChatActions";
import { type ChatConnection, type ChatSessionState, useChatSession } from "../hooks/useChatSession";
import { useSessionNames } from "../hooks/useSessionNames";
import type { ChatEntry } from "../lib/chat-event";
import { isObject, unfinished } from "../lib/chat-event";
import type { ChatApi } from "../lib/chat-api";
import type { BackgroundTask, ChatItem, Pending, UserItem } from "../lib/chat-fold";
import { sessionActivity } from "../lib/chat-inspect";
import { type ChatRow, chatRows, rowIndexOfItem } from "../lib/chat-rows";
import type { ChatMux } from "../lib/chat-mux";
import { measuredOrFallbackRect } from "../lib/virtual-rect";
import { cn } from "../lib/utils";
import { type ChatView, centerViewOf, type Theme, useUiStore } from "../stores/ui-store";
import type { WorkspaceSession } from "../types/workspace";
import { ChatComposer } from "./ChatComposer";
import { type AnswerHandler, PendingRequestCard, type RequestState, ResolvedLine, ToolCard } from "./ChatToolCard";
import { GroupRow, type OpenRows, ThinkingRow, type ToggleRow, TurnRow } from "./ChatProcessRows";
import { OrchestratorIcon } from "./icons";
import { NoteMarkdown } from "./NoteMarkdown";

export type ChatPaneView = "chat" | "events";

export type ChatJump = { itemIndex: number };

type ChatPaneProps = {
	session: WorkspaceSession;
	view: ChatPaneView;
	theme: Theme;
	daemonReady: boolean;
	chat?: ChatSessionState;
	jumpTo?: ChatJump | null;
	onJumped?: () => void;
	createMux?: () => ChatMux;
	chatApi?: ChatApi;
};

type RequestStates = Readonly<Record<string, RequestState>>;

const TOAST_MS = 4000;

function useToast(): [string | null, (message: string) => void] {
	const [toast, setToast] = useState<string | null>(null);
	const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
	useEffect(
		() => () => {
			if (timer.current) clearTimeout(timer.current);
		},
		[],
	);
	const show = useCallback((message: string) => {
		setToast(message);
		if (timer.current) clearTimeout(timer.current);
		timer.current = setTimeout(() => setToast(null), TOAST_MS);
	}, []);
	return [toast, show];
}

export function ChatPane({
	session,
	view,
	theme,
	daemonReady,
	chat: given,
	jumpTo,
	onJumped,
	createMux,
	chatApi,
}: ChatPaneProps) {
	const own = useChatSession(given ? undefined : session.id, {
		daemonReady,
		sessionLive: session.status !== "terminated",
		createMux,
	});
	const chat = given ?? own;
	const actions = useChatActions(session.id, chat.connection, chatApi);
	const [requests, setRequests] = useState<RequestStates>({});
	const [toast, showToast] = useToast();
	const { transcript } = chat;

	const queuedIds = useMemo(() => new Set(transcript.queued.filter((uuid) => uuid !== "")), [transcript.queued]);
	const queued = useMemo(
		() => transcript.items.filter((i): i is UserItem => i.kind === "user" && queuedIds.has(i.uuid)),
		[transcript.items, queuedIds],
	);
	const root = session.workspacePath ?? "";
	const rows = useMemo(
		() => chatRows(transcript, chat.overlay, queuedIds, root),
		[transcript, chat.overlay, queuedIds, root],
	);
	const runningInBackground = useMemo(
		() => sessionActivity(transcript).filter((a) => unfinished(a.state)).length,
		[transcript],
	);
	const requestInspectorView = useUiStore((state) => state.requestInspectorView);

	useEffect(() => {
		const open = new Set(transcript.pending.map((p) => p.requestId));
		setRequests((prev) => {
			const kept = Object.entries(prev).filter(([id]) => open.has(id));
			return kept.length === Object.keys(prev).length ? prev : Object.fromEntries(kept);
		});
	}, [transcript.pending]);

	const { answer: sendAnswer, setMode } = actions;
	const onAnswer = useCallback<AnswerHandler>(
		(body, then) => {
			const id = body.requestId;
			setRequests((prev) => ({ ...prev, [id]: { phase: "sending" } }));
			void sendAnswer(body).then(async (result) => {
				if (!result.ok) {
					setRequests((prev) => {
						const { [id]: _, ...rest } = prev;
						return result.error.kind === "host_not_running"
							? rest
							: { ...rest, [id]: { phase: "failed", message: result.error.message } };
					});
					return;
				}
				if (!then) return;
				const moded = await setMode(then);
				if (!moded.ok && moded.error.kind !== "host_not_running") showToast(moded.error.message);
			});
		},
		[sendAnswer, setMode, showToast],
	);

	const onKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
		if (e.key !== "Escape" || e.defaultPrevented || !transcript.busy || actions.hostDown) return;
		const target = e.target as Element;
		if (!e.currentTarget.contains(target) || target.closest("[data-pending]")) return;
		e.preventDefault();
		void actions.interrupt().then((result) => {
			if (!result.ok && result.error.kind !== "host_not_running") showToast(result.error.message);
		});
	};

	return (
		<div className="chat-pane" data-testid="chat-pane" onKeyDown={onKeyDown}>
			{view === "events" ? (
				<ChatEvents entries={chat.entries} />
			) : (
				<ChatStream
					disabled={actions.hostDown}
					jumpTo={jumpTo}
					onAnswer={onAnswer}
					onJumped={onJumped}
					pending={transcript.pending}
					requests={requests}
					root={root}
					rows={rows}
					theme={theme}
				/>
			)}
			<div className="chat-bottom">
				{actions.hostDown ? (
					<div className="chat-banner" role="alert">
						<CloudOff className="chat-icon" aria-hidden="true" />
						Session is asleep or restarting. Sending and answering resume once it is back.
					</div>
				) : null}
				{view === "chat" ? (
					<ChatComposer
						actions={actions}
						busy={transcript.busy}
						commands={transcript.commands}
						model={transcript.model}
						models={transcript.models}
						onToast={showToast}
						permissionMode={transcript.permissionMode}
						queued={queued}
						sessionId={session.id}
					/>
				) : null}
				<div className="chat-statusline">
					<ConnectionLabel connection={chat.connection} error={chat.error} />
					{transcript.costUsd > 0 ? <span>{formatCost(transcript.costUsd)} this session</span> : null}
					{runningInBackground > 0 ? (
						<button className="chat-statusline__bg" onClick={() => requestInspectorView("session")} type="button">
							<span className="chat-statusline__dot chat-statusline__dot--live" />
							{runningInBackground} running in background
						</button>
					) : null}
					<span className="chat-statusline__grow" />
					{view === "chat" ? <span>Enter sends · Shift+Enter newline · Esc interrupts</span> : null}
				</div>
			</div>
			{toast ? (
				<div className="chat-toast" role="status">
					{toast}
				</div>
			) : null}
		</div>
	);
}

const VIEW_OPTIONS: Array<{ view: ChatView; label: string; Icon: LucideIcon }> = [
	{ view: "chat", label: "Chat", Icon: MessageSquare },
	{ view: "terminal", label: "Terminal", Icon: SquareTerminal },
	{ view: "events", label: "Events", Icon: Code },
];

export function ChatViewToggle({ session, style }: { session: WorkspaceSession; style?: React.CSSProperties }) {
	const chatViews = useUiStore((state) => state.chatViews);
	const setChatView = useUiStore((state) => state.setChatView);
	if (session.transport !== "chat") return null;
	const current = centerViewOf(session, chatViews);
	return (
		<div aria-label="Session view" className="chat-viewseg" role="group" style={style}>
			{VIEW_OPTIONS.map(({ view, label, Icon }) => (
				<button
					aria-pressed={current === view}
					className="chat-viewseg__btn"
					key={view}
					onClick={() => setChatView(session.id, view)}
					type="button"
				>
					<Icon className="chat-icon" aria-hidden="true" />
					{label}
				</button>
			))}
		</div>
	);
}

const CONNECTION_LABEL: Record<ChatConnection, string> = {
	connecting: "Loading the session log…",
	live: "Live",
	offline: "No session host running",
	reconnecting: "Reconnecting…",
	error: "Stream error",
};

function ConnectionLabel({ connection, error }: { connection: ChatConnection; error?: string }) {
	return (
		<span data-connection={connection} title={error}>
			<span
				className={cn(
					"chat-statusline__dot",
					connection === "live" && "chat-statusline__dot--live",
					connection === "error" && "chat-statusline__dot--error",
				)}
			/>
			{connection === "error" && error ? `Stream error: ${error}` : CONNECTION_LABEL[connection]}
		</span>
	);
}

export function formatCost(usd: number): string {
	return `$${usd.toFixed(usd < 1 ? 3 : 2)}`;
}

const STICK_TO_BOTTOM_PX = 48;

type StreamProps = {
	rows: ChatRow[];
	root: string;
	pending: Pending[];
	theme: Theme;
	requests: RequestStates;
	disabled: boolean;
	onAnswer: AnswerHandler;
	jumpTo?: ChatJump | null;
	onJumped?: () => void;
};

function ChatStream({ rows, root, pending, theme, requests, disabled, onAnswer, jumpTo, onJumped }: StreamProps) {
	const scrollRef = useRef<HTMLDivElement | null>(null);
	const atBottomRef = useRef(true);
	const [openRows, setOpenRows] = useState<OpenRows>({});
	const pendingByRequest = useMemo(() => new Map(pending.map((p) => [p.requestId, p])), [pending]);
	const sessionNames = useSessionNames();
	const answers = useMemo(() => ({ requests, disabled, onAnswer }), [requests, disabled, onAnswer]);

	const virtualizer = useVirtualizer({
		count: rows.length,
		getScrollElement: () => scrollRef.current,
		estimateSize: () => 64,
		getItemKey: (index) => rows[index]?.key ?? index,
		overscan: 6,
		observeElementRect: measuredOrFallbackRect,
		// Rows measure themselves from a ref callback, inside React's commit; a
		// synchronous flush from there is refused with a console error mid-scroll.
		useFlushSync: false,
	});

	const toggleRow = useCallback<ToggleRow>((key, open) => {
		setOpenRows((prev) => ({ ...prev, [key]: open }));
	}, []);

	useLayoutEffect(() => {
		if (rows.length > 0 && atBottomRef.current) virtualizer.scrollToIndex(rows.length - 1, { align: "end" });
	}, [rows, virtualizer]);

	useLayoutEffect(() => {
		if (!jumpTo) return;
		const index = rowIndexOfItem(rows, jumpTo.itemIndex);
		if (index < 0) return;
		atBottomRef.current = false;
		virtualizer.scrollToIndex(index, { align: "start" });
		onJumped?.();
	}, [jumpTo, onJumped, rows, virtualizer]);

	const onScroll = useCallback(() => {
		const el = scrollRef.current;
		if (!el) return;
		atBottomRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < STICK_TO_BOTTOM_PX;
	}, []);

	if (rows.length === 0) {
		return (
			<div className="chat-scroll">
				<div className="chat-empty">Nothing in this session's log yet.</div>
			</div>
		);
	}

	return (
		<div className="chat-scroll" data-testid="chat-stream" onScroll={onScroll} ref={scrollRef}>
			<div className="chat-col" style={{ height: virtualizer.getTotalSize() }}>
				{virtualizer.getVirtualItems().map((v) => {
					const row = rows[v.index];
					return (
						<div
							className="chat-row"
							data-index={v.index}
							data-row={row.kind === "item" ? row.item.kind : row.kind}
							key={v.key}
							ref={virtualizer.measureElement}
							style={{ transform: `translateY(${v.start}px)` }}
						>
							<ChatRowView
								answers={answers}
								openRows={openRows}
								pendingByRequest={pendingByRequest}
								root={root}
								row={row}
								sessionNames={sessionNames}
								theme={theme}
								toggleRow={toggleRow}
							/>
						</div>
					);
				})}
			</div>
		</div>
	);
}

type CardAnswers = { requests: RequestStates; disabled: boolean; onAnswer: AnswerHandler };

type RowProps = {
	row: ChatRow;
	answers: CardAnswers;
	theme: Theme;
	root: string;
	openRows: OpenRows;
	toggleRow: ToggleRow;
	pendingByRequest: ReadonlyMap<string, Pending>;
	sessionNames: ReadonlyMap<string, string>;
};

function RequestCard({ request, theme, answers }: { request: Pending; theme: Theme; answers: CardAnswers }) {
	return (
		<PendingRequestCard
			disabled={answers.disabled}
			key={request.requestId}
			onAnswer={answers.onAnswer}
			request={request}
			state={answers.requests[request.requestId]}
			theme={theme}
		/>
	);
}

const ChatRowView = memo(function ChatRowView(props: RowProps) {
	const { row, answers, theme, openRows, toggleRow, pendingByRequest, root } = props;
	switch (row.kind) {
		case "pending":
			return <RequestCard answers={answers} request={row.pending} theme={theme} />;
		case "streaming":
			return row.block.kind === "thinking" ? (
				<ThinkingRow
					onToggle={(open) => toggleRow(row.key, open)}
					open={openRows[row.key]}
					streaming
					text={row.block.text}
				/>
			) : (
				<div className="chat-stream-text" data-streaming>
					{row.block.text}
					{row.caret ? <span aria-hidden="true" className="chat-caret" /> : null}
				</div>
			);
		case "group":
			return <GroupRow openRows={openRows} root={root} row={row} theme={theme} toggleRow={toggleRow} />;
		case "turn":
			return (
				<TurnRow
					openRows={openRows}
					renderRow={(inner) => <ChatRowView {...props} row={inner} />}
					row={row}
					toggleRow={toggleRow}
				/>
			);
		case "item":
			return (
				<ItemRow
					answers={answers}
					item={row.item}
					openRows={openRows}
					pendingByRequest={pendingByRequest}
					root={root}
					rowKey={row.key}
					sessionNames={props.sessionNames}
					subagentTools={row.subagentTools}
					task={row.task}
					theme={theme}
					toggleRow={toggleRow}
				/>
			);
	}
});

function ItemRow({
	answers,
	item,
	rowKey,
	subagentTools,
	task,
	theme,
	root,
	openRows,
	toggleRow,
	pendingByRequest,
	sessionNames,
}: Omit<RowProps, "row"> & { item: ChatItem; rowKey: string; subagentTools: number; task?: BackgroundTask }) {
	switch (item.kind) {
		case "user":
			return item.origin ? (
				<PeerMessage item={item} sessionNames={sessionNames} theme={theme} />
			) : (
				<UserMessage item={item} />
			);
		case "assistant_text":
			return (
				<div className="note-prose chat-md">
					<NoteMarkdown source={item.text} theme={theme} />
				</div>
			);
		case "thinking":
			return <ThinkingRow onToggle={(open) => toggleRow(rowKey, open)} open={openRows[rowKey]} text={item.text} />;
		case "tool_call": {
			const permission = item.permission;
			if (permission?.status === "pending") {
				const request = pendingByRequest.get(permission.requestId);
				if (request) return <RequestCard answers={answers} request={request} theme={theme} />;
			}
			if (item.name === "AskUserQuestion" && permission) return <ResolvedLine call={item} />;
			return (
				<>
					<ToolCard
						call={item}
						onToggle={(open) => toggleRow(item.id, open)}
						open={openRows[item.id]}
						root={root}
						subagentTools={subagentTools}
						task={task}
						theme={theme}
					/>
					<ResolvedLine call={item} />
				</>
			);
		}
		case "turn_end":
			return (
				<div className={cn("chat-sys", item.isError && "chat-sys--error")} data-notice="turn_end">
					{item.isError ? `Turn ended: ${item.subtype.replace(/_/g, " ")}` : "Turn complete"}
					{item.costUsd > 0 ? ` · ${formatCost(item.costUsd)} this session` : ""}
				</div>
			);
		case "notice":
			return <NoticeRow notice={item} />;
	}
}

const shortId = (id: string) => (id.length > 12 ? `${id.slice(0, 4)}…${id.slice(-4)}` : id);

function NoticeRow({ notice }: { notice: Extract<ChatItem, { kind: "notice" }> }) {
	const text = {
		spawn: "Session started",
		resumed: `Resumed claude session ${shortId(notice.text)}`,
		exit: `Claude exited · ${notice.text}`,
		compacted: "Conversation compacted",
		interrupted: "Interrupted",
	}[notice.notice];
	return (
		<div className={cn("chat-sys", notice.notice === "interrupted" && "chat-sys--warn")} data-notice={notice.notice}>
			{text}
		</div>
	);
}

const USER_STATE_LABEL: Record<string, string> = {
	sent: "sent",
	queued: "queued",
	started: "running",
	cancelled: "cancelled",
};

function UserMessage({ item }: { item: UserItem }) {
	const meta = [
		USER_STATE_LABEL[item.state],
		item.images > 0 ? (item.images === 1 ? "1 image" : `${item.images} images`) : undefined,
	].filter(Boolean);
	return (
		<div className="chat-user" data-state={item.state}>
			<div className="chat-user__bubble">{item.text}</div>
			{meta.length ? <div className="chat-user__meta">{meta.join(" · ")}</div> : null}
		</div>
	);
}

function PeerMessage({
	item,
	sessionNames,
	theme,
}: {
	item: UserItem;
	sessionNames: ReadonlyMap<string, string>;
	theme: Theme;
}) {
	const origin = item.origin!;
	const orchestrator = origin.from.endsWith("-orchestrator");
	const name = orchestrator ? "orchestrator" : (sessionNames.get(origin.from) ?? `@${origin.from}`);
	const body = item.text.replace(/^\[from @[^\]]*\]\s*/, "");
	const Icon = orchestrator ? OrchestratorIcon : MessageSquare;
	return (
		<div className={cn("chat-peer", orchestrator && "chat-peer--orchestrator")} data-origin={origin.via}>
			<div className="chat-peer__from">
				<Icon className="chat-icon" aria-hidden="true" />
				<b>{name}</b> · {orchestrator ? "orchestrator message" : "peer message"}
				<span className="chat-peer__via">{origin.via.replace(/_/g, " ")}</span>
			</div>
			<div className="note-prose chat-md">
				<NoteMarkdown source={body} theme={theme} />
			</div>
		</div>
	);
}

const EVENT_ROW_PX = 24;
const JSON_PREVIEW_CHARS = 400;

function eventType(entry: ChatEntry): { label: string; tone: "control" | "host" | "" } {
	const payload = isObject(entry.payload) ? entry.payload : {};
	if (entry.dir === "host") return { label: `host/${String(payload.kind ?? "?")}`, tone: "host" };
	const type = typeof payload.type === "string" ? payload.type : "?";
	const request = isObject(payload.request) ? payload.request : undefined;
	const subtype = typeof payload.subtype === "string" ? payload.subtype : request?.subtype;
	const label = typeof subtype === "string" && subtype !== "" ? `${type}/${subtype}` : type;
	return { label, tone: type.startsWith("control_") ? "control" : "" };
}

function formatTime(ts: string): string {
	const d = new Date(ts);
	if (Number.isNaN(d.getTime())) return ts;
	const pad = (n: number, w = 2) => String(n).padStart(w, "0");
	return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}.${pad(d.getMilliseconds(), 3)}`;
}

function ChatEvents({ entries }: { entries: readonly ChatEntry[] }) {
	const scrollRef = useRef<HTMLDivElement | null>(null);
	const virtualizer = useVirtualizer({
		count: entries.length,
		getScrollElement: () => scrollRef.current,
		estimateSize: () => EVENT_ROW_PX,
		overscan: 20,
		observeElementRect: measuredOrFallbackRect,
	});
	const atBottomRef = useRef(true);
	useEffect(() => {
		if (entries.length > 0 && atBottomRef.current) virtualizer.scrollToIndex(entries.length - 1, { align: "end" });
	}, [entries.length, virtualizer]);
	const onScroll = () => {
		const el = scrollRef.current;
		if (el) atBottomRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < STICK_TO_BOTTOM_PX;
	};
	return (
		<div className="chat-events" data-testid="chat-events" onScroll={onScroll} ref={scrollRef}>
			<div className="relative" style={{ height: virtualizer.getTotalSize() }}>
				{virtualizer.getVirtualItems().map((v) => {
					const entry = entries[v.index];
					const { label, tone } = eventType(entry);
					return (
						<div className="chat-ev" data-seq={entry.seq} key={v.key} style={{ transform: `translateY(${v.start}px)` }}>
							<span className="chat-ev__seq">{entry.seq}</span>
							<span className="chat-ev__ts">{formatTime(entry.ts)}</span>
							<span className="chat-ev__dir">{entry.dir}</span>
							<span className={cn("chat-ev__type", tone && `chat-ev__type--${tone}`)} title={label}>
								{label}
							</span>
							<span className="chat-ev__json">{JSON.stringify(entry.payload).slice(0, JSON_PREVIEW_CHARS)}</span>
						</div>
					);
				})}
			</div>
		</div>
	);
}
