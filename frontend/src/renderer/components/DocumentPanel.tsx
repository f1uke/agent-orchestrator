import { ArrowUpRight, X } from "lucide-react";
import { type ComponentType, useEffect, useMemo, useRef, useState } from "react";
import { useBrowserView } from "../hooks/useBrowserView";
import { type DocKind, docMeta, docTag, type FileDoc, textView } from "../lib/chat-docs";
import { previewDocument } from "../lib/chat-preview";
import { chatFileUrl } from "../lib/chat-refs-store";
import { WIDGET_DOC_TYPE, WIDGET_FRAME_URL } from "../lib/chat-widget";
import { useUiStore } from "../stores/ui-store";
import { CodeBox } from "./ChatCodeBox";
import { NoteMarkdown } from "./NoteMarkdown";
import { Button } from "./ui/button";

type Loaded = { state: "loading" } | { state: "failed" } | { state: "ready"; text: string };

// The daemon serves only allowlisted types, so a refusal is the whole failure story: no retry, no detail.
function useDocText(url: string | undefined, revision = 0): Loaded {
	const [loaded, setLoaded] = useState<Loaded>({ state: "loading" });
	useEffect(() => {
		if (url === undefined) return;
		const controller = new AbortController();
		setLoaded({ state: "loading" });
		fetch(url, { signal: controller.signal })
			.then((response) => (response.ok ? response.text() : Promise.reject(new Error(`file ${response.status}`))))
			.then((text) => setLoaded({ state: "ready", text }))
			.catch(() => {
				if (!controller.signal.aborted) setLoaded({ state: "failed" });
			});
		return () => controller.abort();
	}, [url, revision]);
	return loaded;
}

type ViewerProps = { doc: FileDoc; url: string; sessionId: string; active: boolean; revision: number };

const KIND_VIEWS: Record<DocKind, { Viewer: ComponentType<ViewerProps>; opensInBrowser: boolean }> = {
	markdown: { Viewer: TextDoc, opensInBrowser: false },
	json: { Viewer: TextDoc, opensInBrowser: false },
	text: { Viewer: TextDoc, opensInBrowser: false },
	log: { Viewer: TextDoc, opensInBrowser: false },
	diff: { Viewer: TextDoc, opensInBrowser: false },
	html: { Viewer: HtmlDoc, opensInBrowser: true },
	pdf: { Viewer: NativeDoc, opensInBrowser: true },
	image: { Viewer: ImageDoc, opensInBrowser: true },
	video: { Viewer: VideoDoc, opensInBrowser: true },
};

function Thumbnail({ url }: { url: string }) {
	const [failed, setFailed] = useState(false);
	if (failed) return null;
	return <img alt="" className="document-list__thumb" loading="lazy" onError={() => setFailed(true)} src={url} />;
}

export function DocumentPanel({
	sessionId,
	doc,
	active,
	onClose,
	docs = [],
	onOpen,
	revision = 0,
}: {
	sessionId: string;
	doc: FileDoc | null;
	active: boolean;
	onClose: () => void;
	docs?: readonly FileDoc[];
	onOpen?: (doc: FileDoc) => void;
	revision?: number;
}) {
	if (!doc) {
		return (
			<div className="document-panel" role="tabpanel">
				{docs.length > 0 && onOpen ? (
					<ul aria-label="Results in this session" className="document-list">
						{docs.map((d) => (
							<li className="document-list__item" key={`${d.root}:${d.path}`}>
								<button
									aria-label={`Open ${d.title}`}
									className="document-list__open"
									onClick={() => onOpen(d)}
									type="button"
								>
									<span aria-hidden="true" className="chat-doc__tag" data-kind={d.kind}>
										{docTag(d)}
									</span>
									<span className="chat-doc__text">
										<span className="chat-doc__title">{d.title}</span>
										<span className="chat-doc__meta" title={d.path}>
											{docMeta(d)}
										</span>
									</span>
									{d.kind === "image" ? <Thumbnail url={chatFileUrl(sessionId, d)} /> : null}
								</button>
							</li>
						))}
					</ul>
				) : (
					<p className="inspector-empty">Open a document or media from the chat to see it here.</p>
				)}
			</div>
		);
	}
	const url = chatFileUrl(sessionId, doc);
	const { Viewer, opensInBrowser } = KIND_VIEWS[doc.kind];
	return (
		<div className="document-panel" role="tabpanel">
			<header className="document-panel__head">
				<div className="document-panel__titles">
					<h2 className="document-panel__title">{doc.title}</h2>
					<p className="document-panel__meta" title={doc.path}>
						{docMeta(doc)}
					</p>
				</div>
				{opensInBrowser ? (
					<Button asChild size="sm" variant="outline">
						<a href={url} rel="noreferrer noopener" target="_blank">
							Open in browser
							<ArrowUpRight aria-hidden="true" className="h-3.5 w-3.5" />
						</a>
					</Button>
				) : null}
				<Button aria-label="Close document" onClick={onClose} size="icon-sm" type="button" variant="ghost">
					<X aria-hidden="true" className="h-4 w-4" />
				</Button>
			</header>
			<div className="document-panel__body">
				<Viewer
					active={active}
					doc={doc}
					key={`${doc.root}:${doc.path}`}
					revision={revision}
					sessionId={sessionId}
					url={url}
				/>
			</div>
		</div>
	);
}

