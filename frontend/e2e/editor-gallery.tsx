// ⚠️ First, before anything that reaches `lib/api-client`: the stub has to
// replace `window.fetch` before openapi-fetch captures it. See the module.
import { GALLERY_PATH } from "./editor-gallery-api-stub";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useEffect } from "react";
import { createRoot } from "react-dom/client";
import { WorkspaceFileView } from "../src/renderer/components/WorkspaceFileView";
import { monaco } from "../src/renderer/lib/monaco-setup";
import { useUiStore } from "../src/renderer/stores/ui-store";
import "../src/renderer/styles.css";

// The spec reaches the live editor through Monaco's own registry.
(window as unknown as { __monaco: typeof monaco }).__monaco = monaco;

// The editor sits between the sidebar and the inspector rail, so the widths that
// matter are narrower than the window. `?width=` reproduces one exactly.
const params = new URLSearchParams(window.location.search);
const width = Number(params.get("width") ?? 900);
const initialTheme = params.get("theme") === "light" ? "light" : "dark";

const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });

function Gallery() {
	// Through the real store, not local state: the viewer reads the theme from
	// `useUiStore`, and `_shell` is what mirrors it onto <html>. A harness that
	// only set the attribute would leave Monaco on whatever the OS prefers.
	const theme = useUiStore((s) => s.theme);
	const setTheme = useUiStore((s) => s.setTheme);
	useEffect(() => setTheme(initialTheme), [setTheme]);
	useEffect(() => {
		document.documentElement.dataset.theme = theme;
		document.documentElement.style.colorScheme = theme;
	}, [theme]);
	return (
		<QueryClientProvider client={client}>
			<div style={{ display: "flex", flexDirection: "column", height: "100vh", background: "var(--bg)" }}>
				<button type="button" data-testid="toggle-theme" onClick={() => setTheme(theme === "dark" ? "light" : "dark")}>
					theme: {theme}
				</button>
				<div style={{ display: "flex", flex: 1, minHeight: 0 }}>
					<div data-testid="editor-frame" style={{ width, flex: "none", display: "flex", minHeight: 0 }}>
						<WorkspaceFileView sessionId="gallery" path={GALLERY_PATH} line={26} onClose={() => {}} />
					</div>
					<div style={{ flex: 1, background: "var(--bg-1)" }} />
				</div>
			</div>
		</QueryClientProvider>
	);
}

createRoot(document.getElementById("root") as HTMLElement).render(<Gallery />);
