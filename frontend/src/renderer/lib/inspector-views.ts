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

/** The rail tabs a session shows, in strip order. */
export function allowedInspectorViews(session: WorkspaceSession, flags: InspectorViewFlags = {}): InspectorView[] {
	const isChat = session.transport === "chat";
	if (isOrchestratorSession(session)) return isChat ? ["document", "tasks"] : [];
	return INSPECTOR_VIEWS.filter((view) => {
		switch (view) {
			case "document":
			case "tasks":
				return isChat;
			case "testiny":
				return Boolean(flags.usesTestiny);
			case "simulator":
				return Boolean(flags.hasIOSSimulator);
			case "browser":
				return Boolean(flags.hasWebUI);
			default:
				return true;
		}
	});
}
