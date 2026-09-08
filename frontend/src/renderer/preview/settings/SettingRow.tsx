import { useState } from "react";
import { ChevronRight, Lock, Zap } from "lucide-react";
import { Button } from "../../components/ui/button";
import { Input } from "../../components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../../components/ui/select";
import { Switch } from "../../components/ui/switch";
import { Textarea } from "../../components/ui/textarea";
import { cn } from "../../lib/utils";
import { TIMING_LABEL, TIMING_TONE, type Ownership, type Setting } from "./model";

// SettingRow is the primitive both variants are built from, and it is where the
// redesign actually happens. At rest one row answers all three questions a person
// arrives with: what is this called, what is it set to, and what changes if I
// touch it - plus two chips saying WHOSE setting it is and WHEN it bites. The long
// prose that fills today's page folds away behind the row and comes back with the
// control.

function Chip({
	children,
	tone = "quiet",
	title,
}: {
	children: React.ReactNode;
	tone?: "quiet" | "warn" | "accent";
	title?: string;
}) {
	return (
		<span
			title={title}
			className={cn(
				"inline-flex shrink-0 items-center gap-1 rounded-full border px-2 py-[1px] font-mono text-[10px] uppercase tracking-[0.05em]",
				tone === "warn" && "border-warning/35 bg-warning/10 text-warning",
				tone === "accent" && "border-accent/35 bg-accent-weak text-accent",
				tone === "quiet" && "border-border-strong text-passive",
			)}
		>
			{children}
		</span>
	);
}

// The scope chip. Its whole job is to stop a person guessing at a hierarchy that
// mostly does not exist: of ~40 settings exactly one is genuinely overridable.
function ScopeChip({ ownership }: { ownership: Ownership }) {
	switch (ownership.kind) {
		case "global-only":
			return <Chip title="Set once for every project on this Mac. No project can change it.">Global</Chip>;
		case "global-appendable":
			return (
				<Chip title="Global. A project cannot replace this, only add text after it.">Global · projects append</Chip>
			);
		case "project-only":
			return <Chip title="This project only. Other projects are unaffected.">This project</Chip>;
		case "global-overridable":
			return ownership.overriddenBy.length > 0 ? (
				<Chip tone="accent" title={ownership.overriddenBy.map((o) => `${o.project}: ${o.value}`).join(", ")}>
					Global · {ownership.overriddenBy.length} project{ownership.overriddenBy.length === 1 ? "" : "s"} override
				</Chip>
			) : (
				<Chip title="Global, and a project may override it">Global · overridable</Chip>
			);
		case "project-override":
			return ownership.overriding ? (
				<Chip tone="accent" title={`Global default is ${ownership.globalValue}`}>
					Overrides global
				</Chip>
			) : (
				<Chip title={`Following the global default: ${ownership.globalValue}`}>Inheriting global</Chip>
			);
		case "project-appends":
			return <Chip title={`Added on top of ${ownership.base}, never instead of it`}>Appends to global</Chip>;
		case "global-session-override":
			return (
				<Chip tone="accent" title={`${ownership.followers} running sessions follow this`}>
					Global · each session can override
				</Chip>
			);
		case "readonly":
			return (
				<Chip>
					<Lock className="h-2.5 w-2.5" aria-hidden="true" /> Read-only
				</Chip>
			);
	}
}

// valueAtRest is what makes a section scannable: the right-hand column reads as a
// list of current answers, so you can check a whole section without opening one row.
export function valueAtRest(setting: Setting): string {
	const c = setting.control;
	switch (c.type) {
		case "toggle":
			return c.value ? "On" : "Off";
		case "select":
			return c.value;
		case "text":
			return c.value || `${c.placeholder ?? "empty"} (default)`;
		case "number":
			return `${c.value} ${c.unit}`;
		case "editor":
			return c.customized ? "Customised" : "Default";
		case "action":
			return "";
		case "readonly":
			return "";
	}
}

export function SettingRow({ setting, defaultOpen = false }: { setting: Setting; defaultOpen?: boolean }) {
	const [open, setOpen] = useState(defaultOpen);
	const [draft, setDraft] = useState(setting.control);
	const value = valueAtRest({ ...setting, control: draft });
	const isAction = draft.type === "action";
	const isReadonly = draft.type === "readonly";

	return (
		<div
			className={cn(
				"rounded-lg border transition-colors",
				open ? "border-border-strong bg-card" : "border-transparent hover:border-border hover:bg-interactive-hover",
			)}
		>
			<button
				type="button"
				aria-expanded={open}
				onClick={() => setOpen((o) => !o)}
				className="flex w-full items-start gap-3 px-3 py-2.5 text-left"
			>
				<ChevronRight
					aria-hidden="true"
					className={cn(
						"mt-[3px] h-3.5 w-3.5 shrink-0 text-passive transition-transform",
						open && "rotate-90 text-muted-foreground",
					)}
				/>
				<span className="flex min-w-0 flex-1 flex-col gap-1">
					<span className="flex min-w-0 items-center gap-2">
						<span className="!text-[13px] !font-medium truncate text-foreground">{setting.name}</span>
						{setting.changed && (
							<span aria-label="changed from default" className="h-1.5 w-1.5 shrink-0 rounded-full bg-accent" />
						)}
					</span>
					<span className="text-[11.5px] leading-[1.5] text-muted-foreground">{setting.summary}</span>
					<span className="mt-0.5 flex flex-wrap items-center gap-1.5">
						<ScopeChip ownership={setting.ownership} />
						{/* A read-only fact has no "when" - the scope chip already said it. */}
						{setting.timing !== "readonly" && (
							<Chip tone={TIMING_TONE[setting.timing]}>
								{setting.timing === "instant" && <Zap className="h-2.5 w-2.5" aria-hidden="true" />}
								{TIMING_LABEL[setting.timing]}
							</Chip>
						)}
					</span>
				</span>
				{/* A gated setting has no value worth reporting - showing "Off" would read
				    as a choice somebody made rather than a control that is not available. */}
				{!isAction && !isReadonly && (
					<span className="ml-auto shrink-0 pt-[1px] font-mono text-[11.5px] text-passive">
						{setting.gate ? "Unavailable" : value}
					</span>
				)}
			</button>

			{open && (
				<div className="border-t border-border py-3.5 pl-[38px] pr-3">
					{setting.gate ? (
						<p className="text-[11.5px] leading-[1.6] text-passive">{setting.gate}.</p>
					) : (
						<Control control={draft} onChange={setDraft} />
					)}
					{setting.detail && (
						<p className="mt-3 max-w-[62ch] text-[11.5px] leading-[1.65] text-passive">{setting.detail}</p>
					)}
					<InheritanceNote setting={setting} />
				</div>
			)}
		</div>
	);
}

