import type { ReactNode } from "react";
import { cn } from "../../lib/utils";

// SettingsGroup is the editable "group" card inside a section (mockups' .group):
// a raised card surface with an optional title and vertically stacked fields.
// Built on the same tokens as components/ui/card so it reads as native chrome.
export function SettingsGroup({
	title,
	children,
	className,
}: {
	title?: ReactNode;
	children: ReactNode;
	className?: string;
}) {
	return (
		<section className={cn("mb-4 rounded-xl border border-border bg-card p-4", className)}>
			{title && <h3 className="mb-3.5 text-[13px] font-semibold text-foreground">{title}</h3>}
			<div className="flex flex-col gap-3.5">{children}</div>
		</section>
	);
}

// ActionRow holds true, instant actions (Refresh agents, Send test, Check for
// updates, Run migration) — visually separated from edited-then-saved fields so
// it is clear they run immediately and never route through the save bar
// (locked decision 3).
export function ActionRow({ children, className }: { children: ReactNode; className?: string }) {
	return <div className={cn("flex items-center gap-3", className)}>{children}</div>;
}
