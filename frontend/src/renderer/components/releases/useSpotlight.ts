import { animate } from "motion/react";
import { useEffect, useRef } from "react";
import { LABEL_W } from "../../lib/releases/layout";
import type { Reveal } from "../../lib/releases/locate";
import type { ReleaseMotion } from "../../lib/releases/motion";
import { grow, intersect, runs, scrollDelta, union, type Box } from "../../lib/releases/reveal";
import { useReleasesUi } from "../../stores/releases-ui";
import { useReleaseMotion } from "./ReleaseMotion";

const FIND_MS = 2500;
const RING_PAD = 5;
const RING_ROOM = RING_PAD + 2;
const OPEN: Box = { left: -Infinity, top: -Infinity, right: Infinity, bottom: Infinity };

type Member = { el: HTMLElement; scroller: HTMLElement | null; clips: HTMLElement[] };
type Spot = { members: Member[]; ring: HTMLElement };

function rect(el: Element): Box {
	const r = el.getBoundingClientRect();
	return { left: r.left, top: r.top, right: r.right, bottom: r.bottom };
}

const scrollerOf = (el: HTMLElement) => el.closest<HTMLElement>(".rel-sc, .sh-b");
const sticky = (el: HTMLElement) => !!el.closest(".rel-lab");

function viewOf(scroller: HTMLElement, labelsToo: boolean): Box {
	const b = scroller.getBoundingClientRect();
	const left = b.left + scroller.clientLeft;
	const top = b.top + scroller.clientTop;
	const right = left + scroller.clientWidth - RING_ROOM;
	const bottom = top + scroller.clientHeight - RING_ROOM;
	if (!scroller.classList.contains("rel-sc")) return { left: left + RING_ROOM, top: top + RING_ROOM, right, bottom };
	const axis = scroller.querySelector(".rel-axis")?.getBoundingClientRect().bottom ?? top;
	const sheet = document.querySelector(".rel-sheet")?.getBoundingClientRect().left ?? Infinity;
	return { top: axis, left: labelsToo ? left : left + LABEL_W, right: Math.min(right, sheet - RING_ROOM), bottom };
}

function clipsOf(el: HTMLElement, scroller: HTMLElement | null): HTMLElement[] {
	const out: HTMLElement[] = [];
	for (let a = el.parentElement; a && a !== scroller; a = a.parentElement) {
		const cs = getComputedStyle(a);
		if (cs.overflowX !== "visible" || cs.overflowY !== "visible") out.push(a);
	}
	return out;
}

function clipBox(el: HTMLElement): Box {
	const cs = getComputedStyle(el);
	const r = rect(el);
	return {
		left: cs.overflowX === "visible" ? OPEN.left : r.left,
		right: cs.overflowX === "visible" ? OPEN.right : r.right,
		top: cs.overflowY === "visible" ? OPEN.top : r.top,
		bottom: cs.overflowY === "visible" ? OPEN.bottom : r.bottom,
	};
}

function visibleBox(m: Member): Box | null {
	let v = intersect(rect(m.el), m.scroller ? viewOf(m.scroller, sticky(m.el)) : OPEN);
	for (const c of m.clips) v = v && intersect(v, clipBox(c));
	return v;
}

function spotBox(s: Spot): Box | null {
	const seen = s.members.map(visibleBox).filter((v): v is Box => !!v);
	return seen.length ? union(seen) : null;
}

function find(r: Reveal): HTMLElement[] | null {
	const groups = r.selectors.map((sel) =>
		[...document.querySelectorAll<HTMLElement>(sel)].filter(
			(el) => r.target === "sheet-part" || !el.closest(".rel-sheet"),
		),
	);
	if (groups.some((g) => !g.length)) return null;
	const els = groups.flat();
	return els.every((el) => el.getBoundingClientRect().width > 0) ? els : null;
}

