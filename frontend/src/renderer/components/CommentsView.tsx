import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { useSessionPRComments, type PRCommentGroup } from "../hooks/useSessionPRComments";
import { useReplyToThread, useResolveThread } from "../hooks/useThreadActions";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import {
	ACCENT,
	PALETTE as P,
	accentMix,
	avatarBg,
	baseName,
	genPrompt,
	initialsFor,
	providerBadge,
	relativeTime,
	splitBodyRuns,
	STATUS_COLORS,
	statusFor,
} from "../lib/comment-inbox";

type Group = PRCommentGroup;
type Thread = Group["threads"][number];
type PRFacts = NonNullable<components["schemas"]["ControllersSessionView"]["prs"]>[number];

const MONO = 'ui-monospace, "SF Mono", Menlo, monospace';
const TOAST_MS = 2800;

/**
 * Comments tab — the "Unresolved inbox": a cross-PR list of unresolved review
 * comments (grouped PR → file → comment) with per-comment Resolve / Reply /
 * Send-to-worker actions, a multi-select batch mode, and toasts. Pixel-matched
 * to the Comments Inbox design (exact dark palette + #3b82f6 accent). Wired to
 * the existing comment-resolve / comment-reply / comment-dispatch / send
 * endpoints; the auto-send strip drives the per-session auto-nudge override.
 */
