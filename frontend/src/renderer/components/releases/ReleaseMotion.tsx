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
import { enterKeyframes, releaseMotion, type MoveKeys, type ReleaseMotion } from "../../lib/releases/motion";

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

const MoveScope = createContext<MoveKeys & { loaded: boolean }>({ rows: "", blocks: "", loaded: false });

export function MoveRoot({ keys, children }: { keys: MoveKeys; children: ReactNode }) {
	const [loaded, setLoaded] = useState(false);
	useEffect(() => setLoaded(true), []);
	return <MoveScope.Provider value={{ ...keys, loaded }}>{children}</MoveScope.Provider>;
}

export function Moving({
	track,
	height,
	appear = false,
	className,
	style,
	data,
	children,
}: Box & { track: keyof MoveKeys; height?: number; appear?: boolean }) {
	const mo = useReleaseMotion();
	const scope = useContext(MoveScope);
	const [fresh] = useState(() => appear && scope.loaded);
	const el = useRef<HTMLDivElement>(null);
	const last = useRef({ blocks: scope.blocks, height });
	const shown = useRef<number | null>(null);
	useLayoutEffect(() => {
		const prev = last.current;
		last.current = { blocks: scope.blocks, height };
		const from = shown.current ?? prev.height;
		shown.current = null;
		const node = el.current;
		if (!node || height === undefined) return;
		node.style.minHeight = `${height}px`;
		if (mo.reduced || from === undefined || from === height || prev.blocks === scope.blocks) return;
		// Motion 13.5.1 never applies animate={{ minHeight }} on an element that also has `layout`,
		// and animate() starts a frame late, so the start value is set before paint.
		node.style.minHeight = `${from}px`;
		let running = true;
		const a = animate(
			node,
			{ minHeight: [`${from}px`, `${height}px`] },
			{ ...mo.fold, onComplete: () => (running = false) },
		);
		return () => {
			if (!running) return;
			shown.current = parseFloat(getComputedStyle(node).minHeight);
			a.stop();
		};
	}, [scope.blocks, height, mo]);
	const box = height === undefined ? style : { ...style, minHeight: height };
	if (mo.reduced)
		return (
			<div className={className} style={box} {...data}>
				{children}
			</div>
		);
	return (
		<m.div
			ref={el}
			className={className}
			style={box}
			{...data}
			layout="position"
			layoutDependency={scope[track]}
			initial={fresh ? mo.appear.initial : false}
			animate={mo.appear.animate}
			transition={{ ...mo.appear.transition, layout: mo.row }}
		>
			{children}
		</m.div>
	);
}

export function Unfold({
	open,
	clipAt: clip,
	className,
	style,
	data,
	children,
}: Box & { open: boolean; clipAt?: { top: number; height: number } }) {
	const mo = useReleaseMotion();
	const box: CSSProperties = clip
		? { ...style, position: "absolute", left: 0, right: 0, top: clip.top, overflow: "clip" }
		: { ...style, overflow: "clip" };
	const body = clip ? (
		<div style={{ position: "absolute", left: 0, right: 0, top: -clip.top }}>{children}</div>
	) : (
		children
	);
	if (mo.reduced)
		return open ? (
			<div className={className} style={{ ...box, height: clip?.height }} {...data}>
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
					animate={clip ? { ...mo.unfold.animate, height: clip.height } : mo.unfold.animate}
					exit={mo.unfold.exit}
					transition={mo.unfold.transition}
				>
					{body}
				</m.div>
			)}
		</AnimatePresence>
	);
}

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
				const frames = enterKeyframes(p);
				if (frames) animate(part, frames, p.transition);
			});
	});
}
