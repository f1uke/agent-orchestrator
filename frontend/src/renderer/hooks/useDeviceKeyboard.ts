import { useCallback, useEffect, useRef, useState } from "react";

/**
 * The human's own keyboard, into the device.
 *
 * Until this existed the tab forwarded pointer input and nothing else, so you
 * could tap a field, watch the caret appear, type - and nothing arrived. The
 * only way in was `ao sim type` from a terminal while looking at the tab: the
 * same shape of gap as recording had, where the person driving by hand is
 * pushed out to the CLI for one step in the middle.
 *
 * ## How a keypress becomes a character on the device
 *
 * 🗝 **The character comes from `event.key`, never from a key code.** That is
 * the whole reason this surface can be correct where a synthesised keystroke
 * cannot. The browser has already resolved the human's input source for us: on
 * a Thai Mac, pressing the key labelled `f` gives `event.key === "ด"` - the
 * character they meant. Sending a US HID usage for `f` instead is exactly the
 * bug that made `ao sim type "fa12345"` arrive as `ดฟๅ/_ภถ` while reporting
 * success, because the GUEST remaps usages according to its own input mode.
 *
 * So characters are sent as TEXT to the daemon's `type` gesture, which already
 * knows how to deliver text truthfully - key presses when the guest will
 * deliver US ASCII faithfully, the guest pasteboard when it would remap or the
 * text is not ASCII, and it proves on screen that the text landed. There is no
 * second implementation of "is this keyboard safe" here, which is deliberate:
 * two of those would disagree one day and the layout bug would come back on
 * whichever surface had the stale copy.
 *
 * Keys that produce NO character - Enter, Backspace, Tab, the arrows - go the
 * other way, as named key presses, because there is nothing for a layout to
 * remap them into and text is the wrong shape for them anyway: a flow that
 * turned Enter into a newline would submit nothing.
 *
 * ## Why typing is paced by the route it will take
 *
 * 🗝 A person types by watching characters land, so text that arrives correctly
 * after a pause still reads as broken - they retype, or tap around thinking the
 * field lost focus, and a correct system produces a wrong result. So a
 * character goes out AT ONCE wherever that is cheap, and is batched only where
 * batching is what makes it correct.
 *
 * Which one applies is the device's answer, not this hook's opinion:
 *
 *   - the guest reads US ASCII key presses faithfully (`immediate`), and the
 *     character is ASCII: sent on its own, measured at 3-6 ms on the device.
 *   - anything else - a guest that would remap the keys, one that would not say
 *     what it is, or a character no US key can send (Thai, an emoji): the
 *     characters accumulate and go out as one `type` per burst. Each of those
 *     is a pasteboard round trip that reads the screen twice to prove it
 *     landed, measured at 3.1-3.4 s: one per keystroke would be far worse than
 *     the pause it was meant to remove, and would cycle the guest's pasteboard
 *     on every character.
 *
 * ⚠ `immediate` is a PACING hint and never a routing decision. The daemon plans
 * every request itself, from the mode as it is at that moment, so a hint that
 * has gone stale costs speed and never correctness - which is what keeps
 * "is this keyboard safe" implemented in exactly one place, and not here.
 *
 * A burst ends when the human pauses, presses a key that is not a character,
 * leaves the surface, or stops driving.
 *
 * ⚠ Ordering is the thing that must not break. A named key FLUSHES the pending
 * text first and is queued behind it, so `ab<Backspace>c` cannot arrive as
 * `abc` with a stray backspace at the end. Every send goes through one queue
 * for the same reason.
 */

/** How long a pause ends a burst of typing. */
export const TYPING_FLUSH_MS = 250;

/**
 * Send at least this often regardless of pauses. It bounds how much text can
 * be waiting when something goes wrong, and keeps one `type` well inside the
 * daemon's own 2000-rune limit.
 */
export const MAX_PENDING_RUNES = 200;

