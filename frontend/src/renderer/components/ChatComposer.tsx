import { ArrowUp, AtSign, History, Slash, X } from "lucide-react";
import { useLayoutEffect, useMemo, useRef, useState } from "react";
import type { ChatActions } from "../hooks/useChatActions";
import { useWorkspaceFiles } from "../hooks/useWorkspaceFiles";
import type { PermissionMode } from "../lib/chat-answers";
import type { ChatImage, ChatResult } from "../lib/chat-api";
import type { ChatCommand, ChatModel, UserItem } from "../lib/chat-fold";
import { type ComposerToken, replaceToken, slashGroups, tokenAt } from "../lib/chat-menus";
import { rankFiles } from "../lib/open-quickly";
import { type MenuSection, ModelModePicker, optionId, TokenMenu } from "./ChatComposerMenus";
import { Popover, PopoverAnchor, PopoverContent } from "./ui/popover";

export type Attachment = ChatImage & { id: number; name: string };

const MENU_ID = "chat-token-menu";
const MAX_FILES = 8;

function readImage(file: File, id: number): Promise<Attachment> {
	return new Promise((resolve, reject) => {
		const reader = new FileReader();
		reader.onload = () => {
			const url = String(reader.result);
			resolve({ id, name: file.name || "pasted image", mediaType: file.type, base64: url.slice(url.indexOf(",") + 1) });
		};
		reader.onerror = () => reject(reader.error ?? new Error(`could not read ${file.name}`));
		reader.readAsDataURL(file);
	});
}

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

/** Text another part of the chat wants in the box. A new `seq` is a new request, even for the same text. */
export type Prefill = { text: string; seq: number };

type ComposerProps = {
	sessionId: string;
	actions: ChatActions;
	busy: boolean;
	queued: readonly UserItem[];
	permissionMode: string;
	commands: readonly ChatCommand[];
	models: readonly ChatModel[];
	model: string;
	onToast: (message: string) => void;
	prefill?: Prefill;
};

export function ChatComposer({
	sessionId,
	actions,
	busy,
	queued,
	permissionMode,
	commands,
	models,
	model,
	onToast,
	prefill,
}: ComposerProps) {
	const [text, setText] = useState("");
	const [caret, setCaret] = useState(0);
	const [images, setImages] = useState<Attachment[]>([]);
	const [sending, setSending] = useState(false);
	const [dismissedForText, setDismissedForText] = useState<string | null>(null);
	const [active, setActive] = useState(0);
	const [argHint, setArgHint] = useState<{ after: string; hint: string } | null>(null);
	const [wantFiles, setWantFiles] = useState(false);
	const nextId = useRef(0);
	const input = useRef<HTMLTextAreaElement>(null);
	const composer = useRef<HTMLDivElement>(null);
	const pendingCaret = useRef<number | null>(null);
	const disabled = actions.hostDown;
	const canSend = !disabled && !sending && text.trim() !== "";

	const token: ComposerToken | null = disabled || dismissedForText === text ? null : tokenAt(text, caret);
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

	// Fills the box and never sends. A draft already there keeps its text and the prefill goes below it.
	const seenPrefill = useRef(prefill?.seq);
	useLayoutEffect(() => {
		if (!prefill || prefill.seq === seenPrefill.current) return;
		seenPrefill.current = prefill.seq;
		const next = text.trim() === "" ? prefill.text : `${text}\n\n${prefill.text}`;
		edit(next, next.length);
	});

	const pick = (index: number) => {
		const choice = menu?.choices[index];
		if (!choice || !token) return;
		const next = replaceToken(text, token, choice.insert);
		edit(next.text, next.caret);
		setArgHint(choice.hint ? { after: next.text, hint: choice.hint } : null);
	};

	const attach = (files: readonly File[]) => {
		const refused = files.filter((f) => !f.type.startsWith("image/"));
		if (refused.length) {
			onToast(`Only images can be attached. Skipped ${refused.map((f) => f.name || "a file").join(", ")}.`);
		}
		const reads = files.filter((f) => f.type.startsWith("image/")).map((f) => readImage(f, ++nextId.current));
		void Promise.allSettled(reads).then((settled) => {
			const read = settled.flatMap((s) => (s.status === "fulfilled" ? [s.value] : []));
			if (read.length) setImages((prev) => [...prev, ...read]);
			if (read.length < reads.length) onToast("Could not read an image.");
		});
	};

	const submit = async () => {
		if (!canSend) return;
		setSending(true);
		const result = await actions.send(
			text,
			images.map(({ mediaType, base64 }) => ({ mediaType, base64 })),
		);
		setSending(false);
		if (result.ok) {
			setText("");
			setCaret(0);
			setImages([]);
			setArgHint(null);
		} else if (result.error.kind !== "host_not_running") {
			onToast(result.error.message);
		}
	};

	const report = async (call: Promise<ChatResult>) => {
		const result = await call;
		if (!result.ok && result.error.kind !== "host_not_running") onToast(result.error.message);
	};

	const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
		if (e.nativeEvent.isComposing) return;
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
						{images.length ? (
							<div className="chat-attach">
								{images.map((img) => (
									<span className="chat-attach__item" data-testid="chat-attachment" key={img.id}>
										<img
											alt={img.name}
											className="chat-attach__thumb"
											src={`data:${img.mediaType};base64,${img.base64}`}
										/>
										<span className="chat-attach__name">{img.name}</span>
										<button
											aria-label={`Remove ${img.name}`}
											className="chat-attach__remove"
											onClick={() => setImages((prev) => prev.filter((i) => i.id !== img.id))}
											type="button"
										>
											<X className="chat-icon" aria-hidden="true" />
										</button>
									</span>
								))}
							</div>
						) : null}
						<div className="chat-composer__field">
							<textarea
								aria-activedescendant={menu && activeIndex >= 0 ? optionId(MENU_ID, activeIndex) : undefined}
								aria-controls={menu ? MENU_ID : undefined}
								aria-label="Message Claude"
								className="chat-composer__input"
								disabled={disabled}
								onChange={(e) => {
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
									busy
										? "Message Claude. It queues until the current turn ends."
										: "Message Claude. / for commands and skills, @ for files, paste or drop images."
								}
								ref={input}
								value={text}
							/>
							{argHint && argHint.after === text ? (
								<div aria-hidden="true" className="chat-composer__ghost" data-testid="chat-arg-hint">
									<span className="chat-composer__ghost-text">{text}</span>
									{argHint.hint}
								</div>
							) : null}
						</div>
						<div className="chat-composer__tools">
							<button
								aria-label="Commands and skills"
								className="chat-composer__tool"
								disabled={disabled}
								onClick={() => startToken("/")}
								title="Commands and skills"
								type="button"
							>
								<Slash className="chat-icon" aria-hidden="true" />
							</button>
							<button
								aria-label="Mention a file"
								className="chat-composer__tool"
								disabled={disabled}
								onClick={() => startToken("@")}
								title="Mention a file"
								type="button"
							>
								<AtSign className="chat-icon" aria-hidden="true" />
							</button>
							<span className="chat-composer__grow" />
							<ModelModePicker
								disabled={disabled}
								model={model}
								models={models}
								onMode={(mode: PermissionMode) => void report(actions.setMode(mode))}
								onModel={(value) => void report(actions.setModel(value))}
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
								aria-label="Send"
								className="chat-send"
								disabled={!canSend}
								onClick={() => void submit()}
								title={busy ? "Send (queues while Claude works)" : "Send"}
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
