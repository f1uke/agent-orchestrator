import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { FilesPanel } from "../src/renderer/components/FilesPanel";
import { useUiStore } from "../src/renderer/stores/ui-store";
import { largeWorkspacePaths } from "./large-workspace-fixture";
import "../src/renderer/styles.css";

const params = new URLSearchParams(window.location.search);
const count = Number(params.get("n") ?? 6940);
const paths = largeWorkspacePaths(count);

/**
 * The worst result set the ⌘⇧F panel can ever be handed: the server's caps,
 * exactly — 2,000 matches spread over 500 files. "self" on the human's real iOS
 * project matches 12,847 times in 1,570 files, so this is what the panel gets
 * asked to draw for an ordinary term on an ordinary repo.
 */
function benchSearchResults(query: string) {
	const MAX_MATCHES = 2000;
	const MAX_FILES = 500;
	const perFile = MAX_MATCHES / MAX_FILES;
	const files = Array.from({ length: MAX_FILES }, (_, i) => {
		const path = paths[(i * 13) % paths.length];
		const matches = Array.from({ length: perFile }, (_, m) => {
			const preview = `    private let ${query}Controller = ${query}Controller(store: store) // ${i}:${m}`;
			const at = preview.indexOf(query);
			return {
				line: 40 * m + i + 1,
				column: at + 1,
				endColumn: at + query.length + 1,
				preview,
				previewStart: at,
				previewEnd: at + query.length,
			};
		});
		return { path, matches, total: 27, truncated: true };
	});
	return {
		available: true,
		query,
		files,
		totalMatches: 12847,
		totalFiles: 1570,
		filesSearched: 4488,
		truncated: true,
	};
}

// Registered on the same seam the editor gallery uses, because `dev:web` serves
// mock data and issues no request at all — a fetch stub would never be reached.
(globalThis as { __aoMockWorkspaceFile?: unknown }).__aoMockWorkspaceFile = {
	files: () => ({ available: true, truncated: false, paths }),
	search: (_sessionId: string, query: string) => (query.trim() === "" ? null : benchSearchResults(query)),
};

// The panel remembers its mode in localStorage; the bench always wants Browse.
try {
	window.localStorage.setItem("ao.files.mode", "browse");
	window.localStorage.setItem("ao.files.view", params.get("view") === "list" ? "list" : "tree");
} catch {
	// A harness that cannot persist still renders the right default below.
}

const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });

function Bench() {
	const setTheme = useUiStore((s) => s.setTheme);
	const [mounted, setMounted] = useState(false);
	useEffect(() => {
		setTheme("dark");
		document.documentElement.dataset.theme = "dark";
	}, [setTheme]);
	return (
		<QueryClientProvider client={client}>
			<div style={{ display: "flex", height: "100vh", background: "var(--bg)" }}>
				<div style={{ width: 330, flex: "none", display: "flex", flexDirection: "column", minHeight: 0 }}>
					{mounted ? <FilesPanel sessionId="bench" taskKey="bench-task" /> : null}
				</div>
				<div style={{ flex: 1 }}>
					<button type="button" data-testid="bench-mount" onClick={() => setMounted(true)}>
						mount ({paths.length} files)
					</button>
					{/* Leaving the Files tab and coming back is an UNMOUNT — which is
					    what makes the rail's remembered arrangement observable at all. */}
					<button type="button" data-testid="bench-unmount" onClick={() => setMounted(false)}>
						unmount
					</button>
					<span data-testid="bench-count">{paths.length}</span>
				</div>
			</div>
		</QueryClientProvider>
	);
}

createRoot(document.getElementById("root") as HTMLElement).render(<Bench />);
