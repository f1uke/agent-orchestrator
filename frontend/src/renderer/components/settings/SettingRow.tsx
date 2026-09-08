import { useState, type ReactNode } from "react";
import { ChevronRight, Lock, Zap } from "lucide-react";
import { cn } from "../../lib/utils";
import { TIMING_LABEL, TIMING_TONE, type SettingOwnership, type SettingTiming } from "./setting-meta";

// SettingRow is the primitive every setting on this page renders as. At rest one
// row answers the three questions a person arrives with - what is this called,
// what is it set to, and what changes if I touch it - plus two chips saying whose
// setting it is and when a change lands. Everything longer (caveats, exemptions,
// where the audit log goes) folds away and comes back with the control.
//
// It is deliberately PROP-DRIVEN rather than driven by a catalog: the control is
// a child, so each section keeps passing its own live-wired input and the form
// hooks are untouched.

function Chip({
	children,
	tone = "quiet",
	title,
}: {
	children: ReactNode;
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

function ScopeChip({ ownership }: { ownership: SettingOwnership }) {
	switch (ownership.kind) {
		case "global-only":
			return <Chip title="Set once for every project on this Mac. No project can change it.">Global</Chip>;
		case "global-appendable":
			return (
				<Chip title="Global. A project cannot replace this, only add text after it.">Global · projects append</Chip>
			);
		case "global-overridable": {
			const overrides = ownership.overriddenBy ?? [];
			return overrides.length > 0 ? (
				<Chip tone="accent" title={overrides.map((o) => `${o.project}: ${o.value}`).join(", ")}>
					Global · {overrides.length} project{overrides.length === 1 ? "" : "s"} override
				</Chip>
			) : (
				<Chip title="Global, and a project may override it from its own Settings.">Global · overridable</Chip>
			);
		}
		case "project-only":
			return <Chip title="This project only. Other projects are unaffected.">This project</Chip>;
		case "project-override":
			return ownership.overriding ? (
				<Chip tone="accent" title={`Global default is ${ownership.globalValue}`}>
					Overrides global
				</Chip>
			) : (
				<Chip title={`Following the global default: ${ownership.globalValue}`}>Inheriting global</Chip>
			);
		case "project-appends":
			return <Chip title={`Added after ${ownership.base}, never instead of it`}>Appends to global</Chip>;
		case "global-session-override":
			return (
				<Chip tone="accent" title="A session that answered for itself in its Reviews tab keeps its answer.">
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

// The override stated AT the field being overridden, rather than in a header
// several scrolls away.
function InheritanceNote({ ownership, onUseGlobal }: { ownership: SettingOwnership; onUseGlobal?: () => void }) {
	if (ownership.kind === "project-override") {
		return (
			<div className="mt-3 flex flex-wrap items-center gap-2.5 rounded-md border border-border bg-background/60 px-3 py-2 text-[11.5px]">
				<span className="text-passive">Global default</span>
				<span className="font-mono text-foreground">{ownership.globalValue}</span>
				{ownership.overriding ? (
					<>
						<span className="text-warning">· this project overrides it</span>
						{onUseGlobal && (
							<button
								type="button"
								onClick={onUseGlobal}
								className="!text-[11.5px] ml-auto rounded text-foreground underline-offset-2 hover:underline"
							>
								Use global default
							</button>
						)}
					</>
				) : (
					<span className="text-passive">· this project follows it</span>
				)}
			</div>
		);
	}
	if (ownership.kind === "global-overridable" && (ownership.overriddenBy?.length ?? 0) > 0) {
		return (
			<div className="mt-3 rounded-md border border-border bg-background/60 px-3 py-2 text-[11.5px]">
				<span className="text-passive">Overridden by</span>
				<span className="ml-2 text-foreground">
					{ownership.overriddenBy!.map((x) => `${x.project} (${x.value})`).join(", ")}
				</span>
			</div>
		);
	}
	if (ownership.kind === "project-appends") {
		return (
			<div className="mt-3 rounded-md border border-border bg-background/60 px-3 py-2 text-[11.5px] text-passive">
				Added after the global <span className="text-foreground">{ownership.base}</span>, which stays in force.
			</div>
		);
	}
	return null;
}

export function SettingRow({
	name,
	summary,
	detail,
	ownership,
	timing,
	value,
	modified = false,
	gate,
	controlId,
	onUseGlobal,
	defaultOpen = false,
	children,
}: {
	name: string;
	// The one honest sentence about what changes. Always written, never omitted -
	// a row without it is the problem this page was rebuilt to fix.
	summary: string;
	// Caveats, exemptions, audit-log locations. Folded with the control.
	detail?: ReactNode;
	ownership: SettingOwnership;
	timing: SettingTiming;
	// What the control currently says, shown at rest so a section can be checked
	// without opening a single row. Omitted for pure actions and read-only facts.
	value?: string;
	// Dirty since the last save - the same fact the old "● Modified" tag carried.
	modified?: boolean;
	// Set when the setting is not available here (wrong forge, no remote). The
	// control is replaced by this explanation and the value reads "Unavailable",
	// because showing "Off" would look like a choice somebody made.
	gate?: string;
	// Id of the control inside `children`. The row's name becomes that control's
	// accessible name via a visually-hidden label, so a screen reader hears the
	// same words a sighted user reads and existing by-label queries keep working.
	controlId?: string;
	onUseGlobal?: () => void;
	defaultOpen?: boolean;
	children?: ReactNode;
}) {
	const [open, setOpen] = useState(defaultOpen);
	return (
		<div
			className={cn(
				"rounded-lg border transition-colors",
				open ? "border-border-strong bg-card" : "border-transparent hover:border-border hover:bg-interactive-hover",
			)}
		>
			<button
				type="button"
				// Marks the disclosure so a test can open every row in a section without
				// also hitting the select triggers, which carry aria-expanded too.
				data-testid="setting-row"
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
						<span className="!text-[13px] !font-medium truncate text-foreground">{name}</span>
						{modified && (
							<span
								aria-label="modified"
								className="h-1.5 w-1.5 shrink-0 rounded-full bg-warning shadow-[0_0_6px_rgba(232,193,74,0.5)]"
							/>
						)}
					</span>
					<span className="text-[11.5px] leading-[1.5] text-muted-foreground">{summary}</span>
					<span className="mt-0.5 flex flex-wrap items-center gap-1.5">
						<ScopeChip ownership={ownership} />
						{/* A read-only fact has no "when" - the scope chip already said it. */}
						{timing !== "readonly" && (
							<Chip tone={TIMING_TONE[timing]}>
								{timing === "instant" && <Zap className="h-2.5 w-2.5" aria-hidden="true" />}
								{TIMING_LABEL[timing]}
							</Chip>
						)}
					</span>
				</span>
				{value !== undefined && (
					<span className="ml-auto shrink-0 pt-[1px] font-mono text-[11.5px] text-passive">
						{gate ? "Unavailable" : value}
					</span>
				)}
			</button>

			{open && (
				// Left padding matches the row title exactly: px-3 (12) + icon (14) + gap-3 (12).
				<div className="border-t border-border py-3.5 pl-[38px] pr-3">
					{gate ? (
						<p className="max-w-[62ch] text-[11.5px] leading-[1.6] text-passive">{gate}</p>
					) : (
						<>
							{controlId && (
								<label htmlFor={controlId} className="sr-only">
									{name}
								</label>
							)}
							{children}
						</>
					)}
					{detail && <div className="mt-3 max-w-[62ch] text-[11.5px] leading-[1.65] text-passive">{detail}</div>}
					<InheritanceNote ownership={ownership} onUseGlobal={onUseGlobal} />
				</div>
			)}
		</div>
	);
}

// SettingRows is the list wrapper - rows sit tight together so a section reads as
// one scannable column rather than a stack of cards.
export function SettingRows({ children }: { children: ReactNode }) {
	return <div className="flex flex-col gap-0.5">{children}</div>;
}

// SectionHeading is the one heading a section carries. The hint is the section's
// own promise about what lives here, which under B is also the timing answer.
export function SectionHeading({ title, hint }: { title: string; hint: string }) {
	return (
		<div className="mb-3 flex items-baseline gap-2.5 border-b border-border pb-3 pt-2.5">
			<h2 className="text-[15px] font-semibold tracking-[-0.01em] text-foreground">{title}</h2>
			<span className="text-[12px] font-normal text-passive">· {hint}</span>
		</div>
	);
}
