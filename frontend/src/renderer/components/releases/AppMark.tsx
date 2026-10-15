import type { AppMarkData } from "../../lib/releases/appMark";
import type { BoardApp } from "../../lib/releases/types";

const GLYPH: Record<BoardApp["platform"], string> = {
	ios: "M11.3 8.6c0-1.7 1.4-2.5 1.5-2.5-.8-1.2-2.1-1.4-2.5-1.4-1-.1-2.1.6-2.6.6s-1.4-.6-2.3-.6C4.2 4.7 3 5.4 2.4 6.6c-1.3 2.2-.3 5.4.9 7.2.6.9 1.3 1.8 2.2 1.8.9 0 1.2-.6 2.3-.6s1.4.6 2.3.6c1 0 1.6-.9 2.1-1.8.7-1 1-1.9 1-1.9s-1.9-.8-1.9-3.3zM9.6 3.5c.5-.6.8-1.4.7-2.3-.7 0-1.6.5-2.1 1.1-.5.5-.9 1.3-.8 2.2.8.1 1.6-.4 2.2-1z",
	android:
		"M4.6 3.1a.4.4 0 0 1 .55.12l1.03 1.62A6.6 6.6 0 0 1 8 4.57c.64 0 1.25.1 1.82.27l1.03-1.62a.4.4 0 1 1 .67.43l-.98 1.55A6.3 6.3 0 0 1 14.6 11H1.4a6.3 6.3 0 0 1 4.06-5.8L4.48 3.65a.4.4 0 0 1 .12-.55zM5.3 7.4a.75.75 0 1 0 0 1.5.75.75 0 0 0 0-1.5zm5.4 0a.75.75 0 1 0 0 1.5.75.75 0 0 0 0-1.5z",
};

const VIEW_BOX: Record<BoardApp["platform"], string> = { ios: "0 0 16 16", android: "1.2 1.6 13.6 10.4" };

export function PlatformGlyph({ platform }: { platform: BoardApp["platform"] }) {
	return (
		<svg className="pg" viewBox={VIEW_BOX[platform]} aria-hidden="true" data-platform={platform}>
			<path d={GLYPH[platform]} fillRule="evenodd" />
		</svg>
	);
}

export function AppMark({ mark, size = "md" }: { mark: AppMarkData | null; size?: "sm" | "md" }) {
	if (!mark) return <span className="appmark">all apps</span>;
	return (
		<span className={`appmark ${size}`} role="img" title={mark.name} aria-label={mark.name} data-testid="app-mark">
			<span aria-hidden="true">{mark.family}</span>
			<PlatformGlyph platform={mark.platform} />
		</span>
	);
}
