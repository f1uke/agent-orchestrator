import { useState, type CSSProperties } from "react";
import { DropdownMenu } from "radix-ui";
import { ACCENT, MONO, PALETTE as P, accentMix, genPrompt } from "../lib/comment-inbox";

// Shared leaf UI for the Comments inbox and the full-file diff viewer's anchored
// comment. Kept here (not in CommentsView) so both surfaces render identical
// buttons/menus without a circular import.

export function pill(fontSize: number, color: string, padding = "2px 8px"): CSSProperties {
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

export function outlineBtn(color: string, border: string, padding = "6px 12px"): CSSProperties {
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

export const solidBtn: CSSProperties = {
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

const splitSeg: CSSProperties = {
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

// Menu content lives in a portal (Radix DropdownMenu) so it is not clipped by
// the comment card's `overflow: hidden`; non-modal so it never locks the page.
export function menuBox(width: number): CSSProperties {
	return {
		width,
		background: P.menuBg,
		border: `1px solid ${P.borderMenu}`,
		borderRadius: 10,
		padding: 5,
		zIndex: 50,
		boxShadow: "0 12px 30px rgba(0,0,0,.5)",
	};
}

export const menuItemStyle: CSSProperties = {
	display: "flex",
	flexDirection: "column",
	gap: 2,
	padding: "8px 10px",
	borderRadius: 7,
	cursor: "pointer",
	outline: "none",
};

/** Transient bottom-center confirmation toast (absolute-positioned in its pane). */
export function Toast({ text }: { text: string }) {
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

export function MenuItemBody({ title, desc }: { title: string; desc: string }) {
	return (
		<>
			<span style={{ fontSize: 12.5, fontWeight: 600, color: P.text }}>{title}</span>
			<span style={{ fontSize: 11, color: "#7c7c82" }}>{desc}</span>
		</>
	);
}

/**
 * The Resolve / Reply / Send-to-worker action row for a single review comment,
 * with its inline reply composer and editable "prompt to worker" drawer. Shared
 * by the inbox thread card and the full-file viewer's anchored comment so both
 * offer the exact same actions. Owns only its own transient open/draft state;
 * the mutations flow in via callbacks (see useInboxActions).
 */
export function CommentActions({
	prUrl,
	threadId,
	path,
	line,
	author,
	seedBody,
	busy,
	onResolve,
	onReply,
	onSendQuick,
	onSendPrompt,
}: {
	prUrl: string;
	threadId: string;
	path: string;
	line: number;
	author: string;
	seedBody: string;
	busy: boolean;
	onResolve: (prUrl: string, threadId: string) => void;
	onReply: (prUrl: string, threadId: string, body: string, author: string) => void;
	onSendQuick: (prUrl: string, threadId: string, path: string) => void;
	onSendPrompt: (prUrl: string, threadId: string, message: string, resolveAfter: boolean) => void;
}) {
	const [replyOpen, setReplyOpen] = useState(false);
	const [replyText, setReplyText] = useState("");
	const [promptOpen, setPromptOpen] = useState(false);
	const [promptText, setPromptText] = useState(() => genPrompt(path, line, seedBody));
	const [resolveAfter, setResolveAfter] = useState(true);

	const submitReply = () => {
		if (!replyText.trim()) return;
		onReply(prUrl, threadId, replyText, author);
		setReplyText("");
		setReplyOpen(false);
	};

	return (
		<>
			<div style={{ display: "flex", alignItems: "center", gap: 8, marginTop: 12, flexWrap: "wrap" }}>
				<button
					type="button"
					disabled={busy}
					onClick={() => onResolve(prUrl, threadId)}
					style={outlineBtn(P.green, "rgba(95,184,122,.35)")}
				>
					✓ Resolve
				</button>
				<button type="button" disabled={busy} onClick={() => setReplyOpen((o) => !o)} style={solidBtn}>
					Reply
				</button>
				<SendSplit onQuick={() => onSendQuick(prUrl, threadId, path)} onEdit={() => setPromptOpen((o) => !o)} />
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
						aria-label={`Reply to thread at ${path}:${line}`}
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
								onSendPrompt(prUrl, threadId, promptText, resolveAfter);
								setPromptOpen(false);
							}}
							style={solidBtn}
						>
							Send to worker
						</button>
					</div>
				</div>
			)}
		</>
	);
}

/**
 * "Send to worker" split button: a quick-send main segment plus a portaled
 * caret menu offering Quick send / Edit prompt.
 */
export function SendSplit({ onQuick, onEdit }: { onQuick: () => void; onEdit: () => void }) {
	return (
		<div style={{ display: "inline-flex" }}>
			<button
				type="button"
				onClick={onQuick}
				style={{ ...splitSeg, borderRight: "none", padding: "7px 12px", borderRadius: "7px 0 0 7px", whiteSpace: "nowrap" }}
			>
				⚡ Send to worker
			</button>
			<DropdownMenu.Root modal={false}>
				<DropdownMenu.Trigger asChild>
					<button
						type="button"
						aria-label="Send options"
						style={{ ...splitSeg, justifyContent: "center", width: 30, borderRadius: "0 7px 7px 0", fontSize: 9 }}
					>
						▼
					</button>
				</DropdownMenu.Trigger>
				<DropdownMenu.Portal>
					<DropdownMenu.Content align="end" sideOffset={6} style={menuBox(220)}>
						<DropdownMenu.Item onSelect={onQuick} style={menuItemStyle}>
							<MenuItemBody title="⚡ Quick send" desc="Auto-generated prompt from this comment" />
						</DropdownMenu.Item>
						<DropdownMenu.Item onSelect={onEdit} style={menuItemStyle}>
							<MenuItemBody title="✎ Edit prompt…" desc="Review & tweak before sending" />
						</DropdownMenu.Item>
					</DropdownMenu.Content>
				</DropdownMenu.Portal>
			</DropdownMenu.Root>
		</div>
	);
}
