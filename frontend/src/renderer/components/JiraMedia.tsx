import {
	createContext,
	useCallback,
	useContext,
	useMemo,
	useRef,
	useState,
	type CSSProperties,
	type ReactNode,
} from "react";
import { getApiBaseUrl } from "../lib/api-client";
import type { AdfNode, JiraIssue } from "../hooks/useSessionJiraContext";
import { AttachmentChip } from "./JiraAdf";
import { MediaLightbox, MediaThumb, type MediaItem } from "./MediaLightbox";

type JiraAttachment = NonNullable<JiraIssue["attachments"]>[number];

/** Daemon URL for one Jira attachment's bytes. Fetched (connect-src) and rendered
 * from a blob: object URL — a direct http://127.0.0.1 subresource is CSP-blocked
 * on the app:// scheme (see src/shared/renderer-csp.ts). */
export function attachmentUrl(sessionId: string, attachmentId: string): string {
	return `${getApiBaseUrl()}/api/v1/sessions/${encodeURIComponent(sessionId)}/jira/attachments/${encodeURIComponent(attachmentId)}`;
}

type Ctx = {
	resolve: (node: AdfNode) => { item: MediaItem; index: number } | null;
	open: (index: number, trigger: HTMLElement) => void;
};

const JiraMediaCtx = createContext<Ctx | null>(null);

/** Access the current card's media resolver, or null when JiraAdf renders outside
 * a provider (e.g. the pre-session Browse Jira detail dialog) — media then stays a
 * filename chip. */
export function useJiraMedia() {
	return useContext(JiraMediaCtx);
}

/** The display name the backend normalized a media node with (from ADF alt/title). */
function nodeFilename(node: AdfNode): string {
	return (node.attrs?.filename ?? "").trim();
}

function matchAttachment(byName: Map<string, JiraAttachment>, node: AdfNode): JiraAttachment | undefined {
	if (node.type !== "media" && node.type !== "mediaInline") return undefined;
	const name = nodeFilename(node);
	return name ? byName.get(name) : undefined;
}

/** Index the issue's attachments by filename (first-wins) for node→attachment matching. */
function indexByFilename(attachments: JiraAttachment[]): Map<string, JiraAttachment> {
	const m = new Map<string, JiraAttachment>();
	for (const a of attachments) {
		const f = (a.filename ?? "").trim();
		if (f && !m.has(f)) m.set(f, a);
	}
	return m;
}

/** Collect description media nodes matched to an attachment (by filename), in
 * document order and deduped by attachment id — the lightbox's prev/next set. */
function collectMedia(
	nodes: AdfNode[] | undefined,
	byName: Map<string, JiraAttachment>,
	sessionId: string,
): MediaItem[] {
	if (!nodes) return [];
	const seen = new Set<string>();
	const items: MediaItem[] = [];
	const walk = (list: AdfNode[]) => {
		for (const n of list) {
			const att = matchAttachment(byName, n);
			if (att && !seen.has(att.id)) {
				seen.add(att.id);
				items.push({ id: att.id, filename: att.filename, mime: att.mimeType, src: attachmentUrl(sessionId, att.id) });
			}
			if (n.content) walk(n.content);
		}
	};
	walk(nodes);
	return items;
}

/**
 * Wraps a JiraAdf render so its description media nodes become inline previews.
 * Builds the ordered media set once (matched to attachments by filename) and owns
 * a single shared MediaLightbox, so paging spans every previewable item in the
 * card. Media without a matching attachment stays a filename chip.
 */
export function JiraMediaProvider({
	sessionId,
	description,
	attachments,
	children,
}: {
	sessionId: string;
	description?: AdfNode[];
	attachments?: JiraAttachment[];
	children: ReactNode;
}) {
	const byName = useMemo(() => indexByFilename(attachments ?? []), [attachments]);
	const items = useMemo(() => collectMedia(description, byName, sessionId), [description, byName, sessionId]);
	const [openIndex, setOpenIndex] = useState<number | null>(null);
	const triggerRef = useRef<HTMLElement | null>(null);

	const byId = useMemo(() => {
		const m = new Map<string, number>();
		items.forEach((it, i) => m.set(it.id, i));
		return m;
	}, [items]);

	const resolve = useCallback(
		(node: AdfNode) => {
			const att = matchAttachment(byName, node);
			if (!att) return null;
			const index = byId.get(att.id);
			if (index == null) return null;
			return { item: items[index], index };
		},
		[byName, byId, items],
	);

	const open = useCallback((index: number, trigger: HTMLElement) => {
		triggerRef.current = trigger;
		setOpenIndex(index);
	}, []);

	const ctx = useMemo(() => ({ resolve, open }), [resolve, open]);

	return (
		<JiraMediaCtx.Provider value={ctx}>
			{children}
			{openIndex !== null && (
				<MediaLightbox
					items={items}
					index={openIndex}
					onIndexChange={setOpenIndex}
					onClose={() => setOpenIndex(null)}
					triggerRef={triggerRef}
				/>
			)}
		</JiraMediaCtx.Provider>
	);
}

// Inline-preview sizing: constrained so a preview fits an ADF table cell without
// breaking layout; object-fit keeps aspect, the height cap tames tall images.
const THUMB_STYLE: CSSProperties = {
	maxWidth: "100%",
	width: 168,
	maxHeight: 128,
	borderRadius: 6,
	border: "1px solid var(--border, rgba(255,255,255,.12))",
	objectFit: "cover",
	background: "#000",
	display: "block",
};

/** One description media node: an inline preview when a matching attachment exists,
 * else the original filename chip (unchanged fallback). */
export function JiraMediaNode({ node }: { node: AdfNode }) {
	const media = useJiraMedia();
	const hit = media?.resolve(node) ?? null;
	if (!media || !hit) {
		return <AttachmentChip filename={nodeFilename(node)} />;
	}
	return (
		<span className="jira-adf__media-thumb">
			<MediaThumb item={hit.item} onOpen={(el) => media.open(hit.index, el)} style={THUMB_STYLE} />
		</span>
	);
}
