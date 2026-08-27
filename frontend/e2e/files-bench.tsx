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

// Registered on the same seam the editor gallery uses, because `dev:web` serves
// mock data and issues no request at all — a fetch stub would never be reached.
(globalThis as { __aoMockWorkspaceFile?: unknown }).__aoMockWorkspaceFile = {
	files: () => ({ available: true, truncated: false, paths }),
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
