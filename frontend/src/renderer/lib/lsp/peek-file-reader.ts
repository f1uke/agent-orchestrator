import { apiClient } from "../api-client";
import { modelTextFrom } from "../editor/save-file";

/**
 * Reads a file so a peek widget has something to show in its preview pane.
 *
 * Goes through the daemon's ordinary workspace-file route, which is the same one
 * ⌘click into GOROOT or into a Pod already uses: the READ route is deliberately
 * unconfined (the write route deliberately is not), so an absolute target
 * outside the session's worktree resolves exactly as a definition jump to it
 * does. No second file-reading path is introduced, and in particular no new
 * unconfined IPC surface.
 *
 * 🗝 Returns `null` rather than throwing or inventing text for every file that
 * cannot be shown — too large, binary, deleted since the server indexed it. An
 * empty model would render as an empty file, which is a worse answer than
 * Monaco's own `File.swift:12:5` row: one is a wrong preview, the other is an
 * honest absence of one.
 */
export function peekFileReader(sessionId: string): (absolutePath: string) => Promise<string | null> {
	return async (absolutePath) => {
		const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/workspace/file", {
			params: { path: { sessionId }, query: { path: absolutePath } },
		});
		if (error || !data?.available || !data.lines) return null;
		return modelTextFrom(data.lines);
	};
}
