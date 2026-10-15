export type BarPartId = "tt" | "dates" | "mr" | "next" | "tag" | "title";

export const BAR_DROP_ORDER: readonly BarPartId[] = ["tag", "next", "dates", "mr", "tt"];

/**
 * Parts to hide so a bar's label fits `available` px. `need` measures the label
 * with a set of parts hidden; it is called again after each part is dropped.
 */
export function fitParts<T extends string>(
	dropOrder: readonly T[],
	available: number,
	need: (hidden: ReadonlySet<T>) => number,
): Set<T> {
	const hidden = new Set<T>();
	for (const part of dropOrder) {
		if (need(hidden) <= available) break;
		hidden.add(part);
	}
	return hidden;
}

/** A sticky label pinned at the app column's edge hides once its box has less room left than the label needs. */
export function labelHidden({
	boxLeft,
	boxRight,
	labelWidth,
	edge,
}: {
	boxLeft: number;
	boxRight: number;
	labelWidth: number;
	edge: number;
}): boolean {
	return boxLeft < edge && boxRight - edge < labelWidth + 6;
}
