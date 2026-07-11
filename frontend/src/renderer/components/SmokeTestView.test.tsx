import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { getMock, postMock } = vi.hoisted(() => ({
	getMock: vi.fn(),
	postMock: vi.fn(),
}));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock, POST: postMock },
	apiErrorMessage: (e: unknown, fb = "Request failed") => (e instanceof Error ? e.message : fb),
	getApiBaseUrl: () => "",
}));

import { SmokeTestView } from "./SmokeTestView";

function check(overrides: Record<string, unknown>) {
	return {
		id: "c1",
		sessionId: "s1",
		projectId: "p",
		seq: 1,
		name: "A fresh MR shows up",
		why: "confirms re-polling",
		steps: ["Open Reviews", "Open a new MR"],
		expected: "It appears automatically",
		prNum: 36,
		fileRef: "scmobserver.go:936",
		verdict: "pending",
		note: "",
		evidence: [],
		createdAt: "2026-07-11T10:00:00Z",
		updatedAt: "2026-07-11T10:00:00Z",
		...overrides,
	};
}

let checks: ReturnType<typeof check>[];

beforeEach(() => {
	checks = [check({})];
	getMock.mockReset().mockImplementation(async () => ({ data: { worker: "fix gl note", checks }, error: undefined }));
	postMock
		.mockReset()
		.mockResolvedValue({ data: { delivered: true, target: "worker", summary: "1 pass" }, error: undefined });
});

function renderView(sessionId = "s1") {
	const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={qc}>
			<SmokeTestView sessionId={sessionId} worker="fix gl note" />
		</QueryClientProvider>,
	);
}

describe("SmokeTestView", () => {
	it("renders the checklist header, subtitle, and a case card", async () => {
		checks = [
			check({ verdict: "pass", decidedAt: "2026-07-11T10:05:00Z" }),
			check({ id: "c2", seq: 2, name: "Second case" }),
		];
		renderView();
		expect(await screen.findByText("Smoke test")).toBeInTheDocument();
		expect(screen.getByText(/Checklist from/)).toBeInTheDocument();
		expect(await screen.findByText("A fresh MR shows up")).toBeInTheDocument();
		expect(screen.getByText("Second case")).toBeInTheDocument();
		// counts row: 1 of 2 verified
		expect(screen.getByText(/of 2 verified/)).toBeInTheDocument();
	});

	it("shows the empty state when there is no checklist", async () => {
		checks = [];
		renderView();
		expect(await screen.findByText("No smoke checks yet")).toBeInTheDocument();
	});

	it("expands a pending case and posts a Pass verdict with the note", async () => {
		renderView();
		// Pending cases render expanded; the note textarea + verdict buttons are visible.
		const note = await screen.findByLabelText(/Note for A fresh MR shows up/);
		await userEvent.type(note, "worked great");
		await userEvent.click(screen.getByRole("button", { name: /Works — Pass/ }));
		await waitFor(() =>
			expect(
				postMock.mock.calls.some(([p]) => p === "/api/v1/sessions/{sessionId}/smoke-checks/{checkId}/verdict"),
			).toBe(true),
		);
		const call = postMock.mock.calls.find(([p]) => p === "/api/v1/sessions/{sessionId}/smoke-checks/{checkId}/verdict");
		expect(call![1].params.path).toMatchObject({ sessionId: "s1", checkId: "c1" });
		expect(call![1].body).toEqual({ verdict: "pass", note: "worked great" });
	});

	it("shows a Change control for a decided case and resets it", async () => {
		checks = [check({ verdict: "fail", note: "broke", decidedAt: "2026-07-11T10:05:00Z" })];
		renderView();
		// A decided card starts collapsed; expand it to reveal the Change button.
		await userEvent.click(await screen.findByText("A fresh MR shows up"));
		await userEvent.click(await screen.findByRole("button", { name: "Change" }));
		await waitFor(() =>
			expect(postMock.mock.calls.some(([p]) => p === "/api/v1/sessions/{sessionId}/smoke-checks/{checkId}/reset")).toBe(
				true,
			),
		);
	});

	it("uploads dropped image evidence via a multipart POST to the evidence endpoint", async () => {
		const fetchMock = vi.fn().mockResolvedValue({ ok: true });
		vi.stubGlobal("fetch", fetchMock);
		renderView();
		const slot = await screen.findByRole("button", { name: "Drop or paste evidence" });
		const file = new File(["PNGBYTES"], "shot.png", { type: "image/png" });
		fireEvent.drop(slot, { dataTransfer: { files: [file] } });
		await waitFor(() => expect(fetchMock).toHaveBeenCalled());
		const [url, opts] = fetchMock.mock.calls[0];
		expect(url).toBe("/api/v1/sessions/s1/smoke-checks/c1/evidence");
		expect(opts.method).toBe("POST");
		expect(opts.body).toBeInstanceOf(FormData);
		expect((opts.body as FormData).get("file")).toBe(file);
		vi.unstubAllGlobals();
	});

	it("shows the report bar once a case is decided and reports results", async () => {
		checks = [check({ verdict: "pass", decidedAt: "2026-07-11T10:05:00Z" })];
		renderView();
		const reportBtn = await screen.findByRole("button", { name: /Report results to worker/ });
		await userEvent.click(reportBtn);
		await waitFor(() =>
			expect(postMock.mock.calls.some(([p]) => p === "/api/v1/sessions/{sessionId}/smoke-checks/report")).toBe(true),
		);
		expect(await screen.findByText(/Reported results/)).toBeInTheDocument();
	});
});
