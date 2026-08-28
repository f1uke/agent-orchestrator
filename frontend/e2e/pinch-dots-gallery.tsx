import { createRoot } from "react-dom/client";
import { useEffect, useRef } from "react";

import { SimPinchDots } from "../src/renderer/components/SimPinchDots";
import { PINCH_ANCHOR, pinchGrip } from "../src/renderer/lib/pinch";
import "../src/renderer/styles.css";

/** The picture, at the aspect an iPhone 17 Pro Max is drawn at (1320x2868). */
const SCREEN = { width: Math.round((520 * 1320) / 2868), height: 520 };

/** The device body around the picture, as the Device tab draws one. */
const BEZEL = 11;

/** Where the pointer is, in device coordinates. The spec reads these back. */
const AT = { x: 0.32, y: 0.7 };

/**
 * One pane: a "device picture" of FRAME's aspect letterboxed into a box that is
 * deliberately the WRONG shape, so a dot placed against the pane instead of
 * against the picture lands visibly elsewhere.
 */
function Pane({ ground, label, testId }: { ground: "article" | "chart"; label: string; testId: string }) {
	const dots = useRef<HTMLDivElement | null>(null);
	useEffect(() => {
		const el = dots.current;
		if (!el) return;
		const grip = pinchGrip(AT, PINCH_ANCHOR);
		const set = (name: string, v: number) => el.style.setProperty(name, `${v * 100}%`);
		set("--pinch-ax", grip.a.x);
		set("--pinch-ay", grip.a.y);
		set("--pinch-bx", grip.b!.x);
		set("--pinch-by", grip.b!.y);
		set("--pinch-px", PINCH_ANCHOR.x);
		set("--pinch-py", PINCH_ANCHOR.y);
	}, []);

	return (
		<figure style={{ margin: 0 }}>
			<figcaption style={{ font: "12px system-ui", padding: "6px 0", color: "var(--fg-muted)" }}>{label}</figcaption>
			{/* 260x520 is not FRAME's aspect (that would be 236x520), so the picture
			    is letterboxed with a 12px bar each side - which is exactly what a dot
			    positioned against the pane rather than the picture would get wrong. */}
			{/* A device body of BEZEL around the picture, exactly as the pane draws
			    one, so the dots have to be inset by it to sit on the screen. */}
			<div
				data-testid={testId}
				style={{
					position: "relative",
					width: SCREEN.width + BEZEL * 2,
					height: SCREEN.height + BEZEL * 2,
					padding: BEZEL,
					background: "#1a1a1a",
				}}
			>
				<div
					data-testid={`${testId}-picture`}
					style={{
						width: SCREEN.width,
						height: SCREEN.height,
						...(ground === "article"
							? { background: "#ffffff", color: "#111" }
							: { background: "#0b0d12", color: "#dfe6f2" }),
					}}
				>
					{ground === "article" ? (
						<p style={{ font: "11px/1.5 Georgia,serif", padding: 10 }}>
							A white article page. The dots have to be legible on this and on the chart beside it, and the app being
							driven chose both grounds, not us.
						</p>
					) : (
						<svg aria-hidden height="100%" viewBox="0 0 100 200" width="100%">
							<rect fill="#0b0d12" height="200" width="100" />
							<polyline
								fill="none"
								points="4,180 20,140 36,150 52,90 68,110 84,40 96,60"
								stroke="#3ddc97"
								strokeWidth="2"
							/>
						</svg>
					)}
				</div>
				<SimPinchDots inset={BEZEL} ref={dots} screen={SCREEN} />
			</div>
		</figure>
	);
}

function Gallery() {
	return (
		<div style={{ display: "flex", gap: 24, padding: 20, background: "var(--bg)", minHeight: "100vh" }}>
			<Pane ground="article" label="a white article" testId="pane-article" />
			<Pane ground="chart" label="a dark chart" testId="pane-chart" />
		</div>
	);
}

createRoot(document.getElementById("root")!).render(<Gallery />);
