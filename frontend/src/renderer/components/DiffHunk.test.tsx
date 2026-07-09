import { QueryClientProvider, QueryClient } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock },
	apiErrorMessage: (e: unknown, fb = "Request failed") => (e instanceof Error ? e.message : fb),
}));

import { DiffHunk } from "./DiffHunk";

function renderHunk() {
	const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={qc}>
			<DiffHunk sessionId="s1" prUrl="pr1" path="a.go" line={2} />
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	getMock.mockReset().mockImplementation(async (_path, opts) => {
		const mode = opts?.params?.query?.mode ?? "hunk";
		if (mode === "file") {
			return {
				data: {
					available: true,
					mode: "file",
					path: "a.go",
					truncated: false,
					lines: [
						{ kind: "context", oldLine: 1, newLine: 1, text: "l1" },
						{ kind: "context", oldLine: 2, newLine: 2, text: "CHANGED" },
					],
				},
				error: undefined,
			};
		}
		return {
			data: {
				available: true,
				mode: "hunk",
				path: "a.go",
				truncated: false,
				lines: [{ kind: "add", oldLine: 0, newLine: 2, text: "CHANGED" }],
			},
			error: undefined,
		};
	});
});

describe("DiffHunk", () => {
	it("renders the hunk lines", async () => {
		renderHunk();
		expect(await screen.findByText("CHANGED")).toBeInTheDocument();
	});

	it("expands to the full file on click", async () => {
		renderHunk();
		await screen.findByText("CHANGED");
		await userEvent.click(screen.getByRole("button", { name: /expand/i }));
		await waitFor(() => expect(screen.getByText("l1")).toBeInTheDocument());
	});
});
