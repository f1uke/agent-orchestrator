import { defineConfig } from "vite";

export default defineConfig({
	server: { host: "::", port: 5199, strictPort: true },
	build: { outDir: "dist", sourcemap: false, reportCompressedSize: true },
});