/** What the device understands, keyed by what the browser reports. */
const NAMED_KEYS: Record<string, string> = {
	Enter: "enter",
	Backspace: "backspace",
	Tab: "tab",
	ArrowUp: "arrow-up",
	ArrowDown: "arrow-down",
	ArrowLeft: "arrow-left",
	ArrowRight: "arrow-right",
};

export type DeviceKey = { kind: "text"; text: string } | { kind: "key"; name: string };

/**
 * classifyKey decides what a keypress means to the device, or that it means
 * nothing and must be left alone.
 *
 * ⚠ The modifier rule is the one that keeps AO usable. A keypress with
 * Command, Control or Option held is a shortcut - ⌘W, ⌘K, ⌘, - and is never
 * touched here, so the app around this pane keeps every binding it has. It
 * also means there is no way to send ⌘V to the device from here, which is
 * fine: typing is the way in, and the pasteboard route is the daemon's
 * business rather than something a human should be driving by hand.
 *
 * Shift is not in that list on purpose: it is how a capital letter is typed,
 * and `event.key` already carries the result.
 */
export function classifyKey(event: {
	key: string;
	metaKey: boolean;
	ctrlKey: boolean;
	altKey: boolean;
}): DeviceKey | null {
	if (event.metaKey || event.ctrlKey || event.altKey) return null;

	const named = NAMED_KEYS[event.key];
	if (named) return { kind: "key", name: named };

	// One character, whatever the input source produced. Spread rather than
	// `.length`, so a character outside the basic plane counts as one and an
	// event like "ArrowUp" or "Dead" - which are names, not characters - does
	// not look like a five-letter word.
	if ([...event.key].length === 1) return { kind: "text", text: event.key };

	// ⚠ Everything else is deliberately NOT forwarded and NOT prevented: F-keys,
	// Home, Page Up, a bare Shift, and "Dead" - the first half of a composed
	// character on an input source that composes. Direct input sources (Thai,
	// Latin, Cyrillic) never produce "Dead" for ordinary text; an IME with a
	// candidate window (Chinese, Japanese) does not deliver its result through
	// keydown at all, so that input source is not supported here and is stated
	// rather than half-handled.
	return null;
}

/**
 * How long text may be on its way before the pane admits it is waiting.
 *
 * ⚠ Not a delay on anything - it only decides when the human is TOLD. Below
 * this, saying so would be a flicker on every keystroke of ordinary fast
 * typing, which is noise rather than information; above it, the human is in the
 * gap where they would otherwise start retyping into a field that is about to
 * receive what they already typed.
 */
export const TYPING_WAIT_VISIBLE_MS = 200;

/**
 * Whether a character can be sent on its own cheaply, given a guest that reads
 * US ASCII key presses faithfully.
 *
 * ⚠ This is not "can the device type this" - that question belongs to the
 * daemon and is answered there. It is only "would sending this alone be cheap",
 * and it is deliberately conservative: anything outside printable ASCII takes
 * the pasteboard route, so it is batched instead.
 */
function sendsAlone(text: string): boolean {
	for (const rune of text) {
		const code = rune.codePointAt(0) ?? 0;
		if (code < 0x20 || code > 0x7e) return false;
	}
	return true;
}

export type DeviceKeyboard = {
	/** Wire to onKeyDown of the surface that has focus. */
	onKeyDown: (event: React.KeyboardEvent) => void;
	/** Wire to onBlur: leaving the surface must not strand pending text. */
	flush: () => void;
	/**
	 * Something typed has not reached the device yet, and has been on its way
	 * long enough to be worth saying so. Never what was typed - only that.
	 */
	waiting: boolean;
};

