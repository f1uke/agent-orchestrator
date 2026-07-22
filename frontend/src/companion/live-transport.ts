import type { LiveFeedDeps } from "./live-feed";
import type { LiveSession } from "./live-roster";

// The actual wires: one REST poll for the roster, one SSE for the activity.
//
// Kept apart from `live-feed.ts` so the feed's logic — decay, composition, the
// reconnect policy — is testable without a socket, and so this file has nothing in
// it but transport.

type SessionsResponse = { sessions?: Array<Record<string, unknown>> };

function toLiveSession(raw: Record<string, unknown>): LiveSession | null {
	const id = typeof raw.id === "string" ? raw.id : null;
	const status = typeof raw.status === "string" ? raw.status : null;
	if (!id || !status) return null;
	return {
		id,
		name: typeof raw.name === "string" ? raw.name : id,
		projectName: typeof raw.projectName === "string" ? raw.projectName : undefined,
		status: status as LiveSession["status"],
		statusReason: typeof raw.statusReason === "string" ? (raw.statusReason as LiveSession["statusReason"]) : undefined,
		isTerminated: raw.isTerminated === true,
	};
}

/** REST + SSE against a daemon base URL, as the deps `createLiveFeed` expects. */
export function createHttpTransport(baseUrl: string): Omit<LiveFeedDeps, "now"> {
	return {
		async fetchSessions() {
			const response = await fetch(`${baseUrl}/api/v1/sessions`);
			if (!response.ok) throw new Error(`sessions ${response.status}`);
			const body = (await response.json()) as SessionsResponse;
			return (body.sessions ?? []).map(toLiveSession).filter((s): s is LiveSession => s !== null);
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
