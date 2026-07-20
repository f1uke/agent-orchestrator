import { describe, expect, it } from "vitest";
import { fileKindFor } from "./file-kind";

describe("fileKindFor", () => {
	it("classifies source files as code, whatever the language", () => {
		expect(fileKindFor("src/a.tsx")).toBe("code");
		expect(fileKindFor("backend/internal/service/session/workspace_changes.go")).toBe("code");
		expect(fileKindFor("NterApp/NotificationService.swift")).toBe("code");
	});

	it("separates structured data, prose, styling and assets", () => {
		expect(fileKindFor("fixtures/sessions_golden.json")).toBe("data");
		expect(fileKindFor("docs/README.md")).toBe("doc");
		expect(fileKindFor("src/renderer/styles.css")).toBe("style");
		expect(fileKindFor("assets/ao-dashboard-preview.png")).toBe("image");
	});

	// Judged by the file NAME, so a path containing ".go/" or similar cannot
	// mislead it.
	it("reads the extension from the file name, not the directory", () => {
		expect(fileKindFor("weird.css/actual.go")).toBe("code");
	});

	it("treats dotfiles and well-known bare names as config", () => {
		expect(fileKindFor(".gitignore")).toBe("config");
		expect(fileKindFor("deploy/Dockerfile")).toBe("config");
		expect(fileKindFor("go.mod")).toBe("config");
		expect(fileKindFor("frontend/package.json")).toBe("config");
	});

	it("falls back to other for an unknown or missing extension", () => {
		expect(fileKindFor("LICENSE")).toBe("other");
		expect(fileKindFor("data/blob.xyzzy")).toBe("other");
	});

	it("ignores case in the extension", () => {
		expect(fileKindFor("IMAGE.PNG")).toBe("image");
	});
});