// The override, stated AT the field being overridden rather than in a header three
// scrolls away - the second half of what Fluke asked for.
function InheritanceNote({ setting }: { setting: Setting }) {
	const o = setting.ownership;
	if (o.kind === "project-override") {
		return (
			<div className="mt-3 flex flex-wrap items-center gap-2.5 rounded-md border border-border bg-background/60 px-3 py-2 text-[11.5px]">
				<span className="text-passive">Global default</span>
				<span className="font-mono text-foreground">{o.globalValue}</span>
				{o.overriding ? (
					<>
						<span className="text-warning">· this project overrides it</span>
						<Button type="button" variant="ghost" size="sm" className="ml-auto">
							Use global default
						</Button>
					</>
				) : (
					<span className="text-passive">· this project follows it</span>
				)}
			</div>
		);
	}
	if (o.kind === "global-overridable" && o.overriddenBy.length > 0) {
		return (
			<div className="mt-3 rounded-md border border-border bg-background/60 px-3 py-2 text-[11.5px]">
				<span className="text-passive">Overridden by</span>
				<span className="ml-2 text-foreground">
					{o.overriddenBy.map((x) => `${x.project} (${x.value})`).join(", ")}
				</span>
			</div>
		);
	}
	if (o.kind === "global-session-override") {
		return (
			<div className="mt-3 rounded-md border border-border bg-background/60 px-3 py-2 text-[11.5px] text-passive">
				<span className="text-foreground">{o.followers} running sessions</span> follow this value. A session that set
				its own answer in its Reviews tab keeps it.
			</div>
		);
	}
	if (o.kind === "project-appends") {
		return (
			<div className="mt-3 rounded-md border border-border bg-background/60 px-3 py-2 text-[11.5px] text-passive">
				Added after <span className="text-foreground">{o.base}</span>, which stays in force.
			</div>
		);
	}
	return null;
}

function Control({ control, onChange }: { control: Setting["control"]; onChange: (c: Setting["control"]) => void }) {
	switch (control.type) {
		case "toggle":
			return (
				<div className="flex items-center gap-3">
					<Switch checked={control.value} onCheckedChange={(v) => onChange({ ...control, value: v })} />
					<span className="text-[12px] text-muted-foreground">{control.value ? "On" : "Off"}</span>
				</div>
			);
		case "select":
			return (
				<Select value={control.value} onValueChange={(v) => onChange({ ...control, value: v })}>
					<SelectTrigger className="h-8 w-full max-w-[340px] text-[13px]">
						<SelectValue />
					</SelectTrigger>
					<SelectContent>
						{control.options.map((opt) => (
							<SelectItem key={opt} value={opt}>
								{opt}
							</SelectItem>
						))}
					</SelectContent>
				</Select>
			);
		case "text":
			return (
				<Input
					className={cn("h-8 max-w-[340px] text-[12.5px]", control.mono && "font-mono")}
					placeholder={control.placeholder}
					value={control.value}
					spellCheck={false}
					onChange={(e) => onChange({ ...control, value: e.target.value })}
				/>
			);
		case "number":
			return (
				<div className="flex items-center gap-2.5">
					<Input
						type="number"
						className="h-8 w-28 text-[12.5px]"
						value={control.value}
						onChange={(e) => onChange({ ...control, value: Number(e.target.value) || 0 })}
					/>
					<span className="text-[12px] text-passive">{control.unit}</span>
				</div>
			);
		case "editor":
			return (
				<div className="flex flex-col gap-2">
					{control.placeholders && control.placeholders.length > 0 && (
						<p className="text-[11px] text-passive">
							Placeholders: <code className="font-mono text-muted-foreground">{control.placeholders.join(" ")}</code>
						</p>
					)}
					<Textarea
						value={control.value}
						placeholder="Leave blank to add nothing"
						onChange={(e) => onChange({ ...control, value: e.target.value })}
						className="min-h-[150px] resize-y font-mono text-[12px] leading-relaxed"
					/>
					<div className="flex items-center gap-2.5">
						<Button type="button" variant="outline" size="sm" disabled={!control.customized}>
							Reset to default
						</Button>
						<span className="text-[11px] text-passive">Staged - Save changes to apply</span>
					</div>
				</div>
			);
		case "action":
			return (
				<div className="flex items-center gap-3">
					<Button type="button" variant="outline" size="sm">
						{control.label}
					</Button>
					{control.note && <span className="text-[11.5px] text-passive">{control.note}</span>}
				</div>
			);
		case "readonly":
			return (
				<div className="flex flex-col gap-1 font-mono text-[12px] text-foreground">
					{control.value.split("\n").map((line) => (
						<span key={line}>{line}</span>
					))}
				</div>
			);
	}
}
