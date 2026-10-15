import { animate, AnimatePresence, domMax, LazyMotion, m, MotionConfig } from "motion/react";
import {
	createContext,
	useContext,
	useEffect,
	useLayoutEffect,
	useRef,
	useState,
	type CSSProperties,
	type ReactNode,
	type RefObject,
} from "react";
import { usePrefersReducedMotion } from "../../hooks/usePrefersReducedMotion";
import { releaseMotion, type ReleaseMotion } from "../../lib/releases/motion";

const MotionSet = createContext<ReleaseMotion>(releaseMotion(false));

export function ReleaseMotionRoot({ children }: { children: ReactNode }) {
	const reduced = usePrefersReducedMotion();
	return (
		<LazyMotion features={domMax} strict>
			<MotionConfig reducedMotion="user">
				<MotionSet.Provider value={releaseMotion(reduced)}>{children}</MotionSet.Provider>
			</MotionConfig>
		</LazyMotion>
	);
}

export function useReleaseMotion(): ReleaseMotion {
	return useContext(MotionSet);
}

type Data = Record<`data-${string}`, string | undefined>;
type Box = { className?: string; style?: CSSProperties; data?: Data; children?: ReactNode };

const MoveScope = createContext<{ move: string; loaded: boolean }>({ move: "", loaded: false });

/** Rows inside glide when `move` changes; ones that mount after the first render fade in. */
export function MoveRoot({ move, children }: { move: string; children: ReactNode }) {
	const [loaded, setLoaded] = useState(false);
	useEffect(() => setLoaded(true), []);
	return <MoveScope.Provider value={{ move, loaded }}>{children}</MoveScope.Provider>;
}

/** Glides to its new place when the move key changes. With `appear` it fades in when it mounts after load. */
export function Moving({ appear = false, className, style, data, children }: Box & { appear?: boolean }) {
	const mo = useReleaseMotion();
	const scope = useContext(MoveScope);
	const [fresh] = useState(() => appear && scope.loaded);
	if (mo.reduced)
		return (
			<div className={className} style={style} {...data}>
				{children}
			</div>
		);
	return (
		<m.div
			className={className}
			style={style}
			{...data}
			layout="position"
			layoutDependency={scope.move}
			initial={fresh ? mo.appear.initial : false}
			animate={mo.appear.animate}
			transition={{ ...mo.appear.transition, layout: mo.row }}
		>
			{children}
		</m.div>
	);
}

/**
 * Opens on height and opacity and folds back in reverse. Absolutely placed content passes `at`: the box
 * sits at `at.top` with that height, and the content keeps the coordinates of the box's parent.
 */
export function Unfold({
	open,
	at,
	className,
	style,
	data,
	children,
}: Box & { open: boolean; at?: { top: number; height: number } }) {
	const mo = useReleaseMotion();
	const box: CSSProperties = at
		? { ...style, position: "absolute", left: 0, right: 0, top: at.top, overflow: "clip" }
		: { ...style, overflow: "clip" };
	const body = at ? <div style={{ position: "absolute", left: 0, right: 0, top: -at.top }}>{children}</div> : children;
	if (mo.reduced)
		return open ? (
			<div className={className} style={{ ...box, height: at?.height }} {...data}>
				{body}
			</div>
		) : null;
	return (
		<AnimatePresence initial={false}>
			{open && (
				<m.div
					key="unfold"
					className={className}
					style={box}
					{...data}
					initial={mo.unfold.initial}
					animate={at ? { ...mo.unfold.animate, height: at.height } : mo.unfold.animate}
					exit={mo.unfold.exit}
					transition={mo.unfold.transition}
				>
					{body}
				</m.div>
			)}
		</AnimatePresence>
	);
}

/** Fades in as it shows and out as it hides, on the unfold's clock: wires that follow an unfold. */
export function Fade({ show, children }: { show: boolean; children: ReactNode }) {
	const mo = useReleaseMotion();
	if (mo.reduced) return show ? <>{children}</> : null;
	return (
		<AnimatePresence initial={false}>
			{show && (
				<m.div
					key="fade"
					initial={mo.appear.initial}
					animate={mo.appear.animate}
					exit={mo.appear.exit}
					transition={mo.unfold.transition}
				>
					{children}
				</m.div>
			)}
		</AnimatePresence>
	);
}

type Dot = { x: number; y: number; r: number };

/** A wire whose ends glide with the blocks it joins. */
export function Wire({ d, dot, className }: { d: string; dot: Dot; className?: string }) {
	const mo = useReleaseMotion();
	if (mo.reduced)
		return (
			<>
				<path className={className} d={d} />
				<circle cx={dot.x} cy={dot.y} r={dot.r} />
			</>
		);
	return (
		<>
			<m.path className={className} initial={false} animate={{ d }} transition={mo.row} />
			<m.circle r={dot.r} initial={false} animate={{ cx: dot.x, cy: dot.y }} transition={mo.row} />
		</>
	);
}

export function WireText({ x, y, children }: { x: number; y: number; children: ReactNode }) {
	const mo = useReleaseMotion();
	if (mo.reduced)
		return (
			<text x={x} y={y}>
				{children}
			</text>
		);
	return (
		<m.text initial={false} animate={{ attrX: x, attrY: y }} transition={mo.row}>
			{children}
		</m.text>
	);
}

/** On a focus switch, the parts of the label whose text changed fade in one after another. A refetch never fades. */
export function useLabelStagger(root: RefObject<HTMLElement | null>, focus: string) {
	const mo = useReleaseMotion();
	const last = useRef<{ focus: string; texts: string[] } | null>(null);
	useLayoutEffect(() => {
		const el = root.current;
		if (!el) return;
		const parts = [...el.children] as HTMLElement[];
		const texts = parts.map((p) => p.textContent ?? "");
		const prev = last.current;
		last.current = { focus, texts };
		if (!prev || prev.focus === focus || mo.reduced) return;
		parts
			.filter((_, i) => prev.texts.length !== texts.length || prev.texts[i] !== texts[i])
			.forEach((part, i) => {
				const p = mo.label(i);
				if (p.initial && typeof p.initial.opacity === "number" && typeof p.animate.opacity === "number")
					animate(part, { opacity: [p.initial.opacity, p.animate.opacity] }, p.transition);
			});
	});
}
