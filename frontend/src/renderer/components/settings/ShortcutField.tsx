import { useState, type KeyboardEvent } from "react";
import {
	shortcutFromEvent,
	shortcutKeys,
	shortcutLabel,
	shortcutProblem,
	shortcutsTakenElsewhere,
} from "../../../shared/editor-shortcuts";
import { cn } from "../../lib/utils";
import { isMacPlatform } from "../../lib/platform";
import { Button } from "../ui/button";

// A key-combination field: click it (or press Return on it), then press the
// shortcut. Escape cancels, Delete unbinds. A combination that would fire while
// typing is refused on the spot; one that something else in the app also
// answers to is accepted, with a line saying which of the two wins.

export function ShortcutField({
	id,
	value,
	defaultValue,
	other,
	onChange,
}: {
	id: string;
	value: string;
	defaultValue: string;
	/** The editor's other shortcut, which this one must not duplicate. */
	other: { shortcut: string; what: string };
	onChange: (next: string) => void;
}) {
	const mac = isMacPlatform();
	const [recording, setRecording] = useState(false);
	const [refusal, setRefusal] = useState<string | null>(null);

	const onKeyDown = (event: KeyboardEvent<HTMLButtonElement>) => {
		if (!recording) return;
		// Everything pressed while recording is the shortcut, not an action -
		// Tab included, so focus cannot wander off mid-chord.
		event.preventDefault();
		event.stopPropagation();
		const bare = !event.ctrlKey && !event.altKey && !event.metaKey && !event.shiftKey;
		if (event.key === "Escape" && bare) {
			setRecording(false);
			setRefusal(null);
			return;
		}
		if ((event.key === "Backspace" || event.key === "Delete") && bare) {
			onChange("");
			setRecording(false);
			setRefusal(null);
			return;
		}
		const next = shortcutFromEvent(event.nativeEvent);
		if (next === null) return; // only modifiers so far
		const problem = shortcutProblem(next);
		if (problem) {
			setRefusal(problem);
			return;
		}
		if (next === other.shortcut) {
			setRefusal(`${shortcutLabel(next, mac)} is already ${other.what}.`);
			return;
		}
		onChange(next);
		setRecording(false);
		setRefusal(null);
	};

	const clash = value === "" ? undefined : shortcutsTakenElsewhere(mac).find((t) => t.shortcut === value);
	const keys = shortcutKeys(value, mac);

	return (
		<div className="flex max-w-[340px] flex-col gap-2">
			<div className="flex items-center gap-2">
				<button
					id={id}
					type="button"
					data-testid={`${id}-field`}
					aria-describedby={`${id}-hint`}
					onClick={() => {
						setRecording((r) => !r);
						setRefusal(null);
					}}
					onKeyDown={onKeyDown}
					onBlur={() => setRecording(false)}
					className={cn(
						"flex h-8 min-w-[150px] flex-1 items-center gap-1 rounded-md border bg-background px-2 text-left text-[12.5px] transition-colors focus-visible:outline-none",
						recording ? "border-accent ring-2 ring-accent/30" : "border-border hover:border-border-strong",
					)}
				>
					{recording ? (
						<span className="text-muted-foreground">Press a shortcut…</span>
					) : keys.length === 0 ? (
						<span className="text-passive">Not set</span>
					) : (
						keys.map((key, i) => (
							<kbd
								key={`${key}-${i}`}
								className="inline-flex h-5 min-w-5 items-center justify-center rounded-sm border border-border-strong bg-surface px-1 font-sans text-[11px] text-foreground"
							>
								{key}
							</kbd>
						))
					)}
				</button>
				{value !== defaultValue && (
					<Button type="button" variant="ghost" size="sm" onClick={() => onChange(defaultValue)}>
						Reset to {shortcutLabel(defaultValue, mac)}
					</Button>
				)}
				{value !== "" && value === defaultValue && (
					<Button type="button" variant="ghost" size="sm" onClick={() => onChange("")}>
						Unbind
					</Button>
				)}
			</div>
			<p id={`${id}-hint`} className="text-[11.5px] leading-[1.5] text-passive">
				{refusal ? (
					<span className="text-warning">{refusal}</span>
				) : recording ? (
					"Esc cancels. Delete leaves it unbound."
				) : clash ? (
					<span className="text-warning">
						{clash.what === "Developer Tools"
							? `${shortcutLabel(value, mac)} also opens Developer Tools, and the app menu takes it first - it will not reach the editor.`
							: `${shortcutLabel(value, mac)} is also ${clash.what}. Inside the editor, this one wins.`}
					</span>
				) : null}
			</p>
		</div>
	);
}
