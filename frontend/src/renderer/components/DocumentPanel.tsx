import { ArrowUpRight, X } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { useBrowserView } from "../hooks/useBrowserView";
import { docMeta, type FileDoc, textView } from "../lib/chat-docs";
import { chatFileUrl } from "../lib/chat-refs-store";
import { useUiStore } from "../stores/ui-store";
import { CodeBox } from "./ChatCodeBox";
import { useObjectUrl } from "./MediaLightbox";
import { NoteMarkdown } from "./NoteMarkdown";
import { Button } from "./ui/button";

type Loaded = { state: "loading" } | { state: "failed" } | { state: "ready"; text: string };

// The daemon serves only allowlisted types, so a refusal is the whole failure story: no retry, no detail.
function useDocText(url: string | undefined): Loaded {
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
	}, [url]);
	return loaded;
}

const NATIVE_KINDS = new Set<FileDoc["kind"]>(["html", "pdf"]);

/**
 * The inspector's Document tab: the file a chat card opened, drawn by kind. HTML and PDF go to a native
 * view because the daemon serves them under a sandbox CSP that only a real page can honour; the rest is
 * read here and drawn in the app's own DOM, where no markup from the file can run.
 */
export function DocumentPanel({
	sessionId,
	doc,
	active,
	onClose,
}: {
	sessionId: string;
	doc: FileDoc | null;
	active: boolean;
	onClose: () => void;
}) {
	if (!doc) {
		return (
			<div className="document-panel" role="tabpanel">
				<p className="inspector-empty">Open a document from the chat to read it here.</p>
			</div>
		);
	}
	const url = chatFileUrl(sessionId, doc);
	return (
		<div className="document-panel" role="tabpanel">
			<header className="document-panel__head">
				<div className="document-panel__titles">
					<h2 className="document-panel__title">{doc.title}</h2>
					<p className="document-panel__meta" title={doc.path}>
						{docMeta(doc)}
					</p>
				</div>
				{NATIVE_KINDS.has(doc.kind) ? (
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
				{NATIVE_KINDS.has(doc.kind) ? (
					<NativeDoc active={active} sessionId={sessionId} url={url} />
				) : doc.kind === "image" ? (
					<ImageDoc doc={doc} url={url} />
				) : (
					<TextDoc doc={doc} url={url} />
				)}
			</div>
		</div>
	);
}

function NativeDoc({ sessionId, url, active }: { sessionId: string; url: string; active: boolean }) {
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

function ImageDoc({ doc, url }: { doc: FileDoc; url: string }) {
	const { url: blob, failed } = useObjectUrl(url);
	if (failed) return <Failed doc={doc} />;
	return blob ? <img alt={doc.title} className="document-panel__image" src={blob} /> : null;
}

function TextDoc({ doc, url }: { doc: FileDoc; url: string }) {
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
