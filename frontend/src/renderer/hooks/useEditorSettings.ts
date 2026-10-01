import { useQuery } from "@tanstack/react-query";
import { DEFAULT_EDITOR_SETTINGS, type EditorSettings } from "../../shared/editor-settings";
import { editorSettingsBridge } from "../lib/editor/formatting/format-bridge";

/** Invalidated by the Settings page's save, so every open editor picks a change up at once. */
export const editorSettingsQueryKey = ["settings", "editor"] as const;

/**
 * How the code editor indents and formats. The defaults until the file has been
 * read - and for good if it cannot be: indentation must never wait on a
 * preference.
 */
export function useEditorSettings(): EditorSettings {
	const query = useQuery({
		queryKey: editorSettingsQueryKey,
		queryFn: () => editorSettingsBridge().get(),
		staleTime: Number.POSITIVE_INFINITY,
	});
	return query.data ?? DEFAULT_EDITOR_SETTINGS;
}
