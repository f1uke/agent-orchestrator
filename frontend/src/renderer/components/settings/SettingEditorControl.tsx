import { useState } from "react";
import { Button } from "../ui/button";
import { SettingEditorDrawer } from "./SettingEditorDrawer";

// The expanded control for a setting whose value is a large block of text (the
// four global prompt bases, the message templates, the per-project prompt
// additions). It deliberately does NOT inline a textarea: a prompt base runs to
// thousands of characters and belongs in the slide-over, which already exists and
// already stages into the parent draft rather than self-saving.
export function SettingEditorControl({
	name,
	description,
	textareaLabel,
	value,
	defaultValue,
	onChange,
	placeholders,
}: {
	name: string;
	description?: string;
	textareaLabel: string;
	value: string;
	defaultValue: string;
	onChange: (value: string) => void;
	placeholders?: string[];
}) {
	const [open, setOpen] = useState(false);
	const atDefault = value === defaultValue;
	return (
		<div className="flex flex-col gap-2.5">
			{placeholders && placeholders.length > 0 && (
				<p className="text-[11px] text-passive">
					Placeholders: <code className="font-mono text-muted-foreground">{placeholders.join(" ")}</code>
				</p>
			)}
			<div className="flex items-center gap-2.5">
				<Button type="button" variant="outline" size="sm" aria-label={`Edit ${name}`} onClick={() => setOpen(true)}>
					Edit
				</Button>
				<Button
					type="button"
					variant="ghost"
					size="sm"
					aria-label={`Reset ${name} to default`}
					disabled={atDefault}
					onClick={() => onChange(defaultValue)}
				>
					Reset to default
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
				resetDisabled={atDefault}
			/>
		</div>
	);
}
