import { useRef } from "react";

// A pointer-dismiss event carries only `preventDefault` for our purposes; the
// generic keeps it compatible with each Radix overlay's own event type
// (`PointerDownOutsideEvent`) so consumer handlers compose without casts.
type Dismissable = { preventDefault: () => void };

type OverlayDismissOverrides<E extends Dismissable> = {
	onPointerDownOutside?: (event: E) => void;
	onCloseAutoFocus?: (event: Event) => void;
};

/**
 * Shared fix for the "lingering focus ring after a mouse dismiss" / "click the
 * terminal twice before you can type" problems — generalized from PR #33, which
 * hand-patched only the notifications dropdown.
 *
 * Radix overlays (DropdownMenu / Dialog / Popover / Sheet) return focus to their
 * trigger on close via `onCloseAutoFocus`. Chromium treats that programmatic
 * focus-return as *keyboard* modality, so `:focus-visible` matches and a focus
 * ring lingers on the trigger even though the user dismissed with the mouse —
 * and the forced return steals focus from whatever the click landed on (e.g. the
 * xterm terminal), which is why a single click only closed the overlay and a
 * second click was needed to type.
 *
 * The fix: skip the focus return ONLY when the overlay was dismissed by a pointer
 * press outside it. Keyboard closes (Esc / Tab) leave the flag unset, so focus
 * still returns to the trigger — preserving menu/dialog keyboard accessibility.
 *
 * Spread the returned handlers onto the overlay content:
 *
 *   const dismissFocus = useOverlayDismissFocus();
 *   <DropdownMenuContent {...dismissFocus} />
 *
 * The shared `DropdownMenuContent` / `SelectContent` / `SheetContent` primitives
 * already do this internally, so their consumers get the behavior for free; call
 * this hook directly only for raw `@radix-ui/react-dialog` content. Pass a
 * consumer's own handlers as `overrides` so neither is dropped.
 */
export function useOverlayDismissFocus<E extends Dismissable = Dismissable>(
	overrides?: OverlayDismissOverrides<E>,
) {
	// True when this open was dismissed by a pointer press outside the overlay.
	const dismissedByPointerRef = useRef(false);

	return {
		onPointerDownOutside: (event: E) => {
			dismissedByPointerRef.current = true;
			overrides?.onPointerDownOutside?.(event);
		},
		onCloseAutoFocus: (event: Event) => {
			if (dismissedByPointerRef.current) {
				dismissedByPointerRef.current = false;
				// Keep focus wherever the outside click put it (e.g. the terminal)
				// rather than yanking it back to the trigger and lighting a stray ring.
				event.preventDefault();
			}
			overrides?.onCloseAutoFocus?.(event);
		},
	};
}
