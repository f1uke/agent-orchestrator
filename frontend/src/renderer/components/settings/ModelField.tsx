import { useMemo, useRef, useState } from "react";
import type { components } from "../../../api/schema";
import { Input } from "../ui/input";
import { Popover, PopoverAnchor, PopoverContent } from "../ui/popover";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../ui/select";

type AgentInfo = components["schemas"]["AgentInfo"];

// nextModelOnAgentChange decides what a per-kind model should become when the
// user switches that kind's agent. It degrades gracefully WITHOUT wiping a valid
// free-form value: a value is reset to Default (empty) only when the target agent
// is fixed-tier AND its catalog doesn't list the value (it genuinely can't run
// it). An open-ended target keeps any typed value (its catalog is only examples),
// and an already-empty value is left untouched.
export function nextModelOnAgentChange(currentModel: string, target: AgentInfo | undefined): string {
	if (!currentModel) return currentModel;
	if (target?.modelsOpenEnded) return currentModel;
	const models = target?.models ?? [];
	return models.some((m) => m.id === currentModel) ? currentModel : "";
}

// ModelField picks the right control for the chosen agent: an editable combobox
// for open-ended agents (opencode — a free-typed provider/model id with the
// catalog as suggestions) and the fixed shadcn Select for fixed-tier agents
// (claude-code, codex) or a short hint when the agent exposes no tiers.
export function ModelField({
	id,
	value,
	agent,
	onChange,
}: {
	id: string;
	value: string;
	agent: AgentInfo | undefined;
	onChange: (value: string) => void;
}) {
	if (agent?.modelsOpenEnded) {
		return <ModelCombobox id={id} value={value} agent={agent} onChange={onChange} />;
	}
	return <ModelSelect id={id} value={value} agent={agent} onChange={onChange} />;
}

// ModelSelect is the fixed-tier control: a plain Select of the agent's tiers plus
// an explicit Default. A stored value the catalog doesn't list (e.g. a pinned id
// set via the CLI) is preserved as an extra option so opening this control never
// drops it. Unchanged from the pre-combobox behavior.
function ModelSelect({
	id,
	value,
	agent,
	onChange,
}: {
	id: string;
	value: string;
	agent: AgentInfo | undefined;
	onChange: (value: string) => void;
}) {
	const models = agent?.models ?? [];
	if (models.length === 0) {
		// The chosen agent exposes no tier choice (or none is selected yet):
		// surface a short hint rather than an empty selector.
		return (
			<p className="text-[12px] leading-8 text-passive">
				{agent
					? `${agent.label} uses its own default model (no selectable tiers).`
					: "Select an agent to choose a model."}
			</p>
		);
	}
	const known = models.some((m) => m.id === value);
	const extra = value && !known ? [{ id: value, label: value }] : [];
	return (
		<Select value={value || "__default__"} onValueChange={(v) => onChange(v === "__default__" ? "" : v)}>
			<SelectTrigger id={id} className="h-8 w-full text-[13px]">
				<SelectValue />
			</SelectTrigger>
			<SelectContent>
				<SelectItem value="__default__">Default ({agent?.label ?? "agent"} default)</SelectItem>
				{[...extra, ...models].map((m) => (
					<SelectItem key={m.id} value={m.id}>
						{m.label}
					</SelectItem>
				))}
			</SelectContent>
		</Select>
	);
}

type ComboRow = { id: string; label: string; hint?: string; isDefault: boolean };

// ModelCombobox is the open-ended control: an editable input whose typed value is
// the model id, with the agent's catalog offered as filterable suggestions and an
// explicit "Default" row that maps to the agent's own default (empty). A typed
// value outside the catalog is valid and round-trips. Built from the shadcn
// Popover + Input primitives (no cmdk in this app); the input keeps focus while
// the suggestion list is open, arrow keys move the highlight, Enter accepts it,
// and Escape closes.
function ModelCombobox({
	id,
	value,
	agent,
	onChange,
}: {
	id: string;
	value: string;
	agent: AgentInfo | undefined;
	onChange: (value: string) => void;
}) {
	const models = agent?.models ?? [];
	// The first catalog entry is a real, well-formed id, so it doubles as the
	// placeholder example; fall back to a generic shape if a future open-ended
	// agent ships no examples.
	const placeholder = models[0]?.id ?? "provider/model";
	const [open, setOpen] = useState(false);
	const [active, setActive] = useState(-1);
	const inputRef = useRef<HTMLInputElement>(null);

	const rows = useMemo<ComboRow[]>(() => {
		const query = value.trim().toLowerCase();
		const filtered = query
			? models.filter((m) => m.id.toLowerCase().includes(query) || m.label.toLowerCase().includes(query))
			: models;
		return [
			{ id: "", label: `Default (${agent?.label ?? "agent"} default)`, isDefault: true },
			...filtered.map((m) => ({ id: m.id, label: m.label, hint: m.id, isDefault: false })),
		];
	}, [value, models, agent?.label]);

	const commit = (row: ComboRow) => {
		onChange(row.id);
		setOpen(false);
		setActive(-1);
		inputRef.current?.focus();
	};

	const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
		if (e.key === "ArrowDown") {
			e.preventDefault();
			setOpen(true);
			setActive((i) => (i + 1) % rows.length);
			return;
		}
		if (e.key === "ArrowUp") {
			e.preventDefault();
			setOpen(true);
			setActive((i) => (i <= 0 ? rows.length - 1 : i - 1));
			return;
		}
		if (e.key === "Enter" && open && active >= 0 && active < rows.length) {
			e.preventDefault();
			commit(rows[active]);
			return;
		}
		if (e.key === "Escape") {
			setOpen(false);
			setActive(-1);
		}
	};

	return (
		<Popover open={open} onOpenChange={setOpen}>
			<PopoverAnchor asChild>
				<Input
					id={id}
					ref={inputRef}
					role="combobox"
					aria-expanded={open}
					aria-autocomplete="list"
					autoComplete="off"
					spellCheck={false}
					className="text-[13px]"
					value={value}
					placeholder={placeholder}
					onChange={(e) => {
						onChange(e.target.value);
						setOpen(true);
						setActive(-1);
					}}
					onFocus={() => setOpen(true)}
					onKeyDown={handleKeyDown}
				/>
			</PopoverAnchor>
			<PopoverContent
				align="start"
				sideOffset={4}
				onOpenAutoFocus={(e) => e.preventDefault()}
				className="w-(--radix-popover-trigger-width) p-1"
			>
				<ul role="listbox" className="flex max-h-64 flex-col overflow-y-auto">
					{rows.map((row, i) => (
						<li key={row.isDefault ? "__default__" : row.id}>
							<button
								type="button"
								role="option"
								aria-selected={i === active}
								data-active={i === active || undefined}
								className="flex w-full flex-col items-start gap-0.5 rounded-sm px-2 py-1.5 text-left text-[13px] outline-hidden hover:bg-accent hover:text-accent-foreground data-[active]:bg-accent data-[active]:text-accent-foreground"
								onMouseEnter={() => setActive(i)}
								onClick={() => commit(row)}
							>
								<span className={row.isDefault ? "text-muted-foreground" : "text-foreground"}>{row.label}</span>
								{row.hint && <span className="font-mono text-[11px] text-passive">{row.hint}</span>}
							</button>
						</li>
					))}
				</ul>
			</PopoverContent>
		</Popover>
	);
}
