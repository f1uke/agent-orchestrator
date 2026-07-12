import type { ReactNode } from "react";
import { Label } from "../ui/label";

// A small amber "Modified" tag shown next to a field label when the field's
// draft differs from the last-saved value — the per-field echo of the save bar.
export function ModifiedTag() {
	return (
		<span className="font-mono text-[10px] tracking-[0.04em] text-warning" aria-label="modified">
			● Modified
		</span>
	);
}

// SettingsField frames one editable control: a label (which may carry the
// Modified tag or a "· required" suffix), the control, and optional help text.
// It mirrors the old `Field` helper so control markup/labels stay identical.
export function SettingsField({
	label,
	htmlFor,
	modified = false,
	help,
	children,
}: {
	label?: ReactNode;
	htmlFor?: string;
	modified?: boolean;
	help?: ReactNode;
	children: ReactNode;
}) {
	return (
		<div className="flex flex-col gap-1.5">
			{label && (
				// The Modified tag is a sibling of the Label (not a child) so it never
				// pollutes the label's accessible name / label-text association.
				<div className="flex items-center gap-2">
					<Label htmlFor={htmlFor} className="text-[12px] text-muted-foreground">
						{label}
					</Label>
					{modified && <ModifiedTag />}
				</div>
			)}
			{children}
			{help && <p className="text-[11px] text-passive">{help}</p>}
		</div>
	);
}
