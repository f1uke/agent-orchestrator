/**
 * The shape shared by the board-card chips that carry their own button - a label
 * and a one-click action in one pill ("Undelivered · Move to Done", "Merged #12 ·
 * Move to Done").
 *
 * At ~170px such a chip is wider than a card's chip line at an ordinary window
 * width (148px at 1440px), so it must fit without clipping and without leaving
 * the card. Below 11rem of line - the nearest `@container`, which the card's
 * chip line is - the button drops UNDER the label, indented to the label's text,
 * and the pill becomes a rounded box. The radius is the pill's own half-height,
 * so on one line nothing about it changes. Outside a container it simply stays
 * on one line.
 */
export const CHIP_WITH_ACTION =
	"inline-flex max-w-full shrink-0 items-center gap-1 rounded-[11px] border py-0.5 pl-1.5 pr-0.5 text-[10px] font-medium @max-[11rem]:flex-col @max-[11rem]:items-start @max-[11rem]:gap-0 @max-[11rem]:pr-1.5";

export const CHIP_ACTION_BUTTON =
	"rounded-full px-1.5 py-px text-passive transition-colors hover:bg-[color-mix(in_srgb,var(--fg-passive)_16%,transparent)] disabled:opacity-50 @max-[11rem]:ml-2.5";
