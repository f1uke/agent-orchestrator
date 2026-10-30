import { ArrowUp, ChevronDown, History, ShieldCheck, X } from "lucide-react";
import { useRef, useState } from "react";
import type { ChatActions } from "../hooks/useChatActions";
import type { PermissionMode } from "../lib/chat-answers";
import type { ChatImage } from "../lib/chat-api";
import type { UserItem } from "../lib/chat-fold";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuLabel,
	DropdownMenuRadioGroup,
	DropdownMenuRadioItem,
	DropdownMenuTrigger,
} from "./ui/dropdown-menu";

export type Attachment = ChatImage & { id: number; name: string };

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

export const modeLabel = (mode: string) => MODE_LABEL[mode] ?? (mode || "Mode");

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

type ComposerProps = {
	actions: ChatActions;
	busy: boolean;
	queued: readonly UserItem[];
	permissionMode: string;
	onToast: (message: string) => void;
};

export function ChatComposer({ actions, busy, queued, permissionMode, onToast }: ComposerProps) {
	const [text, setText] = useState("");
	const [images, setImages] = useState<Attachment[]>([]);
	const [sending, setSending] = useState(false);
	const nextId = useRef(0);
	const disabled = actions.hostDown;
	const canSend = !disabled && !sending && text.trim() !== "";

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
			setImages([]);
		} else if (result.error.kind !== "host_not_running") {
			onToast(result.error.message);
		}
	};

	const stop = async () => {
		const result = await actions.interrupt();
		if (!result.ok && result.error.kind !== "host_not_running") onToast(result.error.message);
	};

	const changeMode = async (mode: string) => {
		const result = await actions.setMode(mode as PermissionMode);
		if (!result.ok && result.error.kind !== "host_not_running") onToast(result.error.message);
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
							{item.images > 0 ? (
								<span className="chat-queue__label">{item.images === 1 ? "1 image" : `${item.images} images`}</span>
							) : null}
						</div>
					))}
				</div>
			) : null}
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
			>
				{images.length ? (
					<div className="chat-attach">
						{images.map((img) => (
							<span className="chat-attach__item" data-testid="chat-attachment" key={img.id}>
								<img alt={img.name} className="chat-attach__thumb" src={`data:${img.mediaType};base64,${img.base64}`} />
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
				<textarea
					aria-label="Message Claude"
					className="chat-composer__input"
					disabled={disabled}
					onChange={(e) => setText(e.target.value)}
					onKeyDown={(e) => {
						if (e.key !== "Enter" || e.shiftKey || e.nativeEvent.isComposing) return;
						e.preventDefault();
						void submit();
					}}
					onPaste={(e) => {
						const files = [...e.clipboardData.files];
						if (files.length === 0) return;
						e.preventDefault();
						attach(files);
					}}
					placeholder={
						busy ? "Message Claude. It queues until the current turn ends." : "Message Claude. Paste or drop images."
					}
					value={text}
				/>
				<div className="chat-composer__tools">
					<span className="chat-composer__grow" />
					<DropdownMenu>
						<DropdownMenuTrigger asChild disabled={disabled}>
							<button
								aria-label={`Permission mode: ${modeLabel(permissionMode)}`}
								className="chat-picker"
								type="button"
							>
								<ShieldCheck className="chat-icon" aria-hidden="true" />
								{modeLabel(permissionMode)}
								<ChevronDown className="chat-icon" aria-hidden="true" />
							</button>
						</DropdownMenuTrigger>
						<DropdownMenuContent align="end" className="w-56" side="top">
							<DropdownMenuLabel>Mode</DropdownMenuLabel>
							<DropdownMenuRadioGroup onValueChange={(mode) => void changeMode(mode)} value={permissionMode}>
								{MODES.map(({ mode, label }) => (
									<DropdownMenuRadioItem key={mode} value={mode}>
										{label}
									</DropdownMenuRadioItem>
								))}
							</DropdownMenuRadioGroup>
						</DropdownMenuContent>
					</DropdownMenu>
					{busy ? (
						<button className="chat-stop" disabled={disabled} onClick={() => void stop()} type="button">
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
		</div>
	);
}
