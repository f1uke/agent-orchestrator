import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { DropdownMenu } from "radix-ui";
import type { components } from "../../api/schema";
import { useSessionPRComments, type PRCommentGroup } from "../hooks/useSessionPRComments";
import { useInboxActions } from "../hooks/useInboxActions";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import {
	ACCENT,
	MONO,
	PALETTE as P,
	accentMix,
	avatarBg,
	baseName,
	genPrompt,
	initialsFor,
	originOf,
	providerBadge,
	relativeTime,
	splitBodyRuns,
	splitNoteRuns,
	STATUS_COLORS,
	statusFor,
} from "../lib/comment-inbox";
import { DiffRows } from "./DiffRows";
import { CommentActions, MenuItemBody, Toast, menuBox, menuItemStyle, outlineBtn, pill, solidBtn } from "./inbox-ui";

type Group = PRCommentGroup;
type Thread = Group["threads"][number];
type PRFacts = NonNullable<components["schemas"]["ControllersSessionView"]["prs"]>[number];

/**
 * Everything the full-file diff viewer (center pane) needs to render a comment's
 * file at its line and act on the thread. Emitted by the inbox's "Expand full
 * file" affordance and consumed by SessionView → FileDiffView.
 */
export type FileDiffTarget = {
	prUrl: string;
	htmlUrl: string;
	prNumber: number;
	provider: string;
	thread: Thread;
};

/**
 * Comments tab — the "Unresolved inbox": a cross-PR list of unresolved review
 * comments (grouped PR → file → comment) with per-comment Resolve / Reply /
 * Send-to-worker actions, a multi-select batch mode, and toasts. Pixel-matched
 * to the Comments Inbox design (exact dark palette + #3b82f6 accent). Wired to
 * the existing comment-resolve / comment-reply / comment-dispatch / send
 * endpoints; the auto-send strip drives the per-session auto-nudge override.
 */
export function CommentsView({
	sessionId,
	onOpenFile,
}: {
	sessionId: string;
	/** Open a comment's file as a full-file diff in the center pane. */
	onOpenFile?: (target: FileDiffTarget) => void;
}) {
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

	// --- mutations + toast + per-comment handlers (shared with FileDiffView) --
	const { resolve, dispatch, send, busy, toast, showToast, doResolve, doReply, doSendQuick, doSendPrompt } =
		useInboxActions(sessionId);

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

	// --- batch handlers (operate on the current multi-select) ----------------
	const batchResolve = () => {
		selectedIds.forEach((id) => {
			const it = byId.get(id);
			if (it) resolve.mutate({ prUrl: it.prUrl, threadId: it.thread.threadId });
		});
		showToast(`${selectedIds.length} comment${selectedIds.length > 1 ? "s" : ""} resolved`);
		setSelected(new Set());
	};
	const batchSeparate = () => {
		selectedIds.forEach((id) => {
			const it = byId.get(id);
			if (it) dispatch.mutate({ prUrl: it.prUrl, threadId: it.thread.threadId });
		});
		showToast(`Fanned out ${selectedIds.length} worker tasks`);
	};
	const batchOneTask = () => {
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
							busy={busy}
							onResolve={doResolve}
							onReply={doReply}
							onSendQuick={doSendQuick}
							onSendPrompt={doSendPrompt}
							onOpenFile={onOpenFile}
						/>
					))}

				{!loading && !err && totalUnresolved === 0 && <EmptyState />}

				{!loading && !err && resolvedItems.length > 0 && <ResolvedSection items={resolvedItems} />}
			</div>

			{selectMode && selectedIds.length > 0 && (
				<BatchBar
					count={selectedIds.length}
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
	busy,
	onResolve,
	onReply,
	onSendQuick,
	onSendPrompt,
	onOpenFile,
}: {
	group: Group;
	threads: Thread[];
	facts?: PRFacts;
	sessionId: string;
	selectMode: boolean;
	selected: Set<string>;
	onToggleSelect: (id: string) => void;
	busy: boolean;
	onResolve: (prUrl: string, threadId: string) => void;
	onReply: (prUrl: string, threadId: string, body: string, author: string) => void;
	onSendQuick: (prUrl: string, threadId: string, path: string) => void;
	onSendPrompt: (prUrl: string, threadId: string, message: string, resolveAfter: boolean) => void;
	onOpenFile?: (target: FileDiffTarget) => void;
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
					busy={busy}
					onResolve={onResolve}
					onReply={onReply}
					onSendQuick={onSendQuick}
					onSendPrompt={onSendPrompt}
					onOpenFile={
						onOpenFile &&
						(() =>
							onOpenFile({
								prUrl: group.prUrl,
								htmlUrl: group.htmlUrl,
								prNumber: group.number,
								provider: group.provider,
								thread,
							}))
					}
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
	busy,
	onResolve,
	onReply,
	onSendQuick,
	onSendPrompt,
	onOpenFile,
}: {
	sessionId: string;
	prUrl: string;
	thread: Thread;
	selectMode: boolean;
	selected: boolean;
	onToggleSelect: () => void;
	busy: boolean;
	onResolve: (prUrl: string, threadId: string) => void;
	onReply: (prUrl: string, threadId: string, body: string, author: string) => void;
	onSendQuick: (prUrl: string, threadId: string, path: string) => void;
	onSendPrompt: (prUrl: string, threadId: string, message: string, resolveAfter: boolean) => void;
	onOpenFile?: () => void;
}) {
	const [collapsed, setCollapsed] = useState(false);
	const first = thread.comments[0];
	const author = first?.author ?? "unknown";

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
							{thread.comments.map((c, i) =>
								c.system ? (
									<SystemNoteLine key={c.id} body={c.body} prUrl={prUrl} first={i === 0} />
								) : (
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
								),
							)}

							{thread.path && thread.line > 0 && (
								<InboxDiff
									sessionId={sessionId}
									prUrl={prUrl}
									path={thread.path}
									line={thread.line}
									onOpenFile={onOpenFile}
								/>
							)}

							<CommentActions
								prUrl={prUrl}
								threadId={thread.threadId}
								path={thread.path}
								line={thread.line}
								author={author}
								seedBody={first?.body ?? ""}
								busy={busy}
								onResolve={onResolve}
								onReply={onReply}
								onSendQuick={onSendQuick}
								onSendPrompt={onSendPrompt}
							/>
						</div>
					</div>
				</div>
			)}
		</div>
	);
}

