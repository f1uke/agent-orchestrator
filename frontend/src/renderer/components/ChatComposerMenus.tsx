import { ChevronDown, FileCode, Sparkles } from "lucide-react";
import type { ReactNode } from "react";
import type { PermissionMode } from "../lib/chat-answers";
import type { ChatModel } from "../lib/chat-fold";
import { type ModelMenu, modelMenu } from "../lib/chat-menus";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuLabel,
	DropdownMenuRadioGroup,
	DropdownMenuRadioItem,
	DropdownMenuSeparator,
	DropdownMenuSub,
	DropdownMenuSubContent,
	DropdownMenuSubTrigger,
	DropdownMenuTrigger,
} from "./ui/dropdown-menu";

export type MenuItem = { id: string; label: ReactNode; hint?: string; file?: boolean };
export type MenuSection = { label: string; items: MenuItem[] };

type TokenMenuProps = {
	id: string;
	label: string;
	sections: readonly MenuSection[];
	active: number;
	empty: string;
	onPick: (index: number) => void;
	onActive: (index: number) => void;
};

export const optionId = (menuId: string, index: number) => `${menuId}-${index}`;

export function TokenMenu({ id, label, sections, active, empty, onPick, onActive }: TokenMenuProps) {
	let index = -1;
	return (
		<div aria-label={label} className="chat-menu" id={id} role="listbox">
			{sections.length === 0 ? <div className="chat-menu__empty">{empty}</div> : null}
			{sections.map((section) => (
				<div aria-label={section.label} key={section.label} role="group">
					<div aria-hidden="true" className="chat-menu__label">
						{section.label}
					</div>
					{section.items.map((item) => {
						index += 1;
						const at = index;
						return (
							<div
								aria-selected={at === active}
								className="chat-menu__item"
								data-active={at === active || undefined}
								id={optionId(id, at)}
								key={item.id}
								onMouseDown={(e) => e.preventDefault()}
								onClick={() => onPick(at)}
								onMouseMove={() => at !== active && onActive(at)}
								role="option"
							>
								{item.file ? <FileCode className="chat-icon chat-menu__icon" aria-hidden="true" /> : null}
								<code className="chat-menu__name">{item.label}</code>
								{item.hint ? <span className="chat-menu__hint">{item.hint}</span> : null}
							</div>
						);
					})}
				</div>
			))}
		</div>
	);
}

const MODES: Array<{ mode: PermissionMode; label: string }> = [
	{ mode: "default", label: "Ask before edits" },
	{ mode: "acceptEdits", label: "Auto-accept edits" },
	{ mode: "plan", label: "Plan" },
	{ mode: "auto", label: "Auto" },
];

const MODE_LABEL: Record<string, string> = {
	...Object.fromEntries(MODES.map((m) => [m.mode, m.label])),
	bypassPermissions: "Bypass permissions",
	dontAsk: "Don't ask",
};

const modeLabel = (mode: string) => MODE_LABEL[mode] ?? (mode || "Mode");

function ModelItem({ model, menu }: { model: ChatModel; menu: ModelMenu }) {
	return (
		<DropdownMenuRadioItem className="chat-model-item" title={model.description || undefined} value={model.value}>
			<span className="chat-model-item__name">{model.displayName || model.value}</span>
			<span className="chat-model-item__hint">{model.value === menu.defaultValue ? "default" : model.description}</span>
		</DropdownMenuRadioItem>
	);
}

type PickerProps = {
	models: readonly ChatModel[];
	model: string;
	permissionMode: string;
	disabled: boolean;
	onModel: (value: string) => void;
	onMode: (mode: PermissionMode) => void;
};

export function ModelModePicker({ models, model, permissionMode, disabled, onModel, onMode }: PickerProps) {
	const menu = modelMenu(models, model);
	const modelName = menu.current?.displayName ?? model;
	const label = modelName ? `${modelName} · ${modeLabel(permissionMode)}` : modeLabel(permissionMode);
	const efforts = menu.current?.supportsEffort ? menu.current.supportedEffortLevels : [];
	const current = menu.current?.value ?? "";
	return (
		<DropdownMenu>
			<DropdownMenuTrigger asChild disabled={disabled}>
				<button aria-label={`Model and mode: ${label}`} className="chat-picker" type="button">
					<Sparkles className="chat-icon" aria-hidden="true" />
					{label}
					<ChevronDown className="chat-icon" aria-hidden="true" />
				</button>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end" className="chat-model-menu" side="top">
				{models.length ? (
					<>
						<DropdownMenuLabel>Model</DropdownMenuLabel>
						<DropdownMenuRadioGroup onValueChange={onModel} value={current}>
							{menu.primary.map((m) => (
								<ModelItem key={m.value} menu={menu} model={m} />
							))}
							{menu.pinned.length ? (
								<DropdownMenuSub>
									<DropdownMenuSubTrigger>Other versions</DropdownMenuSubTrigger>
									<DropdownMenuSubContent className="chat-model-menu">
										{menu.pinned.map((m) => (
											<ModelItem key={m.value} menu={menu} model={m} />
										))}
									</DropdownMenuSubContent>
								</DropdownMenuSub>
							) : null}
						</DropdownMenuRadioGroup>
						{efforts.length ? (
							<>
								<DropdownMenuLabel>Effort</DropdownMenuLabel>
								<p className="chat-model-menu__note" data-testid="chat-effort-hint">
									{efforts.join(" · ")}. Set when the session starts.
								</p>
							</>
						) : null}
						<DropdownMenuSeparator />
					</>
				) : null}
				<DropdownMenuLabel>Mode</DropdownMenuLabel>
				<DropdownMenuRadioGroup onValueChange={(mode) => onMode(mode as PermissionMode)} value={permissionMode}>
					{MODES.map(({ mode, label: name }) => (
						<DropdownMenuRadioItem key={mode} value={mode}>
							{name}
						</DropdownMenuRadioItem>
					))}
				</DropdownMenuRadioGroup>
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
