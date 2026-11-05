import { RotateCcw } from "lucide-react";
import { createContext, type ReactNode, useContext, useId, useState } from "react";
import type { ChatResult, RewindPreview } from "../lib/chat-api";
import { shortenPaths } from "../lib/chat-tools";
import type { UserItem } from "../lib/chat-fold";
import { Button } from "./ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "./ui/popover";
import { Switch } from "./ui/switch";

export type RewindControl = {
	rewind: (uuid: string, restoreFiles: boolean) => Promise<ChatResult>;
	preview: (uuid: string) => Promise<RewindPreview | null>;
	/** Called once claude has cut the conversation, with the message that left it. */
	onRewound: (item: UserItem) => void;
	onError: (message: string) => void;
	/** False while a turn runs or the host is down: claude refuses a rewind then. */
	ready: boolean;
	root: string;
};

const RewindContext = createContext<RewindControl | null>(null);

export function ChatRewindProvider({ value, children }: { value: RewindControl; children: ReactNode }) {
	return <RewindContext.Provider value={value}>{children}</RewindContext.Provider>;
}

type Files = { phase: "off" } | { phase: "loading" } | { phase: "shown"; preview: RewindPreview | null };

const MAX_FILES = 8;

export function RewindButton({ item }: { item: UserItem }) {
	const control = useContext(RewindContext);
	const [open, setOpen] = useState(false);
	const [files, setFiles] = useState<Files>({ phase: "off" });
	const [sending, setSending] = useState(false);
	const switchId = useId();
	if (!control || !control.ready || item.uuid === "") return null;

	const restore = files.phase !== "off";
	const preview = files.phase === "shown" ? files.preview : null;
	const blocked = files.phase === "loading" || (files.phase === "shown" && !preview?.canRewind);

	const toggleFiles = (on: boolean) => {
		if (!on) return setFiles({ phase: "off" });
		setFiles({ phase: "loading" });
		void control
			.preview(item.uuid)
			.then((p) => setFiles((f) => (f.phase === "off" ? f : { phase: "shown", preview: p })));
	};

	const confirm = () => {
		setSending(true);
		void control.rewind(item.uuid, restore).then((result) => {
			setSending(false);
			if (!result.ok) return control.onError(result.error.message);
			setOpen(false);
			control.onRewound(item);
		});
	};

	return (
		<Popover
			open={open}
			onOpenChange={(next) => {
				setOpen(next);
				if (!next) setFiles({ phase: "off" });
			}}
		>
			<PopoverTrigger asChild>
				<button aria-label="Rewind to here" className="chat-rewind__trigger" title="Rewind to here" type="button">
					<RotateCcw aria-hidden="true" />
				</button>
			</PopoverTrigger>
			<PopoverContent align="end" className="chat-rewind w-80 p-3">
				<div className="chat-rewind__title">Rewind the conversation to here</div>
				<p className="chat-rewind__body">
					This message and everything after it leave the conversation. Its text goes back into the composer.
				</p>
				<div className="chat-rewind__files">
					<label className="chat-rewind__label" htmlFor={switchId}>
						Restore files to this point
					</label>
					<Switch checked={restore} id={switchId} onCheckedChange={toggleFiles} />
				</div>
				{files.phase === "loading" ? <p className="chat-rewind__note">Checking what would change…</p> : null}
				{files.phase === "shown" ? <FileList preview={preview} root={control.root} /> : null}
				<div className="chat-rewind__actions">
					<Button onClick={() => setOpen(false)} size="sm" variant="ghost">
						Cancel
					</Button>
					<Button disabled={sending || blocked} onClick={confirm} size="sm">
						Rewind
					</Button>
				</div>
			</PopoverContent>
		</Popover>
	);
}

function FileList({ preview, root }: { preview: RewindPreview | null; root: string }) {
	if (!preview) return <p className="chat-rewind__note">Could not check the files. Try again.</p>;
	if (!preview.canRewind)
		return <p className="chat-rewind__note">{preview.error || "No file checkpoint for this message."}</p>;
	if (preview.filesChanged.length === 0)
		return <p className="chat-rewind__note">No files changed since this message.</p>;
	const shown = preview.filesChanged.slice(0, MAX_FILES);
	const more = preview.filesChanged.length - shown.length;
	return (
		<div className="chat-rewind__list">
			<div className="chat-rewind__stat">
				{preview.filesChanged.length} {preview.filesChanged.length === 1 ? "file" : "files"} ·{" "}
				<span className="chat-rewind__plus">+{preview.insertions}</span>{" "}
				<span className="chat-rewind__minus">−{preview.deletions}</span>
			</div>
			<ul>
				{shown.map((path) => (
					<li key={path}>{shortenPaths(path, root)}</li>
				))}
				{more > 0 ? <li className="chat-rewind__more">and {more} more</li> : null}
			</ul>
		</div>
	);
}
