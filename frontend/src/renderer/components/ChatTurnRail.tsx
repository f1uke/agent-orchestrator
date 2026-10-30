import { type KeyboardEvent, useCallback, useId, useLayoutEffect, useRef, useState } from "react";
import type { RailTurn } from "../lib/chat-rail";
import { cn } from "../lib/utils";

const FADE_PX = 20;

type Preview = { index: number; top: number };

const NAV_KEYS: Record<string, (i: number, last: number) => number> = {
	ArrowUp: (i) => i - 1,
	ArrowDown: (i) => i + 1,
	Home: () => 0,
	End: (_, last) => last,
};

export function ChatTurnRail({
	turns,
	active,
	onJump,
}: {
	turns: readonly RailTurn[];
	active: number;
	onJump: (key: string) => void;
}) {
	const ladderRef = useRef<HTMLDivElement | null>(null);
	const pointerInside = useRef(false);
	const [focused, setFocused] = useState<number | null>(null);
	const [preview, setPreview] = useState<Preview | null>(null);
	const [fades, setFades] = useState({ top: false, bottom: false });
	const cardId = useId();

	const tickAt = (index: number) => ladderRef.current?.children[index] as HTMLElement | undefined;

	const previewAt = useCallback((index: number): Preview => {
		const ladder = ladderRef.current;
		const tick = ladder?.children[index] as HTMLElement | undefined;
		const top = tick && ladder ? tick.offsetTop - ladder.scrollTop + tick.offsetHeight / 2 + ladder.offsetTop : 0;
		return { index, top };
	}, []);

	const onLadderScroll = useCallback(() => {
		const ladder = ladderRef.current;
		if (!ladder) return;
		const top = ladder.scrollTop > 1;
		const bottom = ladder.scrollTop < ladder.scrollHeight - ladder.clientHeight - 1;
		setFades((prev) => (prev.top === top && prev.bottom === bottom ? prev : { top, bottom }));
		setPreview((prev) => prev && previewAt(prev.index));
	}, [previewAt]);

	useLayoutEffect(() => {
		const ladder = ladderRef.current;
		const tick = ladder?.children[active] as HTMLElement | undefined;
		if (ladder && tick && !pointerInside.current) {
			const top = tick.offsetTop;
			const bottom = top + tick.offsetHeight;
			if (top < ladder.scrollTop + FADE_PX || bottom > ladder.scrollTop + ladder.clientHeight - FADE_PX) {
				ladder.scrollTop = top - (ladder.clientHeight - tick.offsetHeight) / 2;
			}
		}
		onLadderScroll();
	}, [active, turns.length, onLadderScroll]);

	if (turns.length < 2) return null;

	const stop = focused ?? active;
	const onKeyDown = (e: KeyboardEvent<HTMLButtonElement>, index: number) => {
		const next = NAV_KEYS[e.key];
		if (!next) return;
		e.preventDefault();
		tickAt(Math.max(0, Math.min(turns.length - 1, next(index, turns.length - 1))))?.focus();
	};

	const shown = preview ? turns[preview.index] : undefined;
	return (
		<nav
			aria-label="Turns"
			className="chat-rail"
			onPointerEnter={() => (pointerInside.current = true)}
			onPointerLeave={() => {
				pointerInside.current = false;
				setPreview(focused === null ? null : previewAt(focused));
			}}
		>
			<div
				className={cn(
					"chat-rail__ladder",
					fades.top && "chat-rail__ladder--fade-top",
					fades.bottom && "chat-rail__ladder--fade-bottom",
				)}
				onScroll={onLadderScroll}
				ref={ladderRef}
			>
				{turns.map((turn, index) => (
					<button
						aria-current={index === active ? "true" : undefined}
						aria-describedby={preview?.index === index ? cardId : undefined}
						aria-label={`Turn ${turn.turn}: ${turn.prompt}`}
						className="chat-rail__tick"
						key={turn.key}
						onBlur={() => {
							setFocused(null);
							setPreview(null);
						}}
						onClick={() => onJump(turn.key)}
						onFocus={() => {
							setFocused(index);
							setPreview(previewAt(index));
						}}
						onKeyDown={(e) => onKeyDown(e, index)}
						onPointerEnter={() => setPreview(previewAt(index))}
						tabIndex={index === stop ? 0 : -1}
						type="button"
					/>
				))}
			</div>
			{shown && preview ? (
				<div className="chat-rail__card" id={cardId} role="tooltip" style={{ top: preview.top }}>
					<div className="chat-rail__turn">Turn {shown.turn}</div>
					<div className="chat-rail__prompt">{shown.prompt}</div>
					{shown.reply ? <div className="chat-rail__reply">{shown.reply}</div> : null}
				</div>
			) : null}
		</nav>
	);
}
