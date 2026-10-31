import { ArrowUpRight, FileText } from "lucide-react";
import { createContext, type ReactNode, useContext, useMemo } from "react";
import { type ArtifactDoc, docMeta, docTag, type FileDoc, fileDocOf, type TurnDocs } from "../lib/chat-docs";
import { isShowablePath } from "../lib/chat-refs";
import type { ResolvedFile } from "../lib/chat-refs-store";
import { cn } from "../lib/utils";
import { useResolved } from "./RefImages";

type Docs = { onOpen?: (doc: FileDoc) => void; openDoc: FileDoc | null };

const DocsContext = createContext<Docs | null>(null);

/** Where an Open press goes, and which document is open now, for the cards and the Write rows under it. */
export function ChatDocsProvider({
	onOpen,
	openDoc = null,
	children,
}: {
	onOpen?: (doc: FileDoc) => void;
	openDoc?: FileDoc | null;
	children: ReactNode;
}) {
	const value = useMemo(() => ({ onOpen, openDoc }), [onOpen, openDoc]);
	return <DocsContext.Provider value={value}>{children}</DocsContext.Provider>;
}

const sameFile = (a: Pick<FileDoc, "root" | "path">, b: Pick<FileDoc, "root" | "path">) =>
	a.root === b.root && a.path === b.path;

/** Images stay with RefImages, so a card is never drawn for one. */
function cardFiles(refs: readonly string[], known: ReadonlyMap<string, ResolvedFile[]>): FileDoc[] {
	const docs: FileDoc[] = [];
	for (const ref of refs) {
		for (const match of known.get(ref) ?? []) {
			if (match.kind === "image") continue;
			const doc = fileDocOf(match);
			if (!docs.some((d) => sameFile(d, doc))) docs.push(doc);
		}
	}
	return docs;
}

/**
 * The cards of a finished turn, under its last reply: a file the daemon confirmed opens in the Document
 * tab, a claude.ai artifact opens in the browser. A ref the daemon cannot confirm draws nothing.
 */
export function ChatDocStrip({ docs }: { docs: TurnDocs }) {
	const resolved = useResolved(docs.refs);
	const context = useContext(DocsContext);
	const files = useMemo(() => (resolved ? cardFiles(docs.refs, resolved.known) : []), [resolved, docs.refs]);
	if (!resolved || (files.length === 0 && docs.artifacts.length === 0)) return null;
	return (
		<div className="chat-docs" data-testid="chat-docs">
			{files.map((doc) => (
				<FileCard
					doc={doc}
					isOpen={context?.openDoc != null && sameFile(context.openDoc, doc)}
					key={`${doc.root}:${doc.path}`}
					onOpen={context?.onOpen}
				/>
			))}
			{docs.artifacts.map((artifact) => (
				<ArtifactCard artifact={artifact} key={artifact.url} />
			))}
		</div>
	);
}

function Tag({ doc }: { doc: FileDoc }) {
	return (
		<span aria-hidden="true" className="chat-doc__tag" data-kind={doc.kind}>
			{docTag(doc)}
		</span>
	);
}

function FileCard({ doc, isOpen, onOpen }: { doc: FileDoc; isOpen: boolean; onOpen?: (doc: FileDoc) => void }) {
	return (
		<div aria-label={doc.title} className="chat-doc" data-open={isOpen ? "true" : undefined} role="group">
			<Tag doc={doc} />
			<div className="chat-doc__text">
				<span className="chat-doc__title">{doc.title}</span>
				<span className="chat-doc__meta" title={doc.path}>
					{docMeta(doc)}
				</span>
			</div>
			<button
				aria-label={`Open ${doc.title}`}
				className="chat-doc__open"
				disabled={!onOpen}
				onClick={() => onOpen?.(doc)}
				type="button"
			>
				Open
			</button>
		</div>
	);
}

function ArtifactCard({ artifact }: { artifact: ArtifactDoc }) {
	return (
		<div aria-label={artifact.title} className="chat-doc" role="group">
			<span aria-hidden="true" className="chat-doc__tag chat-doc__tag--artifact">
				<FileText className="chat-icon" />
			</span>
			<div className="chat-doc__text">
				<span className="chat-doc__title">{artifact.title}</span>
				<span className="chat-doc__meta chat-doc__meta--wrap">
					claude.ai artifact · needs your claude.ai login · opens in the browser
				</span>
			</div>
			<a
				className={cn("chat-doc__open", "chat-doc__open--link")}
				href={artifact.url}
				rel="noreferrer noopener"
				target="_blank"
			>
				Open in browser
				<ArrowUpRight aria-hidden="true" className="chat-icon" />
			</a>
		</div>
	);
}

/**
 * The file a Write or Edit row names, once the daemon has confirmed it, and the action that opens it. The
 * ref is the path the model wrote, which the daemon resolves against the worktree.
 */
export function useDocOf(path: string | undefined): { doc: FileDoc | undefined; open: (() => void) | undefined } {
	const context = useContext(DocsContext);
	const names = useMemo(() => (path !== undefined && isShowablePath(path) ? [path] : []), [path]);
	const resolved = useResolved(names);
	return useMemo(() => {
		const match = path === undefined ? undefined : resolved?.known.get(path)?.[0];
		const doc = match && fileDocOf(match);
		const onOpen = context?.onOpen;
		return { doc, open: doc && onOpen ? () => onOpen(doc) : undefined };
	}, [path, resolved, context]);
}
