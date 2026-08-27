// ⚠️ First, before anything that reaches `lib/api-client`: the stub has to
// replace `window.fetch` before openapi-fetch captures it. See the module.
import { GALLERY_PATH } from "./editor-gallery-api-stub";
import { GALLERY_WORKSPACE_ROOT, installFakeLspBridge } from "./editor-gallery-lsp-stub";
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
// Monaco renders only the lines in view, so a spec that measures a token has to
// be able to say which part of the file to be looking at.
const line = Number(params.get("line") ?? 26);
// `?lsp=1` gives the page a language server. Off by default so every other spec
// keeps measuring the grammar alone, which is what they were written against.
const withLsp = params.get("lsp") === "1";
// `?lspDelay=` makes the fake as slow as a cold sourcekit-lsp (measured 400 -
// 1 333 ms for the first completion in a file), which is the only way to see
// the serialisation policy from outside. `?lspFail=` makes attach reject, so a
// spec can ask what ⌃Space says when there is no server at all.
if (withLsp) {
	installFakeLspBridge({
		completionDelayMs: params.has("lspDelay") ? Number(params.get("lspDelay")) : undefined,
		failAttach: params.get("lspFail") ?? undefined,
	});
}

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
					{/* A plain BLOCK box, deliberately: in the app the viewer sits in
					    `<div className="min-h-0 flex-1">` inside a flex COLUMN, so it fills
					    the pane's width. Making this a flex ROW instead let the viewer
					    shrink-wrap to its content (~630px) and silently ignore `?width=`
					    above that — every "at 1240px" measurement was really at 630px. */}
					<div data-testid="editor-frame" style={{ width, flex: "none", minHeight: 0 }}>
						<WorkspaceFileView
							sessionId="gallery"
							path={GALLERY_PATH}
							line={line}
							workspaceRoot={withLsp ? GALLERY_WORKSPACE_ROOT : undefined}
							onClose={() => {}}
						/>
					</div>
					<div style={{ flex: 1, background: "var(--bg-1)" }} />
				</div>
			</div>
		</QueryClientProvider>
	);
}

createRoot(document.getElementById("root") as HTMLElement).render(<Gallery />);
