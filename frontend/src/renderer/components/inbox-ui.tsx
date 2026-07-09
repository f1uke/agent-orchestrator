import type { CSSProperties } from "react";
import { DropdownMenu } from "radix-ui";
import { ACCENT, accentMix, PALETTE as P } from "../lib/comment-inbox";

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

export function MenuItemBody({ title, desc }: { title: string; desc: string }) {
	return (
		<>
			<span style={{ fontSize: 12.5, fontWeight: 600, color: P.text }}>{title}</span>
			<span style={{ fontSize: 11, color: "#7c7c82" }}>{desc}</span>
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