export function CommentsView({ sessionId }: { sessionId: string }) {
	const query = useSessionPRComments(sessionId);
	const queryClient = useQueryClient();

	// Session read model feeds the per-PR status pill (from prs) and the
	// auto-send override; settings feeds the global auto-send default.
	const sessionQuery = useQuery({
		queryKey: ["session", sessionId, "autoNudge"],
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}", {
				params: { path: { sessionId } },
			});
			if (error) throw new Error(apiErrorMessage(error));
			return data.session;
		},
	});
	const settingsQuery = useQuery({
		queryKey: ["settings", "autoNudge"],
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/settings/auto-nudge", {});
			if (error) throw new Error(apiErrorMessage(error));
			return data;
		},
	});
	const factsByUrl = useMemo(() => {
		const m = new Map<string, PRFacts>();
		for (const pr of sessionQuery.data?.prs ?? []) m.set(pr.url, pr);
		return m;
	}, [sessionQuery.data]);

	const override = sessionQuery.data?.autoNudgeComments ?? null;
	const globalDefault = settingsQuery.data?.enabled ?? false;
	const autoOn = override !== null ? override : globalDefault;
	const setOverride = useMutation({
		mutationFn: async (next: boolean) => {
			const { error } = await apiClient.PUT("/api/v1/sessions/{sessionId}/auto-nudge", {
				params: { path: { sessionId } },
				body: { override: next },
			});
			if (error) throw new Error(apiErrorMessage(error));
		},
		onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["session", sessionId, "autoNudge"] }),
	});

	// --- toast ---------------------------------------------------------------
	const [toast, setToast] = useState<string | null>(null);
	const toastTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
	const showToast = (msg: string) => {
		setToast(msg);
		if (toastTimer.current) clearTimeout(toastTimer.current);
		toastTimer.current = setTimeout(() => setToast(null), TOAST_MS);
	};
	useEffect(() => () => void (toastTimer.current && clearTimeout(toastTimer.current)), []);

	// --- select / batch ------------------------------------------------------
	const [selectMode, setSelectMode] = useState(false);
	const [selected, setSelected] = useState<Set<string>>(new Set());
	const toggleSelect = (id: string) =>
		setSelected((s) => {
			const n = new Set(s);
			n.has(id) ? n.delete(id) : n.add(id);
			return n;
		});
	const exitSelect = () => {
		setSelectMode((v) => !v);
		setSelected(new Set());
	};

	// --- single-open caret menu (per-thread or "batch"), outside-click close --
	const [menuOpen, setMenuOpen] = useState<string | null>(null);
	useEffect(() => {
		if (!menuOpen) return;
		const onDoc = (e: MouseEvent) => {
			const el = e.target as HTMLElement;
			if (!el.closest?.("[data-menu]")) setMenuOpen(null);
		};
		document.addEventListener("mousedown", onDoc);
		return () => document.removeEventListener("mousedown", onDoc);
	}, [menuOpen]);

	// --- mutations -----------------------------------------------------------
	const reply = useReplyToThread(sessionId);
	const resolve = useResolveThread(sessionId);
	const dispatch = useMutation({
		mutationFn: async (vars: { prUrl: string; threadId: string; extraPrompt?: string }) => {
			const { error } = await apiClient.POST("/api/v1/sessions/{sessionId}/comment-dispatch", {
				params: { path: { sessionId } },
				body: vars,
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to send"));
		},
	});
	const send = useMutation({
		mutationFn: async (message: string) => {
			const { error } = await apiClient.POST("/api/v1/sessions/{sessionId}/send", {
				params: { path: { sessionId } },
				body: { message },
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to send"));
		},
	});
	const busy = resolve.isPending || dispatch.isPending || send.isPending;

	// --- derive groups (unresolved) + resolved list --------------------------
	const groups = query.data ?? [];
	const unresolved = groups
		.map((g) => ({ group: g, threads: g.threads.filter((t) => !t.resolved) }))
		.filter((g) => g.threads.length > 0);
	const totalUnresolved = unresolved.reduce((n, g) => n + g.threads.length, 0);
	const resolvedItems = groups.flatMap((g) => g.threads.filter((t) => t.resolved).map((t) => ({ group: g, thread: t })));

	// threadId → {prUrl, thread} for batch actions
	const byId = useMemo(() => {
		const m = new Map<string, { prUrl: string; thread: Thread }>();
		for (const { group, threads } of unresolved) for (const t of threads) m.set(t.threadId, { prUrl: group.prUrl, thread: t });
		return m;
	}, [unresolved]);
	const selectedIds = [...selected].filter((id) => byId.has(id));

	// --- action handlers -----------------------------------------------------
	const doResolve = (prUrl: string, threadId: string) => {
		resolve.mutate({ prUrl, threadId });
		showToast("Comment resolved");
	};
	const doReply = (prUrl: string, threadId: string, body: string, author: string) => {
		reply.mutate({ prUrl, threadId, body });
		showToast(`Replied to ${author || "author"}`);
	};
	const doSendQuick = (prUrl: string, threadId: string, path: string) => {
		setMenuOpen(null);
		dispatch.mutate({ prUrl, threadId });
		showToast(`Sent to worker · ${baseName(path)}`);
	};
	const doSendPrompt = (prUrl: string, threadId: string, message: string, resolveAfter: boolean) => {
		send.mutate(message);
		if (resolveAfter) resolve.mutate({ prUrl, threadId });
		showToast(resolveAfter ? "Prompt sent to worker · resolving" : "Prompt sent to worker");
	};
	const batchResolve = () => {
		selectedIds.forEach((id) => {
			const it = byId.get(id);
			if (it) resolve.mutate({ prUrl: it.prUrl, threadId: it.thread.threadId });
		});
		showToast(`${selectedIds.length} comment${selectedIds.length > 1 ? "s" : ""} resolved`);
		setSelected(new Set());
	};
	const batchSeparate = () => {
		setMenuOpen(null);
		selectedIds.forEach((id) => {
			const it = byId.get(id);
			if (it) dispatch.mutate({ prUrl: it.prUrl, threadId: it.thread.threadId });
		});
		showToast(`Fanned out ${selectedIds.length} worker tasks`);
	};
	const batchOneTask = () => {
		setMenuOpen(null);
		const message = selectedIds
			.map((id) => {
				const t = byId.get(id)?.thread;
				const first = t?.comments[0];
				return t ? genPrompt(t.path, t.line, first?.body ?? "") : "";
			})
			.filter(Boolean)
			.join("\n\n---\n\n");
		if (message) send.mutate(message);
		showToast(`${selectedIds.length} comments → 1 worker task`);
	};

	// --- render --------------------------------------------------------------
	const loading = query.isLoading;
	const err = query.error ? apiErrorMessage(query.error, "Unable to load comments") : null;

	return (
		<div
			role="tabpanel"
			style={{
				position: "relative",
				display: "flex",
				flexDirection: "column",
				height: "100%",
				minHeight: 0,
				background: P.rail,
				color: P.text,
			}}
		>
			<InboxHeader
				total={totalUnresolved}
				prCount={unresolved.length}
				selectMode={selectMode}
				onToggleSelect={exitSelect}
			/>
			<AutoSendStrip
				on={autoOn}
				busy={settingsQuery.isLoading || sessionQuery.isLoading || setOverride.isPending}
				onToggle={(next) => {
					setOverride.mutate(next);
					showToast(next ? "Auto-send on · new comments dispatch automatically" : "Auto-send off");
				}}
			/>

			<div style={{ flex: 1, overflowY: "auto", padding: "10px 12px 24px" }}>
				{loading && <p style={{ padding: 16, fontSize: 12.5, color: P.muted2 }}>Loading comments…</p>}
				{!loading && err && <p style={{ padding: 16, fontSize: 12.5, color: P.red }}>{err}</p>}

				{!loading &&
					!err &&
					unresolved.map(({ group, threads }) => (
						<PRGroupView
							key={group.prUrl}
							group={group}
							threads={threads}
							facts={factsByUrl.get(group.prUrl) ?? factsByUrl.get(group.htmlUrl)}
							sessionId={sessionId}
							selectMode={selectMode}
							selected={selected}
							onToggleSelect={toggleSelect}
							menuOpen={menuOpen}
							setMenuOpen={setMenuOpen}
							busy={busy}
							onResolve={doResolve}
							onReply={doReply}
							onSendQuick={doSendQuick}
							onSendPrompt={doSendPrompt}
						/>
					))}

				{!loading && !err && totalUnresolved === 0 && <EmptyState />}

				{!loading && !err && resolvedItems.length > 0 && <ResolvedSection items={resolvedItems} />}
			</div>

			{selectMode && selectedIds.length > 0 && (
				<BatchBar
					count={selectedIds.length}
					menuOpen={menuOpen === "batch"}
					setMenuOpen={(o) => setMenuOpen(o ? "batch" : null)}
					onResolve={batchResolve}
					onOneTask={batchOneTask}
					onSeparate={batchSeparate}
				/>
			)}

			{toast && <Toast text={toast} />}
		</div>
	);
}

// ---------------------------------------------------------------------------

function InboxHeader({
	total,
	prCount,
	selectMode,
	onToggleSelect,
}: {
	total: number;
	prCount: number;
	selectMode: boolean;
	onToggleSelect: () => void;
}) {
	return (
		<div style={{ flex: "none", padding: "16px 16px 12px", borderBottom: `1px solid ${P.divider}` }}>
			<div style={{ display: "flex", alignItems: "baseline", gap: 9 }}>
				<span style={{ fontSize: 16, fontWeight: 700, color: P.textStrong }}>Unresolved</span>
				<span style={pill(12, P.secondary)}>{total}</span>
				<span style={{ fontSize: 11.5, color: P.muted2 }}>
					across {prCount} open PR{prCount === 1 ? "" : "s"}
				</span>
				<div style={{ flex: 1 }} />
				<button
					type="button"
					onClick={onToggleSelect}
					style={{
						fontSize: 11.5,
						fontWeight: 600,
						padding: "4px 11px",
						borderRadius: 7,
						cursor: "pointer",
						border: `1px solid ${selectMode ? accentMix(45) : P.borderPill}`,
						color: selectMode ? ACCENT : P.secondary,
						background: selectMode ? accentMix(12) : "transparent",
					}}
				>
					{selectMode ? "Done" : "Select"}
				</button>
			</div>
		</div>
	);
}

function AutoSendStrip({ on, busy, onToggle }: { on: boolean; busy: boolean; onToggle: (next: boolean) => void }) {
	return (
		<div
			style={{
				flex: "none",
				display: "flex",
				alignItems: "center",
				gap: 14,
				padding: "13px 16px",
				borderBottom: `1px solid ${P.divider}`,
				background: on ? accentMix(6) : "transparent",
			}}
		>
			<div style={{ flex: 1, minWidth: 0 }}>
				<div style={{ display: "flex", alignItems: "center", gap: 7 }}>
					<span style={{ flex: "none", width: 16, textAlign: "center", fontSize: 12, color: on ? ACCENT : P.muted2 }}>
						⚡
					</span>
					<span style={{ fontSize: 13, fontWeight: 600, color: P.text }}>
						Auto-send unresolved comments to worker
					</span>
				</div>
			</div>
			<button
				type="button"
				role="switch"
				aria-checked={on}
				aria-label="Auto-send unresolved comments to worker"
				disabled={busy}
				onClick={() => onToggle(!on)}
				style={{
					flex: "none",
					position: "relative",
					width: 40,
					height: 23,
					borderRadius: 999,
					cursor: busy ? "default" : "pointer",
					padding: 0,
					transition: "background .16s ease",
					background: on ? ACCENT : "#2c2c33",
					border: `1px solid ${on ? "transparent" : "#3a3a42"}`,
					opacity: busy ? 0.6 : 1,
				}}
			>
				<span
					style={{
						position: "absolute",
						top: 2,
						left: 2,
						width: 17,
						height: 17,
						borderRadius: "50%",
						background: "#fff",
						boxShadow: "0 1px 3px rgba(0,0,0,.4)",
						transition: "transform .16s ease",
						transform: `translateX(${on ? "17px" : "0"})`,
					}}
				/>
			</button>
		</div>
	);
}

function PRGroupView({
	group,
	threads,
	facts,
	sessionId,
	selectMode,
	selected,
	onToggleSelect,
	menuOpen,
	setMenuOpen,
	busy,
	onResolve,
	onReply,
	onSendQuick,
	onSendPrompt,
}: {
	group: Group;
	threads: Thread[];
	facts?: PRFacts;
	sessionId: string;
	selectMode: boolean;
	selected: Set<string>;
	onToggleSelect: (id: string) => void;
	menuOpen: string | null;
	setMenuOpen: (id: string | null) => void;
	busy: boolean;
	onResolve: (prUrl: string, threadId: string) => void;
	onReply: (prUrl: string, threadId: string, body: string, author: string) => void;
	onSendQuick: (prUrl: string, threadId: string, path: string) => void;
	onSendPrompt: (prUrl: string, threadId: string, message: string, resolveAfter: boolean) => void;
}) {
	const status = statusFor(facts?.review, facts?.mergeability);
	const sc = STATUS_COLORS[status.kind];
	return (
		<div style={{ marginBottom: 18 }}>
			<div
				style={{
					display: "flex",
					alignItems: "center",
					gap: 9,
					padding: "6px 4px 10px",
					position: "sticky",
					top: 0,
					background: P.rail,
					zIndex: 2,
				}}
			>
				<span
					style={{
						fontFamily: MONO,
						fontSize: 9,
						fontWeight: 600,
						color: "#c7c7cc",
						background: P.pillBg,
						border: `1px solid ${P.borderMenu}`,
						padding: "2px 5px",
						borderRadius: 4,
					}}
				>
					{providerBadge(group.provider)}
				</span>
				<span style={{ fontSize: 13.5, fontWeight: 700, color: P.textStrong }}>PR #{group.number}</span>
				<span style={{ fontSize: 12, color: P.secondary2, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
					{repoName(group.prUrl)}
				</span>
				<div style={{ flex: 1 }} />
				<span
					style={{
						display: "inline-flex",
						alignItems: "center",
						fontSize: 10.5,
						fontWeight: 600,
						color: sc.color,
						background: sc.bg,
						border: `1px solid ${sc.border}`,
						padding: "2px 8px",
						borderRadius: 999,
						whiteSpace: "nowrap",
					}}
				>
					{status.label}
				</span>
				<span style={pill(11, P.secondary, "1px 7px")}>{threads.length}</span>
			</div>

			{threads.map((thread) => (
				<ThreadCard
					key={thread.threadId}
					sessionId={sessionId}
					prUrl={group.prUrl}
					thread={thread}
					selectMode={selectMode}
					selected={selected.has(thread.threadId)}
					onToggleSelect={() => onToggleSelect(thread.threadId)}
					menuOpen={menuOpen === thread.threadId}
					onToggleMenu={() => setMenuOpen(menuOpen === thread.threadId ? null : thread.threadId)}
					busy={busy}
					onResolve={onResolve}
					onReply={onReply}
					onSendQuick={onSendQuick}
					onSendPrompt={onSendPrompt}
				/>
			))}
		</div>
	);
}

function ThreadCard({
	sessionId,
	prUrl,
	thread,
	selectMode,
	selected,
	onToggleSelect,
	menuOpen,
	onToggleMenu,
	busy,
	onResolve,
	onReply,
	onSendQuick,
	onSendPrompt,
}: {
	sessionId: string;
	prUrl: string;
	thread: Thread;
	selectMode: boolean;
	selected: boolean;
	onToggleSelect: () => void;
	menuOpen: boolean;
	onToggleMenu: () => void;
	busy: boolean;
	onResolve: (prUrl: string, threadId: string) => void;
	onReply: (prUrl: string, threadId: string, body: string, author: string) => void;
	onSendQuick: (prUrl: string, threadId: string, path: string) => void;
	onSendPrompt: (prUrl: string, threadId: string, message: string, resolveAfter: boolean) => void;
}) {
	const [collapsed, setCollapsed] = useState(false);
	const [replyOpen, setReplyOpen] = useState(false);
	const [replyText, setReplyText] = useState("");
	const [promptOpen, setPromptOpen] = useState(false);
	const first = thread.comments[0];
	const author = first?.author ?? "unknown";
	const [promptText, setPromptText] = useState(() => genPrompt(thread.path, thread.line, first?.body ?? ""));
	const [resolveAfter, setResolveAfter] = useState(true);

	const submitReply = () => {
		if (!replyText.trim()) return;
		onReply(prUrl, thread.threadId, replyText, author);
		setReplyText("");
		setReplyOpen(false);
	};

	return (
		<div
			style={{
				border: `1px solid ${P.borderCard}`,
				borderRadius: 10,
				overflow: "hidden",
				marginBottom: 10,
				background: P.cardBg,
			}}
		>
			<div
				onClick={() => setCollapsed((c) => !c)}
				style={{
					display: "flex",
					alignItems: "center",
					gap: 8,
					padding: "9px 12px",
					cursor: "pointer",
					background: P.fileHeader,
					borderBottom: collapsed ? "none" : `1px solid ${P.borderCard}`,
				}}
			>
				<span style={{ color: P.muted, fontSize: 10, width: 10, flex: "none" }}>{collapsed ? "▸" : "▾"}</span>
				<span
					title={thread.path}
					style={{
						flex: 1,
						minWidth: 0,
						fontFamily: MONO,
						fontSize: 11.5,
						color: "#b7b7bc",
						overflow: "hidden",
						textOverflow: "ellipsis",
						whiteSpace: "nowrap",
						direction: "rtl",
						textAlign: "left",
					}}
				>
					{thread.path}
				</span>
				<span style={{ fontFamily: MONO, fontSize: 11, color: ACCENT, flex: "none" }}>:{thread.line}</span>
			</div>

			{!collapsed && (
				<div style={{ position: "relative", padding: "13px 14px" }}>
					<div
						style={{
							position: "absolute",
							inset: 0,
							pointerEvents: "none",
							border: `1.5px solid ${selected ? ACCENT : "transparent"}`,
							background: selected ? accentMix(7) : "transparent",
						}}
					/>
					<div style={{ display: "flex", gap: 10, position: "relative" }}>
						{selectMode && (
							<button
								type="button"
								role="checkbox"
								aria-checked={selected}
								aria-label={`Select comment on ${thread.path}:${thread.line}`}
								onClick={onToggleSelect}
								style={{
									flex: "none",
									width: 18,
									height: 18,
									marginTop: 3,
									borderRadius: 5,
									border: `1.5px solid ${selected ? ACCENT : "#3a3a42"}`,
									background: selected ? ACCENT : "transparent",
									display: "flex",
									alignItems: "center",
									justifyContent: "center",
									cursor: "pointer",
									padding: 0,
									color: "#fff",
									fontSize: 11,
									fontWeight: 800,
									lineHeight: 1,
								}}
							>
								{selected ? "✓" : ""}
							</button>
						)}
						<div
							style={{
								flex: "none",
								width: 26,
								height: 26,
								borderRadius: "50%",
								display: "flex",
								alignItems: "center",
								justifyContent: "center",
								fontSize: 10,
								fontWeight: 700,
								color: "#fff",
								background: avatarBg(author),
							}}
						>
							{initialsFor(author)}
						</div>

						<div style={{ flex: 1, minWidth: 0 }}>
							{thread.comments.map((c, i) => (
								<div key={c.id} style={{ marginTop: i === 0 ? 0 : 10 }}>
									<div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 6 }}>
										<span style={{ fontSize: 13, fontWeight: 600, color: P.text }}>{c.author || "unknown"}</span>
										<span style={{ fontSize: 11, color: P.muted2 }}>{relativeTime(c.createdAt, Date.now())}</span>
									</div>
									<div style={{ fontSize: 13, lineHeight: 1.55, color: P.body, wordBreak: "break-word" }}>
										{splitBodyRuns(c.body).map((run, j) =>
											run.code ? (
												<code
													key={j}
													style={{
														fontFamily: MONO,
														fontSize: 11.5,
														color: P.code,
														background: P.pillBg,
														border: `1px solid ${P.borderPill}`,
														borderRadius: 4,
														padding: "0 4px",
													}}
												>
													{run.text}
												</code>
											) : (
												<span key={j}>{run.text}</span>
											),
										)}
									</div>
								</div>
							))}

							{thread.path && thread.line > 0 && (
								<InboxDiff sessionId={sessionId} prUrl={prUrl} path={thread.path} line={thread.line} />
							)}

							<div style={{ display: "flex", alignItems: "center", gap: 8, marginTop: 12, flexWrap: "wrap" }}>
								<button
									type="button"
									disabled={busy}
									onClick={() => onResolve(prUrl, thread.threadId)}
									style={outlineBtn(P.green, "rgba(95,184,122,.35)")}
								>
									✓ Resolve
								</button>
								<button
									type="button"
									disabled={busy}
									onClick={() => setReplyOpen((o) => !o)}
									style={solidBtn}
								>
									Reply
								</button>
								<SendSplit
									menuOpen={menuOpen}
									onToggleMenu={onToggleMenu}
									onQuick={() => onSendQuick(prUrl, thread.threadId, thread.path)}
									onEdit={() => {
										onToggleMenu();
										setPromptOpen((o) => !o);
									}}
								/>
							</div>

							{replyOpen && (
								<div style={{ marginTop: 11, border: `1px solid #26262c`, borderRadius: 9, padding: 9, background: P.replyBg }}>
									<textarea
										autoFocus
										value={replyText}
										onChange={(e) => setReplyText(e.target.value)}
										onKeyDown={(e) => {
											if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
												e.preventDefault();
												submitReply();
											}
										}}
										placeholder={`Reply to ${author}…`}
										aria-label={`Reply to thread at ${thread.path}:${thread.line}`}
										style={{
											width: "100%",
											minHeight: 64,
											resize: "vertical",
											background: "transparent",
											border: "none",
											outline: "none",
											color: P.text,
											fontSize: 12.5,
											lineHeight: 1.5,
											fontFamily: "inherit",
										}}
									/>
									<div style={{ display: "flex", justifyContent: "flex-end", gap: 8, marginTop: 6 }}>
										<button
											type="button"
											onClick={() => {
												setReplyOpen(false);
												setReplyText("");
											}}
											style={{ fontSize: 12, color: P.secondary, background: "transparent", border: "none", cursor: "pointer", padding: "6px 10px" }}
										>
											Cancel
										</button>
										<button type="button" disabled={busy || !replyText.trim()} onClick={submitReply} style={solidBtn}>
											Reply
										</button>
									</div>
								</div>
							)}

							{promptOpen && (
								<div
									style={{
										marginTop: 11,
										border: `1px solid ${accentMix(35, "#26262c")}`,
										borderRadius: 9,
										padding: 11,
										background: accentMix(6, P.replyBg),
									}}
								>
									<div style={{ display: "flex", alignItems: "center", gap: 7, marginBottom: 8 }}>
										<span style={{ fontSize: 11, fontWeight: 700, letterSpacing: ".04em", color: ACCENT }}>⚡ PROMPT TO WORKER</span>
									</div>
									<textarea
										value={promptText}
										onChange={(e) => setPromptText(e.target.value)}
										style={{
											width: "100%",
											minHeight: 104,
											resize: "vertical",
											background: P.promptTextareaBg,
											border: `1px solid ${P.borderBatch}`,
											borderRadius: 7,
											padding: 9,
											outline: "none",
											color: "#dcdce0",
											fontSize: 11.5,
											lineHeight: 1.6,
											fontFamily: MONO,
										}}
									/>
									<div style={{ display: "flex", alignItems: "center", gap: 8, marginTop: 8 }}>
										<label style={{ display: "inline-flex", alignItems: "center", gap: 6, fontSize: 11.5, color: P.secondary2, cursor: "pointer" }}>
											<input type="checkbox" checked={resolveAfter} onChange={(e) => setResolveAfter(e.target.checked)} style={{ accentColor: ACCENT }} />
											Resolve after send
										</label>
										<div style={{ flex: 1 }} />
										<button
											type="button"
											onClick={() => setPromptOpen(false)}
											style={{ fontSize: 12, color: P.secondary, background: "transparent", border: "none", cursor: "pointer", padding: "6px 10px" }}
										>
											Cancel
										</button>
										<button
											type="button"
											disabled={busy || !promptText.trim()}
											onClick={() => {
												onSendPrompt(prUrl, thread.threadId, promptText, resolveAfter);
												setPromptOpen(false);
											}}
											style={solidBtn}
										>
											Send to worker
										</button>
									</div>
								</div>
							)}
						</div>
					</div>
				</div>
			)}
		</div>
	);
}

function SendSplit({
	menuOpen,
	onToggleMenu,
	onQuick,
	onEdit,
}: {
	menuOpen: boolean;
	onToggleMenu: () => void;
	onQuick: () => void;
	onEdit: () => void;
}) {
	const seg: React.CSSProperties = {
		display: "inline-flex",
		alignItems: "center",
		gap: 6,
		fontSize: 12,
		fontWeight: 600,
		color: ACCENT,
		background: accentMix(12),
		border: `1px solid ${accentMix(40)}`,
		cursor: "pointer",
	};
	return (
		<div data-menu style={{ position: "relative", display: "inline-flex" }}>
			<button
				type="button"
				onClick={onQuick}
				style={{ ...seg, borderRight: "none", padding: "7px 12px", borderRadius: "7px 0 0 7px", whiteSpace: "nowrap" }}
			>
				⚡ Send to worker
			</button>
			<button
				type="button"
				aria-label="Send options"
				onClick={onToggleMenu}
				style={{ ...seg, justifyContent: "center", width: 30, borderRadius: "0 7px 7px 0", fontSize: 9 }}
			>
				▼
			</button>
			{menuOpen && (
				<div
					data-menu
					style={{
						position: "absolute",
						top: "calc(100% + 6px)",
						right: 0,
						width: 220,
						background: P.menuBg,
						border: `1px solid ${P.borderMenu}`,
						borderRadius: 10,
						padding: 5,
						zIndex: 20,
						boxShadow: "0 12px 30px rgba(0,0,0,.5)",
					}}
				>
					<MenuItem title="⚡ Quick send" desc="Auto-generated prompt from this comment" onClick={onQuick} />
					<MenuItem title="✎ Edit prompt…" desc="Review & tweak before sending" onClick={onEdit} />
				</div>
			)}
		</div>
	);
}

function MenuItem({ title, desc, onClick }: { title: string; desc: string; onClick: () => void }) {
	return (
		<div
			onClick={onClick}
			style={{ display: "flex", flexDirection: "column", gap: 2, padding: "8px 10px", borderRadius: 7, cursor: "pointer" }}
		>
			<span style={{ fontSize: 12.5, fontWeight: 600, color: P.text }}>{title}</span>
			<span style={{ fontSize: 11, color: "#7c7c82" }}>{desc}</span>
		</div>
	);
}

type DiffCtx = components["schemas"]["DiffContextResponse"];

function InboxDiff({ sessionId, prUrl, path, line }: { sessionId: string; prUrl: string; path: string; line: number }) {
	const [open, setOpen] = useState(false);
	const q = useQuery({
		queryKey: ["diff-context", sessionId, prUrl, path, line, "hunk"],
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/diff-context", {
				params: { path: { sessionId }, query: { prUrl, path, line, mode: "hunk" } },
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to load code"));
			return data as DiffCtx;
		},
	});
	const ctx = q.data;
	if (!ctx || !ctx.available || ctx.lines.length === 0) return null;
	const n = ctx.lines.length;
	return (
		<div style={{ marginTop: 10 }}>
			<button
				type="button"
				onClick={() => setOpen((o) => !o)}
				style={{ fontSize: 11.5, color: P.secondary2, background: "transparent", border: "none", cursor: "pointer", padding: 0, display: "inline-flex", alignItems: "center", gap: 5 }}
			>
				{open ? "▾ Hide" : "▸ Show"} diff · {n} lines
			</button>
			{open && (
				<div style={{ marginTop: 8, border: `1px solid ${P.borderCard}`, borderRadius: 8, overflow: "hidden", fontFamily: MONO, fontSize: 11, lineHeight: 1.7 }}>
					{ctx.lines.map((l, i) => {
						const add = l.kind === "add";
						const del = l.kind === "del";
						const tc = add ? P.diffAddText : del ? P.diffDelText : P.diffContextText;
						return (
							<div key={i} style={{ display: "flex", background: add ? P.diffAddBg : del ? P.diffDelBg : "transparent", padding: "1px 0" }}>
								<span style={{ flex: "none", width: 34, textAlign: "right", paddingRight: 8, color: P.muted3, userSelect: "none" }}>
									{l.newLine || l.oldLine || ""}
								</span>
								<span style={{ flex: "none", width: 14, textAlign: "center", color: tc }}>{add ? "+" : del ? "-" : " "}</span>
								<span style={{ flex: 1, whiteSpace: "pre", color: tc, paddingRight: 10 }}>{l.text}</span>
							</div>
						);
					})}
				</div>
			)}
		</div>
	);
}

function BatchBar({
	count,
	menuOpen,
	setMenuOpen,
	onResolve,
	onOneTask,
	onSeparate,
}: {
	count: number;
	menuOpen: boolean;
	setMenuOpen: (o: boolean) => void;
	onResolve: () => void;
	onOneTask: () => void;
	onSeparate: () => void;
}) {
	return (
		<div
			style={{
				flex: "none",
				padding: "11px 14px",
				borderTop: `1px solid ${P.borderBatch}`,
				background: P.batchBg,
				display: "flex",
				alignItems: "center",
				gap: 10,
			}}
		>
			<span style={{ fontSize: 12.5, fontWeight: 600, color: P.textStrong }}>{count} selected</span>
			<div style={{ flex: 1 }} />
			<button type="button" onClick={onResolve} style={outlineBtn(P.green, "rgba(95,184,122,.35)", "6px 11px")}>
				✓ Resolve
			</button>
			<div data-menu style={{ position: "relative", display: "inline-flex" }}>
				<button
					type="button"
					onClick={onOneTask}
					style={{ ...solidBtn, borderRadius: "7px 0 0 7px", padding: "7px 12px" }}
				>
					⚡ Send batch
				</button>
				<button
					type="button"
					aria-label="Batch send options"
					onClick={() => setMenuOpen(!menuOpen)}
					style={{ ...solidBtn, width: 28, padding: 0, borderRadius: "0 7px 7px 0", borderLeft: "1px solid rgba(255,255,255,.25)", fontSize: 9 }}
				>
					▼
				</button>
				{menuOpen && (
					<div
						data-menu
						style={{
							position: "absolute",
							bottom: "calc(100% + 6px)",
							right: 0,
							width: 236,
							background: P.menuBg,
							border: `1px solid ${P.borderMenu}`,
							borderRadius: 10,
							padding: 5,
							zIndex: 20,
							boxShadow: "0 12px 30px rgba(0,0,0,.5)",
						}}
					>
						<MenuItem title="One task, all comments" desc="Bundle selected into a single worker" onClick={onOneTask} />
						<MenuItem title="Separate task each" desc="Fan out one worker per comment" onClick={onSeparate} />
					</div>
				)}
			</div>
		</div>
	);
}

function ResolvedSection({ items }: { items: { group: Group; thread: Thread }[] }) {
	const [open, setOpen] = useState(false);
	return (
		<div style={{ marginTop: 8, border: `1px solid ${P.borderCard}`, borderRadius: 10, overflow: "hidden", background: P.resolvedBg }}>
			<div onClick={() => setOpen((o) => !o)} style={{ display: "flex", alignItems: "center", gap: 9, padding: "10px 12px", cursor: "pointer" }}>
				<span style={{ color: P.muted, fontSize: 10, width: 10 }}>{open ? "▾" : "▸"}</span>
				<span style={{ color: P.green, fontSize: 12 }}>✓</span>
				<span style={{ fontSize: 12.5, fontWeight: 600, color: P.secondary }}>Resolved</span>
				<span style={pill(11, P.muted, "1px 7px")}>{items.length}</span>
			</div>
			{open && (
				<div style={{ borderTop: `1px solid ${P.borderRail}` }}>
					{items.map(({ group, thread }) => {
						const c = thread.comments[0];
						return (
							<div key={thread.threadId} style={{ display: "flex", gap: 9, padding: "10px 12px", borderBottom: `1px solid ${P.divider}` }}>
								<div
									style={{
										flex: "none",
										width: 24,
										height: 24,
										borderRadius: "50%",
										display: "flex",
										alignItems: "center",
										justifyContent: "center",
										fontSize: 9.5,
										fontWeight: 700,
										color: "#fff",
										background: avatarBg(c?.author ?? "?", 0.42, 0.06),
									}}
								>
									{initialsFor(c?.author ?? "?")}
								</div>
								<div style={{ flex: 1, minWidth: 0 }}>
									<div style={{ display: "flex", alignItems: "center", gap: 7 }}>
										<span style={{ fontSize: 12, fontWeight: 600, color: P.secondary2 }}>{c?.author ?? "unknown"}</span>
										<span style={{ fontFamily: MONO, fontSize: 10.5, color: P.muted2, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
											PR #{group.number} · {baseName(thread.path)}:{thread.line}
										</span>
									</div>
									<div
										style={{
											fontSize: 12,
											lineHeight: 1.5,
											color: P.muted,
											marginTop: 3,
											display: "-webkit-box",
											WebkitLineClamp: 2,
											WebkitBoxOrient: "vertical",
											overflow: "hidden",
										}}
									>
										{(c?.body ?? "").replace(/`/g, "")}
									</div>
								</div>
							</div>
						);
					})}
				</div>
			)}
		</div>
	);
}

function EmptyState() {
	return (
		<div style={{ display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", padding: "80px 20px", textAlign: "center", color: P.muted2 }}>
			<div style={{ fontSize: 34, marginBottom: 14, opacity: 0.6 }}>✓</div>
			<div style={{ fontSize: 14, fontWeight: 600, color: P.secondary, marginBottom: 4 }}>Inbox zero</div>
			<div style={{ fontSize: 12.5 }}>No unresolved comments.</div>
		</div>
	);
}

function Toast({ text }: { text: string }) {
	return (
		<div
			role="status"
			style={{
				position: "absolute",
				bottom: 22,
				left: "50%",
				transform: "translateX(-50%)",
				background: P.toastBg,
				border: `1px solid ${P.borderToast}`,
				color: P.text,
				fontSize: 12.5,
				fontWeight: 500,
				padding: "10px 16px",
				borderRadius: 9,
				boxShadow: "0 10px 34px rgba(0,0,0,.55)",
				display: "flex",
				alignItems: "center",
				gap: 9,
				zIndex: 40,
				whiteSpace: "nowrap",
			}}
		>
			<span style={{ color: ACCENT }}>⚡</span>
			{text}
		</div>
	);
}

// --- shared style helpers ---------------------------------------------------

function pill(fontSize: number, color: string, padding = "2px 8px"): React.CSSProperties {
	return {
		fontSize,
		fontWeight: 600,
		color,
		background: P.pillBg,
		border: `1px solid ${P.borderPill}`,
		borderRadius: 999,
		padding,
	};
}

function outlineBtn(color: string, border: string, padding = "6px 12px"): React.CSSProperties {
	return {
		display: "inline-flex",
		alignItems: "center",
		gap: 5,
		fontSize: 12,
		fontWeight: 600,
		color,
		background: "transparent",
		border: `1px solid ${border}`,
		padding,
		borderRadius: 7,
		cursor: "pointer",
	};
}

const solidBtn: React.CSSProperties = {
	display: "inline-flex",
	alignItems: "center",
	gap: 5,
	fontSize: 12,
	fontWeight: 600,
	color: "#fff",
	background: ACCENT,
	border: "none",
	padding: "7px 13px",
	borderRadius: 7,
	cursor: "pointer",
};

function repoName(prUrl: string): string {
	// GitHub: .../{owner}/{repo}/pull/N ; GitLab: .../{group}/{repo}/-/merge_requests/N
	try {
		const u = new URL(prUrl);
		const parts = u.pathname.split("/").filter(Boolean);
		const cut = parts.findIndex((p) => p === "pull" || p === "merge_requests" || p === "-");
		const repoParts = cut > 0 ? parts.slice(0, cut) : parts;
		return repoParts[repoParts.length - 1] ?? "";
	} catch {
		return "";
	}
}
