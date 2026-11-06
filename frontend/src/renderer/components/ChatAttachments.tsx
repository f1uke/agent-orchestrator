import { Loader2, RotateCw, X } from "lucide-react";
import { attachTag } from "../lib/chat-attach";
import { sizeText } from "../lib/chat-docs";
import type { InlineImage } from "../lib/chat-images";

export type Chip =
	| { kind: "image"; id: number; name: string; image: InlineImage }
	| { kind: "file"; id: number; name: string; size: number; file: File; upload: Upload };

export type Upload = { phase: "uploading" } | { phase: "ready"; path: string } | { phase: "failed"; message: string };

type Props = {
	chips: readonly Chip[];
	onRemove: (id: number) => void;
	onRetry: (id: number) => void;
};

export function ChatAttachments({ chips, onRemove, onRetry }: Props) {
	if (chips.length === 0) return null;
	return (
		<div className="chat-attach">
			{chips.map((chip) => {
				const state = chip.kind === "image" ? "ready" : chip.upload.phase;
				return (
					<span
						aria-label={`Attachment ${chip.name}`}
						className="chat-attach__item"
						data-state={state}
						data-testid="chat-attachment"
						key={chip.id}
						role="group"
					>
						{chip.kind === "image" ? (
							<img
								alt={chip.name}
								className="chat-attach__thumb"
								src={`data:${chip.image.mediaType};base64,${chip.image.base64}`}
							/>
						) : (
							<span className="chat-attach__tag">{attachTag(chip.name)}</span>
						)}
						<span className="chat-attach__text">
							<span className="chat-attach__name">{chip.name}</span>
							{chip.kind === "file" ? (
								<span className="chat-attach__meta">
									{chip.upload.phase === "failed" ? chip.upload.message : sizeText(chip.size)}
								</span>
							) : null}
						</span>
						{chip.kind === "file" && chip.upload.phase === "uploading" ? (
							<Loader2 aria-label="Uploading" className="chat-icon animate-spin" role="status" />
						) : null}
						{chip.kind === "file" && chip.upload.phase === "failed" ? (
							<button
								aria-label={`Retry ${chip.name}`}
								className="chat-attach__retry"
								onClick={() => onRetry(chip.id)}
								type="button"
							>
								<RotateCw className="chat-icon" aria-hidden="true" />
								Retry
							</button>
						) : null}
						<button
							aria-label={`Remove ${chip.name}`}
							className="chat-attach__remove"
							onClick={() => onRemove(chip.id)}
							type="button"
						>
							<X className="chat-icon" aria-hidden="true" />
						</button>
					</span>
				);
			})}
		</div>
	);
}
