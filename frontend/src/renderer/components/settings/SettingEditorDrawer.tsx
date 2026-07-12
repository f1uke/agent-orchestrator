import { Sheet, SheetClose, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "../ui/sheet";
import { Button } from "../ui/button";
import { Textarea } from "../ui/textarea";

// SettingEditorDrawer is the slide-over the large prompt/message editors open
// into (locked decision 2 — a drawer, NOT the inline accordion the mock drew).
// The full mono textarea + placeholder reference + inline Reset live here with
// room to breathe. Edits STAGE into the parent's draft (via onChange) and route
// through the single save bar — the drawer never self-saves; "Done" just closes
// it, keeping the staged draft.
export function SettingEditorDrawer({
	open,
	onOpenChange,
	title,
	description,
	textareaLabel,
	value,
	onChange,
	placeholders,
	onReset,
	resetDisabled = false,
}: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	title: string;
	description?: string;
	// Accessible name for the textarea (kept stable so tests/labels resolve).
	textareaLabel: string;
	value: string;
	onChange: (value: string) => void;
	placeholders?: string[];
	onReset: () => void;
	resetDisabled?: boolean;
}) {
	return (
		<Sheet open={open} onOpenChange={onOpenChange}>
			<SheetContent side="right" className="gap-0 p-0 sm:max-w-xl">
				<div className="flex h-full min-h-0 flex-col">
					<SheetHeader className="border-b border-border p-5">
						<SheetTitle className="text-[15px]">{title}</SheetTitle>
						{description && <SheetDescription className="text-[12px] text-passive">{description}</SheetDescription>}
					</SheetHeader>
					<div className="min-h-0 flex-1 overflow-y-auto p-5">
						{placeholders && placeholders.length > 0 && (
							<p className="mb-2 text-[11px] text-passive">
								Placeholders: <code className="font-mono text-muted-foreground">{placeholders.join(" ")}</code>
							</p>
						)}
						<Textarea
							aria-label={textareaLabel}
							value={value}
							onChange={(event) => onChange(event.target.value)}
							className="min-h-[340px] resize-y font-mono text-[12px] leading-relaxed"
						/>
					</div>
					<div className="flex items-center gap-3 border-t border-border p-5">
						<Button type="button" variant="outline" onClick={onReset} disabled={resetDisabled}>
							Reset to default
						</Button>
						<span className="ml-auto text-[11px] text-passive">Staged — Save changes to apply</span>
						<SheetClose asChild>
							<Button type="button" variant="primary">
								Done
							</Button>
						</SheetClose>
					</div>
				</div>
			</SheetContent>
		</Sheet>
	);
}
