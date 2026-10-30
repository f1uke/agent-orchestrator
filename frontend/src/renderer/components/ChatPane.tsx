import { useVirtualizer } from "@tanstack/react-virtual";
import { Brain, ChevronRight, Code, type LucideIcon, MessageSquare, SquareTerminal } from "lucide-react";
import { memo, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { type ChatConnection, useChatSession } from "../hooks/useChatSession";
import { useSessionNames } from "../hooks/useSessionNames";
import type { ChatEntry } from "../lib/chat-event";
import { isObject } from "../lib/chat-event";
import type { ChatItem, Pending, UserItem } from "../lib/chat-fold";
import { type ChatRow, chatRows } from "../lib/chat-rows";
import type { ChatMux } from "../lib/chat-mux";
import { measuredOrFallbackRect } from "../lib/virtual-rect";
import { cn } from "../lib/utils";
import { type ChatView, centerViewOf, type Theme, useUiStore } from "../stores/ui-store";
import type { WorkspaceSession } from "../types/workspace";
import { PendingRequestCard, ResolvedLine, ToolCard } from "./ChatToolCard";
import { OrchestratorIcon } from "./icons";
import { NoteMarkdown } from "./NoteMarkdown";

export type ChatPaneView = "chat" | "events";

type ChatPaneProps = {
	session: WorkspaceSession;
	view: ChatPaneView;
	theme: Theme;
	daemonReady: boolean;
	createMux?: () => ChatMux;
};

export function ChatPane({ session, view, theme, daemonReady, createMux }: ChatPaneProps) {
	const chat = useChatSession(session.id, {
		daemonReady,
		sessionLive: session.status !== "terminated",
		createMux,
	});
	const rows = useMemo(() => chatRows(chat.transcript, chat.overlay), [chat.transcript, chat.overlay]);
	return (
		<div className="chat-pane" data-testid="chat-pane">
			{view === "events" ? (
				<ChatEvents entries={chat.entries} />
			) : (
				<ChatStream rows={rows} pending={chat.transcript.pending} theme={theme} />
			)}
			<footer className="chat-footer">
				<span>
					Read-only view. Send with <code>ao send</code> for now.
				</span>
				<span className="chat-footer__grow" />
				<ConnectionLabel connection={chat.connection} error={chat.error} />
				{chat.transcript.costUsd > 0 ? <span>{formatCost(chat.transcript.costUsd)} this session</span> : null}
			</footer>
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
					"chat-footer__dot",
					connection === "live" && "chat-footer__dot--live",
					connection === "error" && "chat-footer__dot--error",
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

function ChatStream({ rows, pending, theme }: { rows: ChatRow[]; pending: Pending[]; theme: Theme }) {
	const scrollRef = useRef<HTMLDivElement | null>(null);
	const atBottomRef = useRef(true);
	const [openTools, setOpenTools] = useState<Readonly<Record<string, boolean>>>({});
	const pendingByRequest = useMemo(() => new Map(pending.map((p) => [p.requestId, p])), [pending]);
	const sessionNames = useSessionNames();

	const virtualizer = useVirtualizer({
		count: rows.length,
		getScrollElement: () => scrollRef.current,
		estimateSize: () => 64,
		getItemKey: (index) => rows[index]?.key ?? index,
		overscan: 6,
		observeElementRect: measuredOrFallbackRect,
	});

	const toggleTool = useCallback((id: string, open: boolean) => {
		setOpenTools((prev) => ({ ...prev, [id]: open }));
	}, []);

	useLayoutEffect(() => {
		if (rows.length > 0 && atBottomRef.current) virtualizer.scrollToIndex(rows.length - 1, { align: "end" });
	}, [rows, virtualizer]);

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
								openTools={openTools}
								pendingByRequest={pendingByRequest}
								row={row}
								sessionNames={sessionNames}
								theme={theme}
								toggleTool={toggleTool}
							/>
						</div>
					);
				})}
			</div>
		</div>
	);
}

type RowProps = {
	row: ChatRow;
	theme: Theme;
	openTools: Readonly<Record<string, boolean>>;
	toggleTool: (id: string, open: boolean) => void;
	pendingByRequest: ReadonlyMap<string, Pending>;
	sessionNames: ReadonlyMap<string, string>;
};

const ChatRowView = memo(function ChatRowView({
	row,
	theme,
	openTools,
	toggleTool,
	pendingByRequest,
	sessionNames,
}: RowProps) {
	switch (row.kind) {
		case "pending":
			return <PendingRequestCard request={row.pending} theme={theme} />;
		case "streaming":
			return row.block.kind === "thinking" ? (
				<ThinkingRow streaming text={row.block.text} />
			) : (
				<div className="chat-stream-text" data-streaming>
					{row.block.text}
					{row.caret ? <span aria-hidden="true" className="chat-caret" /> : null}
				</div>
			);
		case "item":
			return (
				<ItemRow
					item={row.item}
					openTools={openTools}
					pendingByRequest={pendingByRequest}
					sessionNames={sessionNames}
					subagentTools={row.subagentTools}
					theme={theme}
					toggleTool={toggleTool}
				/>
			);
	}
});

function ItemRow({
	item,
	subagentTools,
	theme,
	openTools,
	toggleTool,
	pendingByRequest,
	sessionNames,
}: Omit<RowProps, "row"> & { item: ChatItem; subagentTools: number }) {
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
			return <ThinkingRow text={item.text} />;
		case "tool_call": {
			const permission = item.permission;
			if (permission?.status === "pending") {
				const request = pendingByRequest.get(permission.requestId);
				if (request) return <PendingRequestCard request={request} theme={theme} />;
			}
			if (item.name === "AskUserQuestion" && permission) return <ResolvedLine call={item} />;
			return (
				<>
					<ToolCard
						call={item}
						onToggle={(open) => toggleTool(item.id, open)}
						open={openTools[item.id]}
						subagentTools={subagentTools}
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

function ThinkingRow({ text, streaming = false }: { text: string; streaming?: boolean }) {
	const label = streaming ? "Thinking…" : "Thought";
	if (text.trim() === "") {
		return (
			<div className="chat-think">
				<span className="inline-flex items-center gap-1.5">
					<Brain className="chat-icon" aria-hidden="true" />
					{label}
				</span>
			</div>
		);
	}
	return (
		<details className="chat-think">
			<summary>
				<ChevronRight className="chat-chev" aria-hidden="true" />
				<Brain className="chat-icon" aria-hidden="true" />
				{label}
			</summary>
			<div className="chat-think__body">{text}</div>
		</details>
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
