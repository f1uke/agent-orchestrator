import { observeElementRect } from "@tanstack/react-virtual";

/**
 * The viewport a virtualised list assumes when its scroller measures zero-height.
 *
 * jsdom has no layout, so `getBoundingClientRect()` there is all zeros and a
 * virtualiser told the truth would render nothing at all - every existing test
 * of a windowed list would go blank. It is also the right answer in the browser
 * for the moment before layout has run: rows appear immediately and the
 * ResizeObserver corrects the count on the next frame.
 */
const FALLBACK_VIEWPORT = { width: 320, height: 900 };

/**
 * The scroller's size, falling back to {@link FALLBACK_VIEWPORT} when it
 * measures zero. `observeElementRect` is the library's own implementation; all
 * this adds is the floor. Pass it as a virtualiser's `observeElementRect`.
 */
export const measuredOrFallbackRect: typeof observeElementRect = (instance, cb) =>
	observeElementRect(instance, (rect) =>
		cb(rect.height > 0 ? rect : { width: rect.width || FALLBACK_VIEWPORT.width, height: FALLBACK_VIEWPORT.height }),
	);
