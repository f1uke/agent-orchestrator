import {
	type CSSProperties,
	createContext,
	type ReactNode,
	useContext,
	useEffect,
	useMemo,
	useRef,
	useState,
	useSyncExternalStore,
} from "react";
import { imageRefs } from "../lib/chat-refs";
import { chatFileUrl, RefStore, type ResolvedFile, resolveOverHttp } from "../lib/chat-refs-store";
import { MediaLightbox, type MediaItem, MediaThumb } from "./MediaLightbox";

type Refs = { sessionId: string; store: RefStore };

const RefsContext = createContext<Refs | null>(null);

/** One RefStore per chat session, so a ref is resolved once however many blocks name it. */
export function ChatRefsProvider({ sessionId, children }: { sessionId: string; children: ReactNode }) {
	const value = useMemo(() => ({ sessionId, store: new RefStore(resolveOverHttp(sessionId)) }), [sessionId]);
	return <RefsContext.Provider value={value}>{children}</RefsContext.Provider>;
}

// The minimum keeps a thumbnail clickable while its bytes load and when the picture is tiny.
const THUMB: CSSProperties = {
	display: "block",
	maxWidth: 240,
	maxHeight: 160,
	minWidth: 64,
	minHeight: 64,
	objectFit: "contain",
	borderRadius: 8,
	border: "1px solid var(--border-1)",
};

type Shown = { item: MediaItem; path: string };

function shownImages(sessionId: string, refs: readonly string[], known: ReadonlyMap<string, ResolvedFile[]>): Shown[] {
	const seen = new Set<string>();
	const shown: Shown[] = [];
	for (const ref of refs) {
		for (const file of known.get(ref) ?? []) {
			const id = `${file.root}:${file.path}`;
			if (file.kind !== "image" || seen.has(id)) continue;
			seen.add(id);
			shown.push({
				path: file.path,
				item: {
					id,
					filename: file.path.split("/").pop() || file.path,
					mime: file.mime,
					src: chatFileUrl(sessionId, file),
				},
			});
		}
	}
	return shown;
}

const noSubscribe = () => () => undefined;
const noSnapshot = () => undefined;

/**
 * The images a block of chat text names by path or glob, as thumbnails under it.
 * Pass the block's final text: a streaming block is drawn as plain text and never
 * reaches here. A ref the daemon cannot confirm draws nothing.
 */
export function RefImages({ source }: { source: string }) {
	const refs = useContext(RefsContext);
	const names = useMemo(() => (refs ? imageRefs(source) : []), [refs, source]);
	useEffect(() => {
		if (refs && names.length > 0) refs.store.ensure(names);
	}, [refs, names]);
	const known = useSyncExternalStore(refs?.store.subscribe ?? noSubscribe, refs?.store.snapshot ?? noSnapshot);
	const [open, setOpen] = useState<number | null>(null);
	const trigger = useRef<HTMLElement | null>(null);
	const shown = useMemo(() => (refs && known ? shownImages(refs.sessionId, names, known) : []), [refs, known, names]);
	if (shown.length === 0) return null;
	const items = shown.map((s) => s.item);
	return (
		<div className="ref-images" data-testid="ref-images">
			{shown.map(({ item, path }, i) => (
				<figure className="ref-images__item" key={item.id}>
					<MediaThumb
						item={item}
						onOpen={(el) => {
							trigger.current = el;
							setOpen(i);
						}}
						style={THUMB}
					/>
					<figcaption className="ref-images__name" title={path}>
						{item.filename}
					</figcaption>
				</figure>
			))}
			{open !== null ? (
				<MediaLightbox
					index={open}
					items={items}
					onClose={() => setOpen(null)}
					onIndexChange={setOpen}
					triggerRef={trigger}
				/>
			) : null}
		</div>
	);
}