function glide(el: HTMLElement, key: "scrollLeft" | "scrollTop", by: number, mo: ReleaseMotion) {
	const max = key === "scrollLeft" ? el.scrollWidth - el.clientWidth : el.scrollHeight - el.clientHeight;
	const from = el[key];
	const to = Math.max(0, Math.min(max, from + by));
	if (Math.abs(to - from) < 1) return null;
	if (mo.reduced) {
		el[key] = to;
		return null;
	}
	return animate(from, to, { ...mo.axis, onUpdate: (v) => (el[key] = v) });
}

function light(els: [HTMLElement, ...HTMLElement[]], mo: ReleaseMotion): () => void {
	const scroller = scrollerOf(els[0]);
	const glides: ({ stop: () => void } | null)[] = [];
	if (scroller) {
		const labels = els.every(sticky);
		const { dx, dy } = scrollDelta(els.map(rect) as [Box, ...Box[]], viewOf(scroller, labels), { sticky: labels });
		glides.push(glide(scroller, "scrollLeft", dx, mo), glide(scroller, "scrollTop", dy, mo));
	}
	const members: Member[] = els.map((el) => {
		const own = scrollerOf(el);
		return { el, scroller: own, clips: clipsOf(el, own) };
	});
	const spots: Spot[] = runs(els.map(rect)).map((run) => {
		const ring = document.createElement("div");
		ring.className = "rel-halo";
		const [first] = run.map((i) => members[i]);
		(first.scroller ?? first.el.closest(".rel"))?.querySelector(":scope > .rel-halos")?.append(ring);
		return { members: run.map((i) => members[i]), ring };
	});
	let frame = 0;
	const place = () => {
		for (const s of spots) {
			const v = spotBox(s);
			const layer = s.ring.parentElement;
			s.ring.hidden = !v || !layer;
			if (!v || !layer) continue;
			const at = layer.getBoundingClientRect();
			const g = grow(v, RING_PAD);
			s.ring.style.left = `${g.left - at.left}px`;
			s.ring.style.top = `${g.top - at.top}px`;
			s.ring.style.width = `${g.right - g.left}px`;
			s.ring.style.height = `${g.bottom - g.top}px`;
		}
		frame = requestAnimationFrame(place);
	};
	place();
	let timer = 0;
	const pulses: Animation[] = [];
	const stop = () => {
		cancelAnimationFrame(frame);
		window.clearTimeout(timer);
		for (const g of glides) g?.stop();
		for (const p of pulses) p.cancel();
		for (const s of spots) s.ring.remove();
	};
	if (mo.ring.kind === "pulse") {
		const { keyframes, timing } = mo.ring;
		pulses.push(...spots.map((s) => s.ring.animate(keyframes, timing)));
		pulses[0]?.finished.then(stop, () => {});
	} else timer = window.setTimeout(stop, mo.ring.ms);
	return stop;
}

export function useSpotlight() {
	const mo = useReleaseMotion();
	const pending = useReleasesUi((s) => s.pending);
	const stop = useRef<(() => void) | null>(null);
	useEffect(() => () => stop.current?.(), []);
	useEffect(() => {
		if (!pending) return;
		stop.current?.();
		stop.current = null;
		const start = performance.now();
		let frame = 0;
		let last = "";
		const look = () => {
			const els = find(pending);
			const at = els?.map((el) => Object.values(rect(el)).join(",")).join("|") ?? "";
			if (els?.length && at === last) {
				stop.current = light(els as [HTMLElement, ...HTMLElement[]], mo);
				useReleasesUi.getState().setPending(null);
				return;
			}
			last = at;
			if (performance.now() - start > FIND_MS) {
				if (import.meta.env.DEV) console.warn(`Show found nothing stable for ${pending.selectors.join(", ")}`);
				useReleasesUi.getState().setPending(null);
				return;
			}
			frame = requestAnimationFrame(look);
		};
		frame = requestAnimationFrame(look);
		return () => cancelAnimationFrame(frame);
	}, [pending, mo]);
}
