import { useEffect, useRef, useState } from "react";
import { Check, Copy } from "lucide-react";
import { aoBridge } from "../lib/bridge";
import { cn } from "../lib/utils";

/** How long the icon stays a check after a successful copy. */
export const COPIED_FEEDBACK_MS = 1200;

/**
 * Click-to-copy, one idiom used everywhere something has to be pasted
 * somewhere else.
 *
 * It started life beside a session's `@id` line in the sidebar, where the value
 * being saved was the CLI argument somebody would otherwise retype and get
 * wrong. It is the same job wherever a value has to leave the app: the path of
 * a recorded flow, pasted into a message to a worker; the flow's bare name,
 * written in prose. Extracted rather than copied, so all of them keep the same
 * feedback, the same accessible-name pattern and the same "copying must not
 * also navigate" behaviour.
 *
 * Three things are load-bearing and were paid for by real bugs:
 *
 * The click MUST NOT bubble. Every place this sits is inside something else
 * that is clickable - a row that opens a session, a row that selects a
 * recording - and copying a value that also navigated away would be a worse
 * papercut than the retyping it saves. The pointerdown is stopped too, because
 * the sidebar reads one as the start of a split-drag.
 *
 * The glyph swap reuses the same box, and the control reserves its space at all
 * times, so revealing or confirming can never resize what it sits in.
 *
 * The hover tint is dropped while confirming: a `hover:` variant always
 * outranks a base utility in Tailwind, and the pointer is by definition still
 * on the button right after a click - so keeping it would repaint the green
 * check plain foreground exactly when it is meant to be read.
 */
export function CopyButton({
	value,
	/** What is being copied, as a reader hears it: "session id", "path", "file name". */
	what,
	className,
	/** Extra classes while confirming, for a caller whose reveal rules differ. */
	copiedClassName,
}: {
	value: string;
	what: string;
	className?: string;
	copiedClassName?: string;
	size?: number;
}) {
	const [copied, setCopied] = useState(false);
	const resetTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

	useEffect(() => () => clearTimeout(resetTimer.current), []);

	const copy = (event: React.MouseEvent) => {
		event.stopPropagation();
		void aoBridge.clipboard
			.writeText(value)
			.then(() => {
				setCopied(true);
				clearTimeout(resetTimer.current);
				resetTimer.current = setTimeout(() => setCopied(false), COPIED_FEEDBACK_MS);
			})
			.catch((error) => {
				console.warn(`Unable to copy ${what}`, error);
			});
	};

	const label = copied ? `Copied ${what} ${value}` : `Copy ${what} ${value}`;
	return (
		<button
			aria-label={label}
			className={cn(
				// `before:` widens the hit area beyond the glyph without adding
				// layout width, so a small icon is still comfortably clickable.
				// The box is sized here, in a class rather than a style, so the
				// glyph swap provably reuses it - a caller can widen it, and a
				// test can see that confirming did not change it.
				"relative grid size-[13px] shrink-0 place-items-center rounded-[3px]",
				"before:absolute before:-inset-1 before:content-['']",
				"text-muted-foreground transition-[opacity,color]",
				"focus-visible:ring-1 focus-visible:ring-ring focus-visible:outline-none",
				copied ? cn("text-success opacity-100", copiedClassName) : "hover:text-foreground",
				className,
			)}
			onClick={copy}
			onPointerDown={(event) => event.stopPropagation()}
			title={label}
			type="button"
		>
			{copied ? <Check className="size-3" aria-hidden="true" /> : <Copy className="size-3" aria-hidden="true" />}
		</button>
	);
}
