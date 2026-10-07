import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ScriptsStoreRefusal, UncommittedFile } from "../lib/kill-session";

vi.mock("../lib/api-client", () => ({ apiClient: { POST: vi.fn() }, apiErrorMessage: () => "failed" }));
vi.mock("../lib/telemetry", () => ({ captureRendererEvent: vi.fn() }));
vi.mock("../hooks/useWorkspaceQuery", () => ({ workspaceQueryKey: ["workspaces"] }));

import { UndeliveredWorkDialog } from "./UndeliveredWorkDialog";

const store: ScriptsStoreRefusal = {
	path: "/data/store-worktrees/mobile-ui-scripts/nter-7",
	branch: "ao/nter-7",
	baseBranch: "main",
	uncommitted: ["projects/nter/draft.yaml"],
	publishRefused: {
		hold: "publish_conflict",
		detail: "ao/nter-7 conflicts with main",
		files: ["projects/nter/login.yaml"],
	},
};

function renderDialog(files: UncommittedFile[], scriptsStore?: ScriptsStoreRefusal) {
	const qc = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={qc}>
			<UndeliveredWorkDialog
				open
				onOpenChange={() => {}}
				sessionId="nter-7"
				sessionTitle="login flow script"
				files={files}
				scriptsStore={scriptsStore}
			/>
		</QueryClientProvider>,
	);
}

describe("UndeliveredWorkDialog's scripts store section", () => {
	it("lists the store worktree's files and the refused publish when that is all the refusal names", () => {
		renderDialog([], store);
		const section = document.querySelector("[data-undelivered-scripts-store]") as HTMLElement;
		expect(section).not.toBeNull();
		expect(within(section).getByText("Scripts store")).toBeInTheDocument();
		expect(within(section).getByText(store.path)).toBeInTheDocument();
		expect(within(section).getByText("projects/nter/draft.yaml")).toBeInTheDocument();
		expect(within(section).getByText("projects/nter/login.yaml")).toBeInTheDocument();
		expect(within(section).getByText(/ao\/nter-7 conflicts with main/)).toBeInTheDocument();
		expect(screen.getByText(/has scripts in its scripts store worktree/)).toBeInTheDocument();
		expect(screen.queryByText(/The daemon named no files. Open the session/)).toBeNull();
		expect(screen.getByText(/This cannot be undone/)).toBeInTheDocument();
	});

	it("names it beside the worktree's own files, so a discard is confirmed against both", () => {
		renderDialog([{ path: "Sources/App.swift", status: "modified" }], store);
		expect(screen.getByText("Sources/App.swift")).toBeInTheDocument();
		expect(document.querySelector("[data-undelivered-scripts-store]")).not.toBeNull();
		expect(screen.getByText(/The scripts store worktree and its branch are deleted/)).toBeInTheDocument();
	});

	it("is absent when the refusal does not name the store", () => {
		renderDialog([{ path: "Sources/App.swift", status: "modified" }]);
		expect(document.querySelector("[data-undelivered-scripts-store]")).toBeNull();
	});
});
