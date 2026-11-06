import { ArrowUp, AtSign, History, Paperclip, Slash } from "lucide-react";
import { useLayoutEffect, useMemo, useRef, useState } from "react";
import type { ChatActions } from "../hooks/useChatActions";
import { useWorkspaceFiles } from "../hooks/useWorkspaceFiles";
import type { PermissionMode } from "../lib/chat-answers";
import type { ChatResult } from "../lib/chat-api";
import { BASH_BLOCK_TEXT, BASH_HINT, bashBlock, bashModeEntry } from "../lib/chat-bash";
import { MAX_IMAGES, prepareImage, withAttachedPaths } from "../lib/chat-attach";
import type { ChatCommand, ChatModel, RemoteControl, UserItem } from "../lib/chat-fold";
import { isImageMime } from "../lib/chat-images";
import { type ComposerToken, replaceToken, slashGroups, tokenAt } from "../lib/chat-menus";
import { rankFiles } from "../lib/open-quickly";
import { ChatAttachments, type Chip, type Upload } from "./ChatAttachments";
import { type MenuSection, ModelModePicker, optionId, TokenMenu } from "./ChatComposerMenus";
import { Popover, PopoverAnchor, PopoverContent } from "./ui/popover";

const MENU_ID = "chat-token-menu";
const SUGGESTION_ID = "chat-suggestion-description";
const BASH_HINT_ID = "chat-bash-hint";
const MAX_FILES = 8;

type Choice = { insert: string; hint?: string };
type TokenMenuModel = { label: string; sections: MenuSection[]; choices: Choice[]; empty: string };

function slashMenu(commands: readonly ChatCommand[], query: string): TokenMenuModel {
	const groups = slashGroups(commands, query);
	return {
		label: "Commands and skills",
		sections: groups.map((g) => ({
			label: g.label,
			items: g.commands.map((c) => ({ id: c.name, label: `/${c.name}`, hint: c.description })),
		})),
		choices: groups.flatMap((g) => g.commands.map((c) => ({ insert: `/${c.name}`, hint: c.argumentHint }))),
		empty: "No command matches",
	};
}

type FilesState = { loading: boolean; paths?: readonly string[] };

function mentionMenu(files: FilesState, query: string): TokenMenuModel {
	const matches = rankFiles(files.paths ?? [], query, MAX_FILES);
	let empty = "No file matches";
	if (files.loading) empty = "Loading files…";
	else if (!files.paths) empty = "This workspace has no file index";
	else if (query === "") empty = "Type to search files";
	return {
		label: "Files",
		sections: matches.length
			? [{ label: "Files", items: matches.map((m) => ({ id: m.path, label: m.path, file: true })) }]
			: [],
		choices: matches.map((m) => ({ insert: `@${m.path}` })),
		empty,
	};
}

export type Prefill = { text: string; seq: number };

/** What Enter does: "prompt" sends the text to Claude, "bash" runs it as a shell command. */
type Mode = "prompt" | "bash";

type ComposerProps = {
	sessionId: string;
	actions: ChatActions;
	busy: boolean;
	/** A "!" command is running or has not yet reached Claude. */
	bashBusy: boolean;
	queued: readonly UserItem[];
	permissionMode: string;
	commands: readonly ChatCommand[];
	models: readonly ChatModel[];
	model: string;
	remoteControl: RemoteControl;
	chrome: boolean;
	/** Claude's guess at the next message; shown as ghost text while the box is empty and the session idle. */
	suggestion: string;
	onToast: (message: string) => void;
	prefill?: Prefill;
};

