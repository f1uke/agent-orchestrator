import { createContext, useContext, type ReactNode } from "react";

/** What the host lets the Releases page do. The desktop app grants everything; a read-only host sets readOnly. */
export type ReleasesCapabilities = { readOnly: boolean };

const Capabilities = createContext<ReleasesCapabilities>({ readOnly: false });

export function ReleasesCapabilitiesProvider({
	readOnly = false,
	children,
}: {
	readOnly?: boolean;
	children: ReactNode;
}) {
	return <Capabilities.Provider value={{ readOnly }}>{children}</Capabilities.Provider>;
}

/** True when the page must show the Board and offer no write: no switch, hide, fix, plan edit, No MR mark or refresh. */
export function useReleasesReadOnly(): boolean {
	return useContext(Capabilities).readOnly;
}

export class ReadOnlyError extends Error {
	constructor() {
		super("The Releases page is read-only here.");
		this.name = "ReadOnlyError";
	}
}