function NativeDoc({ sessionId, url, active }: ViewerProps) {
	const native = Boolean(window.ao?.browser);
	const view = useBrowserView({
		sessionId,
		viewName: `doc:${sessionId}`,
		disposable: true,
		active,
		poppedOut: false,
		previewUrl: native ? url : undefined,
	});
	if (!native) {
		return (
			<p className="inspector-empty">This document opens in the desktop app. Use Open in browser to read it here.</p>
		);
	}
	return <div className="document-panel__slot" data-doc-slot ref={view.slotRef} />;
}

type HtmlMode = "preview" | "source";

const HTML_MODES: ReadonlyArray<readonly [HtmlMode, string]> = [
	["preview", "Preview"],
	["source", "Source"],
];

function HtmlDoc({ doc, url, revision }: ViewerProps) {
	const theme = useUiStore((state) => state.theme);
	const [mode, setMode] = useState<HtmlMode>("preview");
	const loaded = useDocText(url, revision);
	const source = useMemo(
		() => (loaded.state === "ready" && mode === "source" ? textView(doc, loaded.text) : undefined),
		[doc, loaded, mode],
	);
	return (
		<div className="document-html">
			<div aria-label="View" className="document-html__modes" role="tablist">
				{HTML_MODES.map(([value, label]) => (
					<button
						aria-selected={mode === value}
						className="document-html__mode"
						key={value}
						onClick={() => setMode(value)}
						role="tab"
						type="button"
					>
						{label}
					</button>
				))}
			</div>
			{loaded.state === "failed" ? (
				<Failed doc={doc} />
			) : loaded.state !== "ready" ? null : mode === "preview" ? (
				<PreviewFrame html={loaded.text} key={revision} title={`Preview of ${doc.title}`} />
			) : source ? (
				<CodeBox mode="read" named={false} theme={theme} view={source} />
			) : null}
		</div>
	);
}

// Opaque origin (no allow-same-origin): the file reaches no cookie, storage or API of the app.
function PreviewFrame({ html, title }: { html: string; title: string }) {
	const frame = useRef<HTMLIFrameElement>(null);
	const sent = useRef(false);
	return (
		<iframe
			className="document-html__frame"
			onLoad={() => {
				if (sent.current) return;
				sent.current = true;
				frame.current?.contentWindow?.postMessage({ type: WIDGET_DOC_TYPE, html: previewDocument(html) }, "*");
			}}
			ref={frame}
			referrerPolicy="no-referrer"
			sandbox="allow-scripts"
			src={WIDGET_FRAME_URL}
			title={title}
		/>
	);
}

function ImageDoc({ doc, url }: ViewerProps) {
	const [fit, setFit] = useState(true);
	const [failed, setFailed] = useState(false);
	if (failed) return <Failed doc={doc} />;
	return (
		<div className="document-media">
			<div className="document-media__bar">
				<Button onClick={() => setFit(!fit)} size="sm" type="button" variant="outline">
					{fit ? "Full size" : "Fit"}
				</Button>
			</div>
			<div className="document-media__stage">
				<img
					alt={doc.title}
					className="document-media__image"
					data-fit={fit ? "fit" : "full"}
					onClick={() => setFit(!fit)}
					onError={() => setFailed(true)}
					src={url}
				/>
			</div>
		</div>
	);
}

function VideoDoc({ doc, url }: ViewerProps) {
	const [failed, setFailed] = useState(false);
	if (failed) return <Failed doc={doc} />;
	return (
		<div className="document-media">
			<div className="document-media__stage">
				<video
					className="document-media__video"
					controls
					onError={() => setFailed(true)}
					preload="metadata"
					src={url}
				/>
			</div>
		</div>
	);
}

function TextDoc({ doc, url }: ViewerProps) {
	const theme = useUiStore((state) => state.theme);
	const loaded = useDocText(url);
	const view = useMemo(
		() => (loaded.state === "ready" && doc.kind !== "markdown" ? textView(doc, loaded.text) : undefined),
		[doc, loaded],
	);
	if (loaded.state === "failed") return <Failed doc={doc} />;
	if (loaded.state !== "ready") return null;
	if (doc.kind === "markdown") {
		return (
			<div className="note-prose chat-md document-panel__prose">
				<NoteMarkdown source={loaded.text} theme={theme} />
			</div>
		);
	}
	return view ? <CodeBox mode="read" named={false} theme={theme} view={view} /> : null;
}

function Failed({ doc }: { doc: FileDoc }) {
	return (
		<p className="document-panel__error" role="alert">
			Could not open {doc.title}. The file may have moved or grown past what the chat shows.
		</p>
	);
}
