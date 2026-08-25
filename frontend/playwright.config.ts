import { defineConfig } from "@playwright/test";

// Overridable because several AO worktrees of this repo are often open at once,
// and `reuseExistingServer` cannot tell one repo's dev server from another's: a
// second checkout serving 5173 is silently reused, and every spec then runs
// against the wrong tree's code. Set AO_E2E_PORT to claim a port of your own.
const PORT = Number(process.env.AO_E2E_PORT ?? 5173);

export default defineConfig({
	testDir: "e2e",
	use: {
		baseURL: `http://127.0.0.1:${PORT}`,
	},
	webServer: {
		// dev:web serves the renderer alone (VITE_NO_ELECTRON=1) — no Electron child to
		// launch, which is all the browser-based e2e suite needs.
		//
		// --host 127.0.0.1 is load-bearing on macOS: vite's default `localhost` binds
		// ::1 there, and every request to the 127.0.0.1 baseURL above is refused.
		command: `npm run dev:web -- --port ${PORT} --host 127.0.0.1 --strictPort`,
		port: PORT,
		reuseExistingServer: !process.env.CI,
	},
});
