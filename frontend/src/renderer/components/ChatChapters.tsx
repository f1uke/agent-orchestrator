import { List } from "lucide-react";
import { type FocusEvent, type KeyboardEvent, useRef, useState } from "react";
import type { Chapter } from "../lib/chat-chapters";

export function ChatChapters({
	chapters,
	active,
	onJump,
}: {
	chapters: readonly Chapter[];
	active: number;
	onJump: (key: string) => void;
}) {
	const [open, setOpen] = useState(false);
	const button = useRef<HTMLButtonElement | null>(null);
	if (chapters.length < 2) return null;

	const close = () => {
		setOpen(false);
		button.current?.focus();
	};
	const onKeyDown = (e: KeyboardEvent) => {
		if (e.key !== "Escape" || !open) return;
		e.stopPropagation();
		close();
	};
	const onBlur = (e: FocusEvent<HTMLElement>) => {
		if (!e.currentTarget.contains(e.relatedTarget)) setOpen(false);
	};

	return (
		<nav aria-label="Chapters" className="chat-chapters" onBlur={onBlur} onKeyDown={onKeyDown}>
			<button
				aria-expanded={open}
				className="chat-chapters__toggle"
				onClick={() => setOpen(!open)}
				ref={button}
				type="button"
			>
				<List aria-hidden="true" className="chat-icon" />
				<span className="chat-chapters__current">{chapters[active]?.title ?? "Chapters"}</span>
			</button>
			{open ? (
				<ul className="chat-chapters__list">
					{chapters.map((chapter, i) => (
						<li key={chapter.key}>
							<button
								aria-current={i === active ? "true" : undefined}
								className="chat-chapters__item"
								onClick={() => {
									onJump(chapter.key);
									close();
								}}
								title={chapter.summary || undefined}
								type="button"
							>
								{chapter.title}
							</button>
						</li>
					))}
				</ul>
			) : null}
		</nav>
	);
}
