import type { ReactNode } from "react";

export function ChatRefsProvider({ children }: { sessionId: string; children: ReactNode }) {
	return <>{children}</>;
}

export function RefImages(_props: { source: string }) {
	return null;
}