export function ChatComposer({
	sessionId,
	actions,
	busy,
	bashBusy,
	queued,
	permissionMode,
	commands,
	models,
	model,
	remoteControl,
	chrome,
	suggestion,
	onToast,
	prefill,
}: ComposerProps) {
	const [text, setText] = useState("");
	const [mode, setMode] = useState<Mode>("prompt");
	const [caret, setCaret] = useState(0);
	const [chips, setChips] = useState<Chip[]>([]);
	const [preparing, setPreparing] = useState(0);
	const [sending, setSending] = useState(false);
	const [dismissedForText, setDismissedForText] = useState<string | null>(null);
	const [active, setActive] = useState(0);
	const [argHint, setArgHint] = useState<{ after: string; hint: string } | null>(null);
	const [wantFiles, setWantFiles] = useState(false);
	const [dismissedSuggestion, setDismissedSuggestion] = useState<string | null>(null);
	const nextId = useRef(0);
	const input = useRef<HTMLTextAreaElement>(null);
	const picker = useRef<HTMLInputElement>(null);
	const composer = useRef<HTMLDivElement>(null);
	const pendingCaret = useRef<number | null>(null);
	const disabled = actions.hostDown;
	const images = chips.flatMap((c) => (c.kind === "image" ? [c.image] : []));
	const uploaded = chips.flatMap((c) => (c.kind === "file" && c.upload.phase === "ready" ? [c.upload.path] : []));
	const settled = preparing === 0 && chips.every((c) => c.kind === "image" || c.upload.phase === "ready");
	const bash = mode === "bash";
	const block = bashBlock({ busy, bashBusy });
	const canSend = !disabled && !sending && !bashBusy && settled && (text.trim() !== "" || chips.length > 0);
	const canRun = !disabled && !sending && block === null && text.trim() !== "";

	if (suggestion === "" && dismissedSuggestion !== null) setDismissedSuggestion(null);
	const suggesting =
		suggestion !== "" && text === "" && !busy && !disabled && !bash && dismissedSuggestion !== suggestion;

	const token: ComposerToken | null = disabled || bash || dismissedForText === text ? null : tokenAt(text, caret);
	if (token?.kind === "mention" && !wantFiles) setWantFiles(true);
	const fileIndex = useWorkspaceFiles(sessionId, wantFiles);
	const filesLoading = fileIndex.isPending && wantFiles;
	const paths = fileIndex.data?.available ? fileIndex.data.paths : undefined;

	const kind = token?.kind;
	const query = token?.query ?? "";
	const tokenKey = kind ? `${kind}:${query}` : "";
	const [shownKey, setShownKey] = useState(tokenKey);
	if (shownKey !== tokenKey) {
		setShownKey(tokenKey);
		setActive(0);
	}

	const menu = useMemo(() => {
		if (kind === "slash") return commands.length ? slashMenu(commands, query) : null;
		if (kind === "mention") return mentionMenu({ loading: filesLoading, paths }, query);
		return null;
	}, [kind, query, commands, filesLoading, paths]);
	const activeIndex = menu ? Math.min(active, menu.choices.length - 1) : -1;

	useLayoutEffect(() => {
		if (pendingCaret.current === null || !input.current) return;
		input.current.focus();
		input.current.setSelectionRange(pendingCaret.current, pendingCaret.current);
		pendingCaret.current = null;
	});

	const edit = (next: string, at: number) => {
		setText(next);
		setCaret(at);
		pendingCaret.current = at;
	};

	const seenPrefill = useRef(prefill?.seq);
	useLayoutEffect(() => {
		if (!prefill || prefill.seq === seenPrefill.current) return;
		seenPrefill.current = prefill.seq;
		const next = bash || text.trim() === "" ? prefill.text : `${text}\n\n${prefill.text}`;
		setMode("prompt");
		edit(next, next.length);
	});

	const pick = (index: number) => {
		const choice = menu?.choices[index];
		if (!choice || !token) return;
		const next = replaceToken(text, token, choice.insert);
		edit(next.text, next.caret);
		setArgHint(choice.hint ? { after: next.text, hint: choice.hint } : null);
	};

	const setUpload = (id: number, upload: Upload) =>
		setChips((prev) => prev.map((c) => (c.id === id && c.kind === "file" ? { ...c, upload } : c)));

	const upload = async (id: number, file: File) => {
		const result = await actions.upload(file);
		if (result.ok) setUpload(id, { phase: "ready", path: result.file.path });
		else setUpload(id, { phase: "failed", message: result.error.message });
	};

	const addImage = async (file: File, id: number) => {
		const name = file.name || "pasted image";
		setPreparing((n) => n + 1);
		const prepared = await prepareImage(file);
		setPreparing((n) => n - 1);
		if (prepared.ok) setChips((prev) => [...prev, { kind: "image", id, name, image: prepared.image }]);
		else onToast(prepared.reason === "too_large" ? `${name} is larger than 5 MB.` : `Could not read ${name}.`);
	};

	const attach = (files: readonly File[]) => {
		let room = MAX_IMAGES - images.length - preparing;
		const skipped: string[] = [];
		for (const file of files) {
			const id = ++nextId.current;
			if (!isImageMime(file.type)) {
				const name = file.name || "pasted file";
				setChips((prev) => [
					...prev,
					{ kind: "file", id, name, size: file.size, file, upload: { phase: "uploading" } },
				]);
				void upload(id, file);
			} else if (room > 0) {
				room -= 1;
				void addImage(file, id);
			} else {
				skipped.push(file.name || "pasted image");
			}
		}
		if (skipped.length) onToast(`At most ${MAX_IMAGES} images per message. Skipped ${skipped.join(", ")}.`);
	};

	const retry = (id: number) => {
		const chip = chips.find((c) => c.id === id);
		if (chip?.kind !== "file") return;
		setUpload(id, { phase: "uploading" });
		void upload(id, chip.file);
	};

	const submit = async () => {
		if (!canSend) return;
		setSending(true);
		const result = await actions.send(withAttachedPaths(text, uploaded), images);
		setSending(false);
		if (result.ok) {
			setText("");
			setCaret(0);
			setChips([]);
			setArgHint(null);
		} else if (result.error.kind !== "host_not_running") {
			onToast(result.error.message);
		}
	};

	const run = async () => {
		if (!canRun) return;
		setSending(true);
		const result = await actions.runBash(text.trim());
		setSending(false);
		if (result.ok) {
			setText("");
			setCaret(0);
			setMode("prompt");
		} else if (result.error.kind !== "host_not_running") {
			onToast(result.error.message);
		}
	};

	const leaveBash = () => {
		setMode("prompt");
		edit("", 0);
	};

	const report = async (call: Promise<ChatResult>) => {
		const result = await call;
		if (!result.ok && result.error.kind !== "host_not_running") onToast(result.error.message);
	};

	const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
		if (e.nativeEvent.isComposing) return;
		if (bash) {
			if (e.key === "Escape" || (e.key === "Backspace" && text === "")) {
				e.preventDefault();
				leaveBash();
			} else if (e.key === "Enter" && !e.shiftKey) {
				e.preventDefault();
				void run();
			}
			return;
		}
		if (menu) {
			const n = menu.choices.length;
			if (n > 0 && (e.key === "ArrowDown" || e.key === "ArrowUp")) {
				e.preventDefault();
				setActive((activeIndex + (e.key === "ArrowDown" ? 1 : n - 1)) % n);
				return;
			}
			if (n > 0 && (e.key === "Tab" || (e.key === "Enter" && !e.shiftKey))) {
				e.preventDefault();
				pick(activeIndex);
				return;
			}
			if (e.key === "Escape") {
				e.preventDefault();
				setDismissedForText(text);
				return;
			}
		}
		if (suggesting) {
			if (e.key === "Tab" && !e.shiftKey && !e.ctrlKey && !e.altKey && !e.metaKey) {
				e.preventDefault();
				edit(suggestion, suggestion.length);
				return;
			}
			if (e.key === "Escape") {
				e.preventDefault();
				setDismissedSuggestion(suggestion);
				return;
			}
		}
		if (e.key !== "Enter" || e.shiftKey) return;
		e.preventDefault();
		void submit();
	};

	const startToken = (sigil: "/" | "@") => {
		if (sigil === "/") {
			edit(text.startsWith("/") ? text : `/${text}`, 1);
			return;
		}
		const at = input.current?.selectionStart ?? text.length;
		const gap = at > 0 && !/\s/.test(text[at - 1]) ? " " : "";
		edit(`${text.slice(0, at)}${gap}@${text.slice(at)}`, at + gap.length + 1);
	};

	return (
		<div className="chat-composer-wrap">
			{queued.length ? (
				<div aria-label="Queued messages" className="chat-queue" role="list">
					{queued.map((item) => (
						<div className="chat-queue__item" data-testid="chat-queued" key={item.uuid} role="listitem">
							<History className="chat-icon" aria-hidden="true" />
							<span className="chat-queue__label">Queued</span>
							<span className="chat-queue__text">{item.text}</span>
							{item.images.length > 0 ? (
								<span className="chat-queue__label">
									{item.images.length === 1 ? "1 image" : `${item.images.length} images`}
								</span>
							) : null}
						</div>
					))}
				</div>
			) : null}
			<Popover open={menu !== null}>
				<PopoverAnchor asChild>
					<div
						className="chat-composer"
						data-disabled={disabled || undefined}
						data-mode={bash ? "bash" : undefined}
						onDragOver={(e) => {
							if (e.dataTransfer.types.includes("Files")) e.preventDefault();
						}}
						onDrop={(e) => {
							if (disabled || e.dataTransfer.files.length === 0) return;
							e.preventDefault();
							attach([...e.dataTransfer.files]);
						}}
						ref={composer}
					>
						<ChatAttachments
							chips={chips}
							onRemove={(id) => setChips((prev) => prev.filter((c) => c.id !== id))}
							onRetry={retry}
						/>
						{bash ? (
							<div className="chat-composer__mode" data-testid="chat-bash-mode">
								<span className="chat-composer__mode-chip">! bash</span>
								<span
									className="chat-composer__mode-hint"
									data-blocked={block ? "" : undefined}
									id={BASH_HINT_ID}
									role={block ? "status" : undefined}
								>
									{block ? BASH_BLOCK_TEXT[block] : BASH_HINT}
								</span>
							</div>
						) : null}
						<div className="chat-composer__field" data-mode={bash ? "bash" : undefined}>
							{bash ? (
								<span aria-hidden="true" className="chat-composer__bang">
									!
								</span>
							) : null}
							<textarea
								aria-activedescendant={menu && activeIndex >= 0 ? optionId(MENU_ID, activeIndex) : undefined}
								aria-controls={menu ? MENU_ID : undefined}
								aria-describedby={bash ? BASH_HINT_ID : suggesting ? SUGGESTION_ID : undefined}
								aria-label={bash ? "Shell command" : "Message Claude"}
								className="chat-composer__input"
								disabled={disabled}
								onChange={(e) => {
									const entered = bash || chips.length > 0 ? null : bashModeEntry(text, e.target.value);
									if (entered !== null) {
										setMode("bash");
										edit(entered, entered.length);
										return;
									}
									setText(e.target.value);
									setCaret(e.target.selectionStart);
								}}
								onKeyDown={onKeyDown}
								onPaste={(e) => {
									const pasted = [...e.clipboardData.files];
									if (pasted.length === 0) return;
									e.preventDefault();
									attach(pasted);
								}}
								onSelect={(e) => setCaret(e.currentTarget.selectionStart)}
								placeholder={
									bash
										? "git status"
										: suggesting
											? ""
											: busy
												? "Message Claude. It queues until the current turn ends."
												: "Message Claude. / for commands and skills, @ for files, paste, drop or attach files."
								}
								ref={input}
								value={text}
							/>
							{suggesting ? (
								<>
									<span className="sr-only" id={SUGGESTION_ID}>
										Suggestion: {suggestion}, press Tab to use
									</span>
									<div aria-hidden="true" className="chat-composer__suggestion" data-testid="chat-suggestion">
										<span className="chat-composer__suggestion-text">{suggestion}</span>
										<span className="chat-kbd">Tab</span>
									</div>
								</>
							) : null}
							{argHint && argHint.after === text ? (
								<div aria-hidden="true" className="chat-composer__ghost" data-testid="chat-arg-hint">
									<span className="chat-composer__ghost-text">{text}</span>
									{argHint.hint}
								</div>
							) : null}
						</div>
						<div className="chat-composer__tools">
							<button
								aria-label="Attach files"
								className="chat-composer__tool"
								disabled={disabled || bash}
								onClick={() => picker.current?.click()}
								title="Attach files"
								type="button"
							>
								<Paperclip className="chat-icon" aria-hidden="true" />
							</button>
							<input
								data-testid="chat-file-input"
								disabled={disabled}
								hidden
								multiple
								onChange={(e) => {
									const picked = [...(e.target.files ?? [])];
									e.target.value = "";
									if (picked.length) attach(picked);
								}}
								ref={picker}
								tabIndex={-1}
								type="file"
							/>
							<button
								aria-label="Commands and skills"
								className="chat-composer__tool"
								disabled={disabled || bash}
								onClick={() => startToken("/")}
								title="Commands and skills"
								type="button"
							>
								<Slash className="chat-icon" aria-hidden="true" />
							</button>
							<button
								aria-label="Mention a file"
								className="chat-composer__tool"
								disabled={disabled || bash}
								onClick={() => startToken("@")}
								title="Mention a file"
								type="button"
							>
								<AtSign className="chat-icon" aria-hidden="true" />
							</button>
							<span className="chat-composer__grow" />
							<ModelModePicker
								disabled={disabled}
								chrome={chrome}
								chromeLocked={busy}
								model={model}
								models={models}
								onChrome={(enabled) => void report(actions.setChrome(enabled))}
								onMode={(mode: PermissionMode) => void report(actions.setMode(mode))}
								onModel={(value) => void report(actions.setModel(value))}
								onRemoteControl={(enabled) => void report(actions.setRemoteControl(enabled))}
								remoteControl={remoteControl}
								permissionMode={permissionMode}
							/>
							{busy ? (
								<button
									className="chat-stop"
									disabled={disabled}
									onClick={() => void report(actions.interrupt())}
									type="button"
								>
									<span aria-hidden="true" className="chat-stop__square" />
									Stop
								</button>
							) : null}
							<button
								aria-label={bash ? "Run command" : "Send"}
								className="chat-send"
								disabled={bash ? !canRun : !canSend}
								onClick={() => void (bash ? run() : submit())}
								title={
									bash
										? block
											? BASH_BLOCK_TEXT[block]
											: "Run command"
										: bashBusy
											? BASH_BLOCK_TEXT.bash
											: busy
												? "Send (queues while Claude works)"
												: "Send"
								}
								type="button"
							>
								<ArrowUp className="chat-send__icon" aria-hidden="true" />
							</button>
						</div>
					</div>
				</PopoverAnchor>
				{menu ? (
					<PopoverContent
						align="start"
						className="chat-menu-popover"
						onCloseAutoFocus={(e) => e.preventDefault()}
						onInteractOutside={(e) => {
							if (composer.current?.contains(e.target as Node)) e.preventDefault();
							else setDismissedForText(text);
						}}
						onOpenAutoFocus={(e) => e.preventDefault()}
						side="top"
						sideOffset={6}
					>
						<TokenMenu
							active={activeIndex}
							empty={menu.empty}
							id={MENU_ID}
							label={menu.label}
							onActive={setActive}
							onPick={pick}
							sections={menu.sections}
						/>
					</PopoverContent>
				) : null}
			</Popover>
		</div>
	);
}
