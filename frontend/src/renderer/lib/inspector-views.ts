import { isOrchestratorSession, type WorkspaceSession } from "../types/workspace";

export const INSPECTOR_VIEWS = [
	"summary",
	"reviews",
	"files",
	"document",
	"tasks",
	"testiny",
	"simulator",
	"browser",
] as const;

export type InspectorView = (typeof INSPECTOR_VIEWS)[number];

/** Project switches that turn on the opt-in tabs of a worker's rail. */
export type InspectorViewFlags = {
	hasWebUI?: boolean;
	hasIOSSimulator?: boolean;
	usesTestiny?: boolean;
};

/**
 * The rail tabs a session shows, in strip order. The one place that decides it.
 *
 * An orchestrator's workspace is the project checkout, with no branch to diff,
 * no PR to read and no app of its own to preview. Its chat can still open
 * documents and run background tasks, so a chat orchestrator gets those two tabs
 * whatever the project enables. A terminal orchestrator has no chat to open
 * anything from and gets no rail, which is the empty list.
 */
export function allowedInspectorViews(
	session: WorkspaceSession,
	flags: InspectorViewFlags = {},
): InspectorView[] {
	const isChat = session.transport === "chat";
	if (isOrchestratorSession(session)) return isChat ? ["document", "tasks"] : [];
	return INSPECTOR_VIEWS.filter((view) => {
		switch (view) {
			case "document":
			case "tasks":
				// Documents come from chat cards, and tasks from the chat's agent.
				return isChat;
			case "testiny":
				return Boolean(flags.usesTestiny);
			case "simulator":
				return Boolean(flags.hasIOSSimulator);
			case "browser":
				// A project with no web UI has nothing to preview, so an always-empty
				// tab is worse than one tab fewer.
				return Boolean(flags.hasWebUI);
			default:
				return true;
		}
	});
}
