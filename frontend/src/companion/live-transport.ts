import { displaySessionName } from "../renderer/lib/session-title";
import type { LiveFeedDeps } from "./live-feed";
import type { LiveSession } from "./live-roster";

// The actual wires: one REST poll for the roster, one SSE for the activity.
//
// Kept apart from `live-feed.ts` so the feed's logic — decay, composition, the
// reconnect policy — is testable without a socket, and so this file has nothing in
// it but transport.
//
// A session row carries NO human name and NO project name: the board derives both,
// from `displayName`/`issueId` and from a join on the projects list. The overlay
// derives them the SAME way — through the board's own `displaySessionName` — so a
// Proc is labelled with exactly the words on the card it stands for, and the
// session id stays what it is on the board: the last resort.

type SessionsResponse = { sessions?: Array<Record<string, unknown>> };
type ProjectsResponse = { projects?: Array<Record<string, unknown>> };

function text(value: unknown): string | undefined {
	return typeof value === "string" ? value : undefined;
}

function toLiveSession(raw: Record<string, unknown>, projects: Map<string, string>): LiveSession | null {
	const id = text(raw.id);
	const status = text(raw.status);
	if (!id || !status) return null;
	return {
		id,
		name: displaySessionName({ displayName: text(raw.displayName), issueId: text(raw.issueId), id }),
		projectName: projects.get(text(raw.projectId) ?? ""),
		// Anything that is not the coordinator is doing the work, whatever it calls
		// itself — the overlay draws exactly one distinction here.
		kind: raw.kind === "orchestrator" ? "orchestrator" : "worker",
		status: status as LiveSession["status"],
		statusReason: text(raw.statusReason) as LiveSession["statusReason"] | undefined,
		isTerminated: raw.isTerminated === true,
	};
}

/** REST + SSE against a daemon base URL, as the deps `createLiveFeed` expects. */
export function createHttpTransport(baseUrl: string): Omit<LiveFeedDeps, "now"> {
	// A project's name never changes under us often enough to be worth re-reading on
	// every 4s roster poll, but it is cheap and self-healing to refresh alongside it.
	const fetchProjects = async (): Promise<Map<string, string>> => {
		try {
			const response = await fetch(`${baseUrl}/api/v1/projects`);
			if (!response.ok) return new Map();
			const body = (await response.json()) as ProjectsResponse;
			return new Map(
				(body.projects ?? [])
					.map((raw) => [text(raw.id), text(raw.name)] as const)
					.filter((pair): pair is readonly [string, string] => Boolean(pair[0] && pair[1])),
			);
		} catch {
			// A project we cannot name is a Proc with a blank tooltip line. A Proc we
			// cannot show at all is a session that has vanished off the desktop — so
			// this failure must never take the roster down with it.
			return new Map();
		}
	};

	return {
		async fetchSessions() {
			const [response, projects] = await Promise.all([fetch(`${baseUrl}/api/v1/sessions`), fetchProjects()]);
			if (!response.ok) throw new Error(`sessions ${response.status}`);
			const body = (await response.json()) as SessionsResponse;
			return (body.sessions ?? [])
				.map((raw) => toLiveSession(raw, projects))
				.filter((session): session is LiveSession => session !== null);
		},
		openStream(onFrame, onError) {
			// The activity Hub's SSE — NOT the CDC `/events` stream, which fires only on
			// an activity_state change and so emits nothing during a burst of tool calls.
			const source = new EventSource(`${baseUrl}/api/v1/activity/stream`);
			const handle = (event: MessageEvent) => {
				try {
					onFrame(JSON.parse(event.data));
				} catch {
					// A frame we cannot parse tells us nothing; it must not take the
					// overlay down with it.
				}
			};
			source.addEventListener("activity", handle as EventListener);
			source.addEventListener("error", () => onError());
			return () => {
				source.removeEventListener("activity", handle as EventListener);
				source.close();
			};
		},
	};
}
