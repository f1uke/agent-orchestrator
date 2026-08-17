import { defineConfig } from "@playwright/test";

export default defineConfig({
	testDir: "e2e",
	use: {
		baseURL: "http://127.0.0.1:5173",
	},
	webServer: {
		// dev:web serves the renderer alone (VITE_NO_ELECTRON=1) — no Electron child to
		// launch, which is all the browser-based e2e suite needs.
		//
		// --host 127.0.0.1 is load-bearing on macOS: vite's default `localhost` binds
		// ::1 there, and every request to the 127.0.0.1 baseURL above is refused.
		command: "npm run dev:web -- --port 5173 --host 127.0.0.1",
		port: 5173,
		reuseExistingServer: !process.env.CI,
	},
});
