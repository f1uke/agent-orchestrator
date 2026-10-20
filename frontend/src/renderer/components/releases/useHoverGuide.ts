import { useEffect, type RefObject } from "react";
import { DW } from "../../lib/releases/layout";
import { hoverColumn } from "../../lib/releases/hover";

/** Moves one line and one header chip by hand from a frame callback, so a pointer move never re-renders the lanes. */
export function useHoverGuide(
	sc: RefObject<HTMLDivElement | null>,
	canvas: RefObject<HTMLDivElement | null>,
	days: number,
) {
	useEffect(() => {
		const scroller = sc.current;
		const root = canvas.current;
		const line = root?.querySelector<HTMLElement>(".hovercol");
		if (!scroller || !root || !line) return;
		let x: number | null = null;
		let queued = 0;
		let chip: Element | null = null;
		const paint = () => {
			queued = 0;
			const col =
				x === null
					? null
					: hoverColumn({
							clientX: x,
							scrollerLeft: scroller.getBoundingClientRect().left,
							canvasLeft: root.getBoundingClientRect().left,
							days,
						});
			const next = col === null ? null : root.querySelector(`.rel-day[data-col="${col}"]`);
			if (next !== chip) {
				chip?.classList.remove("hv");
				next?.classList.add("hv");
				chip = next;
			}
			line.hidden = col === null;
			if (col !== null) line.style.transform = `translateX(${col * DW + DW / 2}px)`;
		};
		const queue = () => {
			if (!queued) queued = requestAnimationFrame(paint);
		};
		const move = (e: PointerEvent) => {
			x = e.clientX;
			queue();
		};
		const leave = () => {
			x = null;
			queue();
		};
		scroller.addEventListener("pointermove", move, { passive: true });
		scroller.addEventListener("pointerleave", leave);
		scroller.addEventListener("scroll", queue, { passive: true });
		return () => {
			scroller.removeEventListener("pointermove", move);
			scroller.removeEventListener("pointerleave", leave);
			scroller.removeEventListener("scroll", queue);
			cancelAnimationFrame(queued);
			chip?.classList.remove("hv");
			line.hidden = true;
		};
	}, [sc, canvas, days]);
}
