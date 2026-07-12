import { useState } from "react";
import type { LucideIcon } from "lucide-react";
import { Button } from "../ui/button";
import { SettingEditorDrawer } from "./SettingEditorDrawer";

// A Default/Customized status chip so a user sees at a glance which editors they
// have overridden (proposal §6). "Customized" (accent) when the draft differs
// from the built-in default; "Default" (passive) otherwise — computed from the
// live draft so it updates the moment you edit or reset.
function StatusChip({ customized }: { customized: boolean }) {
	return customized ? (
		<span className="rounded-full bg-accent-weak px-2.5 py-0.5 font-mono text-[10px] uppercase tracking-[0.04em] text-accent">
			Customized
		</span>
	) : (
		<span className="rounded-full border border-border-strong px-2.5 py-0.5 font-mono text-[10px] uppercase tracking-[0.04em] text-passive">
			Default
		</span>
	);
}

// SettingEditorRow is the collapsed row a large editor shows as: icon · name ·
// one-line purpose · Default/Customized chip · Edit. Edit opens the slide-over
// drawer. The row is a controlled view over the parent's draft: `value` is the
// draft and `defaultValue` the built-in, so `customized` = draft ≠ default. The
// parent passes `modified` (draft differs from the last-saved value) so the
// "unsaved" dot reflects dirty-since-save, not merely customized-vs-default.
export function SettingEditorRow({
	icon: Icon,
	name,
	purpose,
	description,
	textareaLabel,
	value,
	defaultValue,
	modified = false,
	onChange,
	placeholders,
}: {
	icon?: LucideIcon;
	name: string;
	purpose: string;
	description?: string;
	textareaLabel: string;
	value: string;
	defaultValue: string;
	modified?: boolean;
	onChange: (value: string) => void;
	placeholders?: string[];
}) {
	const [open, setOpen] = useState(false);
	const customized = value !== defaultValue;
	// Reset restores the built-in default; disabled only when already at default.
	const resetDisabled = value === defaultValue;
	return (
		<div className="mb-2.5 overflow-hidden rounded-xl border border-border bg-card">
			<div className="flex items-center gap-3 p-3.5">
				{Icon && <Icon className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />}
				<div className="flex min-w-0 flex-1 flex-col gap-0.5">
					<span className="text-[13px] font-semibold text-foreground">{name}</span>
					<span className="truncate text-[11.5px] text-passive">{purpose}</span>
				</div>
				{modified && (
					<span
						aria-label="modified"
						className="h-1.5 w-1.5 shrink-0 rounded-full bg-warning shadow-[0_0_6px_rgba(232,193,74,0.5)]"
					/>
				)}
				<StatusChip customized={customized} />
				<Button type="button" variant="outline" size="sm" aria-label={`Edit ${name}`} onClick={() => setOpen(true)}>
					Edit
				</Button>
			</div>
			<SettingEditorDrawer
				open={open}
				onOpenChange={setOpen}
				title={name}
				description={description}
				textareaLabel={textareaLabel}
				value={value}
				onChange={onChange}
				placeholders={placeholders}
				onReset={() => onChange(defaultValue)}
				resetDisabled={resetDisabled}
			/>
		</div>
	);
}
