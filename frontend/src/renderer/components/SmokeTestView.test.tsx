import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { getMock, postMock, deleteMock } = vi.hoisted(() => ({
	getMock: vi.fn(),
	postMock: vi.fn(),
	deleteMock: vi.fn(),
}));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock, POST: postMock, DELETE: deleteMock },
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
	// Deleting server-side clears the case's evidence, so the reconciling refetch
	// returns it empty (matching the optimistic drop).
	deleteMock.mockReset().mockImplementation(async () => {
		checks = checks.map((c) => ({ ...c, evidence: [] }));
		return { data: { check: checks[0] }, error: undefined };
	});
});

function renderView(sessionId = "s1", issueId?: string) {
	const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={qc}>
			<SmokeTestView sessionId={sessionId} worker="fix gl note" issueId={issueId} />
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

	it("collapses the case immediately when a verdict is recorded", async () => {
		renderView();
		// Pending case is expanded — its note textarea is visible.
		expect(await screen.findByLabelText(/Note for A fresh MR shows up/)).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: /Works — Pass/ }));
		// The expanded body (note textarea) is gone the moment the verdict is set.
		await waitFor(() => expect(screen.queryByLabelText(/Note for A fresh MR shows up/)).not.toBeInTheDocument());
		// The case title stays visible in the collapsed header.
		expect(screen.getByText("A fresh MR shows up")).toBeInTheDocument();
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

	it("renders an evidence thumbnail via a fetched blob URL and drops the capture buttons", async () => {
		const realCreate = URL.createObjectURL;
		const realRevoke = URL.revokeObjectURL;
		URL.createObjectURL = vi.fn(() => "blob:mock");
		URL.revokeObjectURL = vi.fn();
		const fetchMock = vi.fn().mockResolvedValue({ ok: true, blob: async () => new Blob(["x"], { type: "image/png" }) });
		vi.stubGlobal("fetch", fetchMock);
		checks = [
			check({
				evidence: [
					{
						id: "ev1",
						checkId: "c1",
						sessionId: "s1",
						kind: "image",
						filename: "shot.png",
						mime: "image/png",
						sizeBytes: 3,
						createdAt: "2026-07-11T10:00:00Z",
					},
				],
			}),
		];
		try {
			renderView();
			// The thumbnail loads through fetch (not a direct <img> to the daemon).
			await waitFor(() =>
				expect(fetchMock).toHaveBeenCalledWith(expect.stringContaining("/smoke-checks/c1/evidence/ev1")),
			);
			// The disabled "coming soon" capture buttons are gone.
			expect(screen.queryByRole("button", { name: /Record screen/ })).not.toBeInTheDocument();
			expect(screen.queryByRole("button", { name: /Grab screenshot/ })).not.toBeInTheDocument();
		} finally {
			URL.createObjectURL = realCreate;
			URL.revokeObjectURL = realRevoke;
			vi.unstubAllGlobals();
		}
	});

	it("removes an evidence item via the hover × button (DELETE + optimistic drop)", async () => {
		const realCreate = URL.createObjectURL;
		const realRevoke = URL.revokeObjectURL;
		URL.createObjectURL = vi.fn(() => "blob:mock");
		URL.revokeObjectURL = vi.fn();
		const fetchMock = vi.fn().mockResolvedValue({ ok: true, blob: async () => new Blob(["x"], { type: "image/png" }) });
		vi.stubGlobal("fetch", fetchMock);
		checks = [
			check({
				evidence: [
					{
						id: "ev1",
						checkId: "c1",
						sessionId: "s1",
						kind: "image",
						filename: "shot.png",
						mime: "image/png",
						sizeBytes: 3,
						createdAt: "2026-07-11T10:00:00Z",
					},
				],
			}),
		];
		try {
			renderView();
			const removeBtn = await screen.findByRole("button", { name: "Remove shot.png" });
			fireEvent.click(removeBtn);
			// DELETE hits the per-evidence endpoint with the right path params.
			await waitFor(() => expect(deleteMock).toHaveBeenCalled());
			const [path, opts] = deleteMock.mock.calls[0];
			expect(path).toBe("/api/v1/sessions/{sessionId}/smoke-checks/{checkId}/evidence/{evidenceId}");
			expect(opts.params.path).toEqual({ sessionId: "s1", checkId: "c1", evidenceId: "ev1" });
			// Optimistically dropped — the thumbnail's remove button is gone.
			await waitFor(() => expect(screen.queryByRole("button", { name: "Remove shot.png" })).not.toBeInTheDocument());
		} finally {
			URL.createObjectURL = realCreate;
			URL.revokeObjectURL = realRevoke;
			vi.unstubAllGlobals();
		}
	});

	it("shows a framed placeholder (never a broken direct <img>) when the evidence fetch fails", async () => {
		const fetchMock = vi.fn().mockRejectedValue(new Error("blocked"));
		vi.stubGlobal("fetch", fetchMock);
		checks = [
			check({
				evidence: [
					{
						id: "ev1",
						checkId: "c1",
						sessionId: "s1",
						kind: "image",
						filename: "shot.png",
						mime: "image/png",
						sizeBytes: 3,
						createdAt: "2026-07-11T10:00:00Z",
					},
				],
			}),
		];
		try {
			renderView();
			// The placeholder surfaces the filename; no <img> is rendered (a direct
			// http:// src would be CSP-blocked and show a broken icon).
			await waitFor(() => expect(screen.getAllByText("shot.png").length).toBeGreaterThan(0));
			expect(document.querySelector("img")).toBeNull();
		} finally {
			vi.unstubAllGlobals();
		}
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

	it("posts run results to Jira for a linked session", async () => {
		checks = [check({ verdict: "pass", decidedAt: "2026-07-11T10:05:00Z" })];
		postMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/sessions/{sessionId}/smoke-checks/jira") {
				return {
					data: { key: "DEMO-101", commentUrl: "", attachmentsUploaded: 1, rowsPosted: 1, embeddedMedia: true },
					error: undefined,
				};
			}
			return { data: { delivered: true, target: "worker", summary: "1 pass" }, error: undefined };
		});
		renderView("s1", "jira:DEMO-101");
		await userEvent.click(await screen.findByRole("button", { name: /Post to Jira/ }));
		await waitFor(() =>
			expect(postMock.mock.calls.some(([p]) => p === "/api/v1/sessions/{sessionId}/smoke-checks/jira")).toBe(true),
		);
		const call = postMock.mock.calls.find(([p]) => p === "/api/v1/sessions/{sessionId}/smoke-checks/jira");
		expect(call![1].params.path).toMatchObject({ sessionId: "s1" });
		expect(await screen.findByText(/Posted 1 result to DEMO-101/)).toBeInTheDocument();
	});

	it("guides an unlinked session to the link flow instead of posting", async () => {
		checks = [check({ verdict: "pass", decidedAt: "2026-07-11T10:05:00Z" })];
		renderView("s1"); // no issueId → not Jira-linked
		await userEvent.click(await screen.findByRole("button", { name: /Post to Jira/ }));
		// The link dialog opens; nothing is posted to the Jira endpoint.
		expect(await screen.findByText(/Link a Jira issue/)).toBeInTheDocument();
		expect(postMock.mock.calls.some(([p]) => p === "/api/v1/sessions/{sessionId}/smoke-checks/jira")).toBe(false);
	});

	describe("evidence lightbox", () => {
		function ev(id: string, over: Record<string, unknown> = {}) {
			return {
				id,
				checkId: "c1",
				sessionId: "s1",
				kind: "image",
				filename: `${id}.png`,
				mime: "image/png",
				sizeBytes: 3,
				createdAt: "2026-07-11T10:00:00Z",
				...over,
			};
		}

		let realCreate: typeof URL.createObjectURL;
		let realRevoke: typeof URL.revokeObjectURL;

		beforeEach(() => {
			realCreate = URL.createObjectURL;
			realRevoke = URL.revokeObjectURL;
			URL.createObjectURL = vi.fn(() => "blob:mock");
			URL.revokeObjectURL = vi.fn();
			vi.stubGlobal(
				"fetch",
				vi.fn().mockResolvedValue({ ok: true, blob: async () => new Blob(["x"], { type: "image/png" }) }),
			);
			// jsdom has no media playback; the video's muted-autoplay would otherwise warn.
			HTMLMediaElement.prototype.play = vi.fn().mockResolvedValue(undefined);
		});

		afterEach(() => {
			URL.createObjectURL = realCreate;
			URL.revokeObjectURL = realRevoke;
			vi.unstubAllGlobals();
		});

		async function openViewer(evidence: ReturnType<typeof ev>[], name: string) {
			checks = [check({ evidence })];
			renderView();
			const thumb = await screen.findByRole("button", { name });
			await userEvent.click(thumb);
			const dialog = await screen.findByRole("dialog");
			return { thumb, dialog };
		}

		it("opens a centered modal showing the item large when a thumbnail is clicked", async () => {
			const { dialog } = await openViewer([ev("ev1"), ev("ev2")], "View ev1.png");
			expect(dialog).toHaveAttribute("aria-label", expect.stringContaining("ev1.png"));
			await waitFor(() => expect(within(dialog).getAllByRole("img").length).toBeGreaterThan(0));
			expect(within(dialog).getByText("1 / 2")).toBeInTheDocument();
		});

		it("pages with next/prev buttons and Left/Right keys, wrapping at both ends", async () => {
			const { dialog } = await openViewer([ev("ev1"), ev("ev2")], "View ev1.png");
			expect(within(dialog).getByText("1 / 2")).toBeInTheDocument();
			await userEvent.click(within(dialog).getByRole("button", { name: "Next evidence" }));
			expect(within(dialog).getByText("2 / 2")).toBeInTheDocument();
			// wrap forward: last → first
			await userEvent.click(within(dialog).getByRole("button", { name: "Next evidence" }));
			expect(within(dialog).getByText("1 / 2")).toBeInTheDocument();
			// arrow keys: wrap backward first → last, then forward again
			fireEvent.keyDown(dialog, { key: "ArrowLeft" });
			expect(within(dialog).getByText("2 / 2")).toBeInTheDocument();
			fireEvent.keyDown(dialog, { key: "ArrowRight" });
			expect(within(dialog).getByText("1 / 2")).toBeInTheDocument();
		});

		it("navigates across mixed image and video items", async () => {
			const { dialog } = await openViewer(
				[ev("ev1"), ev("vid1", { kind: "video", mime: "video/mp4", filename: "clip.mp4" })],
				"View ev1.png",
			);
			// image item shows zoom controls
			expect(within(dialog).getByRole("button", { name: "Zoom in" })).toBeInTheDocument();
			await userEvent.click(within(dialog).getByRole("button", { name: "Next evidence" }));
			// video item plays inline with no zoom controls
			await waitFor(() => expect(dialog.querySelector("video")).not.toBeNull());
			expect(within(dialog).queryByRole("button", { name: "Zoom in" })).not.toBeInTheDocument();
		});

		it("zooms an image in and resets zoom when switching items", async () => {
			const { dialog } = await openViewer([ev("ev1"), ev("ev2")], "View ev1.png");
			expect(within(dialog).getByText("100%")).toBeInTheDocument();
			await userEvent.click(within(dialog).getByRole("button", { name: "Zoom in" }));
			expect(within(dialog).getByText("150%")).toBeInTheDocument();
			// switching items resets the zoom
			await userEvent.click(within(dialog).getByRole("button", { name: "Next evidence" }));
			expect(within(dialog).getByText("100%")).toBeInTheDocument();
		});

		it("resets zoom when the viewer is closed and reopened", async () => {
			const { dialog } = await openViewer([ev("ev1")], "View ev1.png");
			await userEvent.click(within(dialog).getByRole("button", { name: "Zoom in" }));
			expect(within(dialog).getByText("150%")).toBeInTheDocument();
			await userEvent.click(within(dialog).getByRole("button", { name: "Close viewer" }));
			await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
			await userEvent.click(screen.getByRole("button", { name: "View ev1.png" }));
			const reopened = await screen.findByRole("dialog");
			expect(within(reopened).getByText("100%")).toBeInTheDocument();
		});

		it("closes via the X button, Esc, and a backdrop click", async () => {
			// X button
			let dialog = (await openViewer([ev("ev1")], "View ev1.png")).dialog;
			await userEvent.click(within(dialog).getByRole("button", { name: "Close viewer" }));
			await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
			// Esc
			await userEvent.click(screen.getByRole("button", { name: "View ev1.png" }));
			dialog = await screen.findByRole("dialog");
			await userEvent.keyboard("{Escape}");
			await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
			// backdrop click (the padding around the media = the dialog element itself)
			await userEvent.click(screen.getByRole("button", { name: "View ev1.png" }));
			dialog = await screen.findByRole("dialog");
			fireEvent.click(dialog);
			await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
		});

		it("does not close when the media itself is clicked", async () => {
			const { dialog } = await openViewer([ev("ev1")], "View ev1.png");
			const img = await within(dialog).findByRole("img");
			await userEvent.click(img);
			expect(screen.getByRole("dialog")).toBeInTheDocument();
		});

		it("restores focus to the triggering thumbnail on close", async () => {
			const { thumb, dialog } = await openViewer([ev("ev1")], "View ev1.png");
			await userEvent.click(within(dialog).getByRole("button", { name: "Close viewer" }));
			await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
			await waitFor(() => expect(thumb).toHaveFocus());
		});

		it("deleting an evidence item via the × does not open the lightbox", async () => {
			checks = [check({ evidence: [ev("ev1")] })];
			renderView();
			const removeBtn = await screen.findByRole("button", { name: "Remove ev1.png" });
			fireEvent.click(removeBtn);
			await waitFor(() => expect(deleteMock).toHaveBeenCalled());
			expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
		});
	});
});
