import type { ReactNode } from "react";
import type { FileDoc, TurnDocs } from "../lib/chat-docs";

export function ChatDocsProvider({
	children,
}: {
	onOpen?: (doc: FileDoc) => void;
	openDoc?: FileDoc | null;
	children: ReactNode;
}) {
	return <>{children}</>;
}

export function ChatDocStrip(_props: { docs: TurnDocs }) {
	return null;
}

export function useDocOf(_path: string | undefined): { doc: FileDoc | undefined; open: (() => void) | undefined } {
	return { doc: undefined, open: undefined };
}