/**
 * A GitLab (or other provider) system note — e.g. "changed this line in version 6
 * of the diff" that GitLab appends when a thread goes outdated. Rendered as a
 * small, de-emphasized activity line (no avatar/author block, unlike a real
 * comment), with its embedded markdown link shown as a clean hyperlink rather
 * than the raw URL. Purely presentational: it never affects thread counts.
 */
function SystemNoteLine({ body, prUrl, first }: { body: string; prUrl: string; first: boolean }) {
	const runs = splitNoteRuns(body, originOf(prUrl));
	return (
		<div
			style={{
				marginTop: first ? 0 : 8,
				fontSize: 11.5,
				lineHeight: 1.5,
				color: P.muted,
				wordBreak: "break-word",
			}}
		>
			{runs.map((run, j) =>
				run.href ? (
					<a
						key={j}
						href={run.href}
						target="_blank"
						rel="noopener noreferrer"
						style={{ color: P.secondary, textDecoration: "underline", textUnderlineOffset: 2 }}
					>
						{run.text}
					</a>
				) : (
					<span key={j}>{run.text}</span>
				),
			)}
		</div>
	);
}

type DiffCtx = components["schemas"]["DiffContextResponse"];

// Inline diff for one comment: a collapsible, syntax-highlighted hunk plus an
// "Expand full file" affordance that opens the whole file in the center pane.
function InboxDiff({
	sessionId,
	prUrl,
	path,
	line,
	onOpenFile,
}: {
	sessionId: string;
	prUrl: string;
	path: string;
	line: number;
	onOpenFile?: () => void;
}) {
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
			<div style={{ display: "flex", alignItems: "center", gap: 16 }}>
				<button
					type="button"
					onClick={() => setOpen((o) => !o)}
					style={{ fontSize: 11.5, color: P.secondary2, background: "transparent", border: "none", cursor: "pointer", padding: 0, display: "inline-flex", alignItems: "center", gap: 5 }}
				>
					{open ? "▾ Hide" : "▸ Show"} diff · {n} lines
				</button>
				{onOpenFile && (
					<button
						type="button"
						onClick={onOpenFile}
						style={{ fontSize: 11.5, fontWeight: 600, color: ACCENT, background: "transparent", border: "none", cursor: "pointer", padding: 0, display: "inline-flex", alignItems: "center", gap: 5 }}
					>
						Expand full file ↗
					</button>
				)}
			</div>
			{open && (
				<div style={{ marginTop: 8, border: `1px solid ${P.borderCard}`, borderRadius: 8, overflow: "hidden" }}>
					<DiffRows lines={ctx.lines} size="narrow" />
				</div>
			)}
		</div>
	);
}

function BatchBar({
	count,
	onResolve,
	onOneTask,
	onSeparate,
}: {
	count: number;
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
			<div style={{ display: "inline-flex" }}>
				<button
					type="button"
					onClick={onOneTask}
					style={{ ...solidBtn, borderRadius: "7px 0 0 7px", padding: "7px 12px" }}
				>
					⚡ Send batch
				</button>
				<DropdownMenu.Root modal={false}>
					<DropdownMenu.Trigger asChild>
						<button
							type="button"
							aria-label="Batch send options"
							style={{ ...solidBtn, width: 28, padding: 0, borderRadius: "0 7px 7px 0", borderLeft: "1px solid rgba(255,255,255,.25)", fontSize: 9 }}
						>
							▼
						</button>
					</DropdownMenu.Trigger>
					<DropdownMenu.Portal>
						<DropdownMenu.Content side="top" align="end" sideOffset={6} style={menuBox(236)}>
							<DropdownMenu.Item onSelect={onOneTask} style={menuItemStyle}>
								<MenuItemBody title="One task, all comments" desc="Bundle selected into a single worker" />
							</DropdownMenu.Item>
							<DropdownMenu.Item onSelect={onSeparate} style={menuItemStyle}>
								<MenuItemBody title="Separate task each" desc="Fan out one worker per comment" />
							</DropdownMenu.Item>
						</DropdownMenu.Content>
					</DropdownMenu.Portal>
				</DropdownMenu.Root>
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