export function useDeviceKeyboard({
	enabled,
	immediate = false,
	sendText,
	sendKey,
	onEscape,
}: {
	/** Typing reaches the device only while this is true. */
	enabled: boolean;
	/**
	 * The device reads US ASCII key presses as the characters they were sent
	 * as, so an ASCII character can go out on its own. A pacing hint from the
	 * daemon; see the note above about why it is never a routing decision.
	 * Defaults to false, so a pane that has not been told yet batches - the
	 * slower answer is always the safe one to guess.
	 */
	immediate?: boolean;
	sendText: (text: string) => Promise<void>;
	sendKey: (name: string) => Promise<void>;
	/** Escape is the way out of the surface, and is never sent to the device. */
	onEscape: () => void;
}): DeviceKeyboard {
	const pending = useRef("");
	const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
	// One queue, so text and keys reach the device in the order they were
	// typed even though each send is a separate request.
	const queue = useRef<Promise<void>>(Promise.resolve());
	const sendTextRef = useRef(sendText);
	const sendKeyRef = useRef(sendKey);
	sendTextRef.current = sendText;
	sendKeyRef.current = sendKey;

	// How many typed characters have been accepted and not yet reached the
	// device - buffered here, queued, or in flight. Counted rather than a flag,
	// because several sends can be outstanding at once when typing is immediate.
	const unconfirmed = useRef(0);
	const waitTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
	const [waiting, setWaiting] = useState(false);

	const accepted = useCallback((count: number) => {
		unconfirmed.current += count;
		if (waitTimer.current === undefined) {
			waitTimer.current = setTimeout(() => setWaiting(true), TYPING_WAIT_VISIBLE_MS);
		}
	}, []);

	const confirmed = useCallback((count: number) => {
		unconfirmed.current = Math.max(0, unconfirmed.current - count);
		if (unconfirmed.current > 0) return;
		clearTimeout(waitTimer.current);
		waitTimer.current = undefined;
		setWaiting(false);
	}, []);

	const enqueue = useCallback((run: () => Promise<void>) => {
		queue.current = queue.current.then(run, run);
	}, []);

	const flush = useCallback(() => {
		clearTimeout(timer.current);
		timer.current = undefined;
		const text = pending.current;
		pending.current = "";
		if (text === "") return;
		const runes = [...text].length;
		// settled, not resolved: text that failed to reach the device is no
		// longer on its way either, and leaving the pane claiming it was would
		// be a spinner that never stops. The failure itself is reported by the
		// caller's own error handling.
		enqueue(() => sendTextRef.current(text).finally(() => confirmed(runes)));
	}, [confirmed, enqueue]);

	// Nothing typed may be left behind when typing stops being allowed - the
	// lease going away, driving switched off, the tab closing.
	useEffect(() => {
		if (!enabled) flush();
	}, [enabled, flush]);
	useEffect(
		() => () => {
			clearTimeout(timer.current);
			clearTimeout(waitTimer.current);
		},
		[],
	);

	const onKeyDown = useCallback(
		(event: React.KeyboardEvent) => {
			if (event.key === "Escape") {
				// Not forwarded. It is the way back out of the surface, and a
				// keyboard trap with no exit is a worse bug than the one this
				// whole hook fixes.
				flush();
				onEscape();
				return;
			}
			if (!enabled) return;

			const what = classifyKey(event);
			if (!what) return;
			// Only now: a key this pane does not forward keeps its ordinary
			// meaning in the app around it.
			event.preventDefault();

			if (what.kind === "key") {
				flush();
				enqueue(() => sendKeyRef.current(what.name));
				return;
			}

			pending.current += what.text;
			accepted(1);
			// The character can stand on its own, and the device will read it as
			// itself: send it now rather than making the human wait for a pause
			// they have no reason to take. Anything already pending goes with it,
			// so nothing can overtake what was typed before it.
			if (immediate && sendsAlone(pending.current)) {
				flush();
				return;
			}
			if ([...pending.current].length >= MAX_PENDING_RUNES) {
				flush();
				return;
			}
			clearTimeout(timer.current);
			timer.current = setTimeout(flush, TYPING_FLUSH_MS);
		},
		[accepted, enabled, enqueue, flush, immediate, onEscape],
	);

	return { onKeyDown, flush, waiting };
}
