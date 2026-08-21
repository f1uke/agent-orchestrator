import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { WorkspaceSession } from "../types/workspace";

const { postMock } = vi.hoisted(() => ({ postMock: vi.fn() }));

vi.mock("../lib/api-client", () => ({
	apiClient: { POST: postMock },
	apiErrorMessage: (error: unknown, fallback = "Request failed") => (error instanceof Error ? error.message : fallback),
}));
vi.mock("../lib/telemetry", () => ({ captureRendererEvent: vi.fn() }));
vi.mock("../hooks/useWorkspaceQuery", () => ({ workspaceQueryKey: ["workspaces"] }));

import { CrewBatonBar } from "./CrewBatonBar";

function member(id: string, role: "dev" | "qa", over: Partial<WorkspaceSession> = {}): WorkspaceSession {
	return {
		id,
		workspaceId: "proj-1",
		workspaceName: "my-app",
		title: "build the thing",
		provider: "claude-code",
		kind: "worker",
		branch: "feature/task",
		status: "working",
		updatedAt: "2026-08-21T00:00:00Z",
		prs: [],
		crew: { id: "dev-1", role, hasRun: true },
		...over,
	};
}

/** The sleeping qa whose card is open, plus a dev in whatever state is under test. */
function renderBar(dev: WorkspaceSession) {
	const qa = member("qa-1", "qa", { isSuspended: true, sleepReason: "turn" });
	const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={qc}>
			<CrewBatonBar session={qa} sessions={[dev, qa]} />
		</QueryClientProvider>,
	);
}

describe("CrewBatonBar", () => {
	beforeEach(() => {
		postMock.mockReset();
		postMock.mockResolvedValue({ error: undefined });
	});

	// "(sleeps dev)" is a promise about what pressing this does, so it may only be
	// made against a member that is actually running.
	it("promises to sleep the other member only while it is awake", () => {
		renderBar(member("dev-1", "dev"));
		expect(screen.getByRole("button", { name: "Wake qa (sleeps dev)" })).toBeInTheDocument();
		expect(screen.getByText(/dev has the turn/)).toBeInTheDocument();
	});

	// The state the human hit on the real screen: dev's PR merged, so dev is gone -
	// waking qa sleeps nobody, and the daemon takes the ordinary resume path.
	it.each([
		["finished", { isTerminated: true, status: "terminated" as const }],
		["merged", { status: "merged" as const }],
		["already asleep", { isSuspended: true, sleepReason: "turn" as const }],
		["not started", { isTodo: true, status: "todo" as const }],
	])("does not claim to sleep a %s member", (_name, over) => {
		renderBar(member("dev-1", "dev", over));
		expect(screen.getByRole("button", { name: "Wake qa" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /sleeps/ })).not.toBeInTheDocument();
		expect(screen.getByText(/nobody has the turn on this task/)).toBeInTheDocument();
	});
});
