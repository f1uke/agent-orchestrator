import { Maximize2, Shapes } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import type { ToolCallItem } from "../lib/chat-fold";
import {
	parseWidget,
	parseWidgetMessage,
	WIDGET_DOC_TYPE,
	WIDGET_FRAME_URL,
	widgetDocument,
	widgetTheme,
} from "../lib/chat-widget";
import type { Theme } from "../stores/ui-store";
import { LiveDots } from "./ChatProcessRows";
import { Sheet, SheetContent, SheetDescription, SheetTitle } from "./ui/sheet";

const START_HEIGHT = 160;

// A row scrolled out of the virtual list unmounts its frame. Keeping the last height means the row
// comes back at the size it had, instead of at the start height until the widget measures itself again.
const heights = new Map<string, number>();

type FrameProps = {
	html: string;
	title: string;
	theme: Theme;
	onPrompt: (text: string) => void;
	onSize?: (height: number) => void;
	className: string;
	style?: React.CSSProperties;
};

/**
 * One sandboxed frame and its bridge. The sandbox is exactly "allow-scripts": the frame has an opaque
 * origin, so it can reach nothing of the app, and everything it may ask for arrives as a message that
 * is read only when it came from this frame's own window.
 */
function WidgetFrame({ html, title, theme, onPrompt, onSize, className, style }: FrameProps) {
	const frame = useRef<HTMLIFrameElement>(null);
	const [loaded, setLoaded] = useState(false);
	const sent = useRef(false);
	const handlers = useRef({ onPrompt, onSize });
	handlers.current = { onPrompt, onSize };

	useEffect(() => {
		const onMessage = (event: MessageEvent) => {
			if (event.source === null || event.source !== frame.current?.contentWindow) return;
			const message = parseWidgetMessage(event.data);
			if (message?.type === "prompt") handlers.current.onPrompt(message.text);
			else if (message?.type === "size") handlers.current.onSize?.(message.height);
		};
		window.addEventListener("message", onMessage);
		return () => window.removeEventListener("message", onMessage);
	}, []);

	// The frame's document was built with the theme at first render. This keeps it current without
	// rebuilding it, which would reload the frame and lose the widget's state. The frame is read a
	// frame later because the app sets its theme attribute in an effect that runs after this one.
	useEffect(() => {
		if (!loaded) return;
		const id = requestAnimationFrame(() => {
			const target = frame.current?.contentWindow;
			if (!target) return;
			target.postMessage({ type: "ao-widget/theme", ...widgetTheme(document.documentElement, theme) }, "*");
		});
		return () => cancelAnimationFrame(id);
	}, [loaded, theme]);

	return (
		<iframe
			className={className}
			onLoad={() => {
				// Writing the document into the frame fires load again; the page takes one document only.
				if (sent.current) return;
				sent.current = true;
				frame.current?.contentWindow?.postMessage({ type: WIDGET_DOC_TYPE, html }, "*");
				setLoaded(true);
			}}
			ref={frame}
			referrerPolicy="no-referrer"
			sandbox="allow-scripts"
			src={WIDGET_FRAME_URL}
			style={style}
			title={title}
		/>
	);
}

export function ChatWidget({
	call,
	theme,
	onPrompt,
}: {
	call: ToolCallItem;
	theme: Theme;
	onPrompt: (text: string) => void;
}) {
	const spec = useMemo(() => parseWidget(call.input), [call.input]);
	const firstTheme = useRef(theme);
	const html = useMemo(
		() => (spec ? widgetDocument(spec, widgetTheme(document.documentElement, firstTheme.current)) : ""),
		[spec],
	);
	const [height, setHeight] = useState(() => heights.get(call.id) ?? START_HEIGHT);
	const [ready, setReady] = useState(false);
	const [expanded, setExpanded] = useState(false);
	if (!spec) return null;

	const running = call.result === undefined;
	const sizeTo = (next: number) => {
		heights.set(call.id, next);
		setHeight(next);
		setReady(true);
	};
	const prompt = (text: string) => {
		setExpanded(false);
		onPrompt(text);
	};

	return (
		<div className="chat-widget" data-widget={call.id}>
			<div className="chat-widget__head">
				<Shapes className="chat-icon" aria-hidden="true" />
				{running ? (
					<span className="chat-live">
						<LiveDots />
						<span className="chat-live__verb">Making a visual</span>
					</span>
				) : (
					<span>Made a visual</span>
				)}
				{spec.title ? <span className="chat-widget__title">{spec.title}</span> : null}
				<span className="chat-widget__grow" />
				<button
					aria-label="Expand visual"
					className="chat-widget__expand"
					onClick={() => setExpanded(true)}
					title="Expand visual"
					type="button"
				>
					<Maximize2 className="chat-icon" aria-hidden="true" />
				</button>
			</div>
			<div className="chat-widget__card">
				{ready ? null : (
					<div className="chat-widget__loading" role="status">
						{spec.loading[0] ?? "Drawing the visual…"}
					</div>
				)}
				<WidgetFrame
					className="chat-widget__frame"
					html={html}
					onPrompt={prompt}
					onSize={sizeTo}
					style={{ height, visibility: ready ? "visible" : "hidden" }}
					theme={theme}
					title={spec.title || "Visual"}
				/>
			</div>
			<Sheet onOpenChange={setExpanded} open={expanded}>
				<SheetContent className="chat-widget__sheet gap-0 sm:max-w-none" side="right">
					<div className="chat-widget__sheet-head">
						<SheetTitle>{spec.title || "Visual"}</SheetTitle>
						<SheetDescription className="sr-only">The visual at a larger size.</SheetDescription>
					</div>
					<WidgetFrame
						className="chat-widget__frame chat-widget__frame--expanded"
						html={html}
						onPrompt={prompt}
						theme={theme}
						title={`${spec.title || "Visual"} (expanded)`}
					/>
				</SheetContent>
			</Sheet>
		</div>
	);
}
