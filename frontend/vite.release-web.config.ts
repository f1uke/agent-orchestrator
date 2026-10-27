import { renameSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

const outDir = process.env.RELEASE_WEB_OUT_DIR ?? "daemon/release-web";

const servedAsIndex: Plugin = {
	name: "release-web-index",
	writeBundle(options) {
		const dir = options.dir ?? outDir;
		renameSync(join(dir, "release-web.html"), join(dir, "index.html"));
	},
};

export default defineConfig({
	plugins: [react(), tailwindcss(), servedAsIndex],
	resolve: {
		alias: {
			"@": fileURLToPath(new URL("./src/renderer", import.meta.url)),
		},
	},
	build: {
		outDir,
		emptyOutDir: true,
		rollupOptions: {
			input: { index: fileURLToPath(new URL("./release-web.html", import.meta.url)) },
		},
	},
});
