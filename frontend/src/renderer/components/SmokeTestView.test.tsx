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

// check builds a case in the shape the daemon actually sends. The four agent*
// fields are DERIVED there from the latest recorded run, so a fixture that set
// them without a run would be a shape production never produces - and the run
// history is exactly what the tab now reads. Give `runs` explicitly to model a
// case that ran more than once.
function check(overrides: Record<string, unknown>) {
	const base = {
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
	} as Record<string, unknown>;
	if (!base.runs && base.agentRanAt) {
		base.runs = [
			{
				id: "run_c1_1",
				checkId: base.id,
				sessionId: base.sessionId,
				seq: 1,
				verdict: base.agentVerdict ?? "",
				note: base.agentNote ?? "",
				sha: base.agentSha ?? "",
				recordedAt: base.agentRanAt,
				createdAt: base.agentRanAt,
				updatedAt: base.agentRanAt,
			},
		];
		// A capture with no run of its own belongs to the run that recorded it,
		// which is what the upload path does; leaving it unattached here would
		// make every fixture read as pre-history evidence.
		base.agentEvidence = ((base.agentEvidence as Record<string, unknown>[]) ?? []).map((ev) => ({
			runId: "run_c1_1",
			...ev,
		}));
	}
	return base;
}

let checks: ReturnType<typeof check>[];
// A member's recorded "I looked, there is nothing here for a person". Absent by
// default, which is the state every existing test is written against.
let standDown: Record<string, unknown> | null;
// The tab reads the session's PR summaries too, for the staleness rule: a
// machine result is stale when the commit it ran against is no longer head.
let prs: { number: number; headSha: string }[];

beforeEach(() => {
	checks = [check({})];
	standDown = null;
	prs = [];
	getMock.mockReset().mockImplementation(async (path: string) => {
		if (path === "/api/v1/sessions/{sessionId}/pr") return { data: { prs }, error: undefined };
		return { data: { worker: "fix gl note", checks, ...(standDown ? { standDown } : {}) }, error: undefined };
	});
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

	it("reveals + opens evidence via the export endpoint and the shell bridge", async () => {
		const realCreate = URL.createObjectURL;
		const realRevoke = URL.revokeObjectURL;
		URL.createObjectURL = vi.fn(() => "blob:mock");
		URL.revokeObjectURL = vi.fn();
		const fetchMock = vi.fn().mockResolvedValue({ ok: true, blob: async () => new Blob(["x"], { type: "image/png" }) });
		vi.stubGlobal("fetch", fetchMock);
		const exportPath = "/Users/x/.ao/data/evidence/s1/c1/_open/c1-shot.png";
		postMock.mockReset().mockResolvedValue({ data: { path: exportPath }, error: undefined });
		const reveal = vi.fn().mockResolvedValue(undefined);
		const open = vi.fn().mockResolvedValue(undefined);
		const realReveal = window.ao!.shell.showItemInFolder;
		const realOpen = window.ao!.shell.openPath;
		window.ao!.shell.showItemInFolder = reveal;
		window.ao!.shell.openPath = open;
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
			// Reveal → POST to the export endpoint, then shell.showItemInFolder(path).
			fireEvent.click(await screen.findByRole("button", { name: "Reveal shot.png in Finder" }));
			await waitFor(() => expect(reveal).toHaveBeenCalledWith(exportPath));
			const exportCall = postMock.mock.calls.find((c) => String(c[0]).includes("/export"));
			expect(exportCall?.[1].params.path).toEqual({ sessionId: "s1", checkId: "c1", evidenceId: "ev1" });
			expect(open).not.toHaveBeenCalled();

			// Open → same export, but shell.openPath(path).
			fireEvent.click(screen.getByRole("button", { name: "Open shot.png" }));
			await waitFor(() => expect(open).toHaveBeenCalledWith(exportPath));
		} finally {
			window.ao!.shell.showItemInFolder = realReveal;
			window.ao!.shell.openPath = realOpen;
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

	// Evidence that lands as a download link instead of an inline preview is the
	// one failure of this flow the user cannot see from the app: the toast used to
	// read exactly like a clean post, so the only way to find out was to open the
	// issue and notice the screenshots were missing.
	it("says so when evidence lands as links instead of inline previews", async () => {
		checks = [check({ verdict: "pass", decidedAt: "2026-07-11T10:05:00Z" })];
		postMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/sessions/{sessionId}/smoke-checks/jira") {
				return {
					data: {
						key: "DEMO-101",
						commentUrl: "",
						attachmentsUploaded: 2,
						rowsPosted: 1,
						embeddedMedia: false,
						evidenceLinked: 2,
					},
					error: undefined,
				};
			}
			return { data: { delivered: true, target: "worker", summary: "1 pass" }, error: undefined };
		});
		renderView("s1", "jira:DEMO-101");
		await userEvent.click(await screen.findByRole("button", { name: /Post to Jira/ }));
		expect(await screen.findByText(/2 evidence files posted as links, not previews/)).toBeInTheDocument();
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
	// -----------------------------------------------------------------------
	// The machine's lane. jsdom cannot see paint, so these pin only what is
	// logic: which state the tab believes it is in, and - the load-bearing one -
	// that no machine result is ever rendered as a case a person has finished.
	describe("a machine result beside the human's", () => {
		const HEAD = "4b21e07c9a5d1f6083e2b7c4419af6d2e0d5c118";
		const OLD = "9f0c2ad41b77e3b5c8d6a0f21e4c7b9038a1d6e5";
		const RAN = "2026-07-11T09:00:00Z";

		it("shows nothing in the machine's slot when it has not run", async () => {
			renderView();
			expect(await screen.findByText("A fresh MR shows up")).toBeInTheDocument();
			expect(screen.queryByText(/^qa · /)).not.toBeInTheDocument();
			expect(screen.queryByText("WHAT QA SAW")).not.toBeInTheDocument();
		});

		it("renders a run that declined to judge distinctly from one that never ran", async () => {
			// agentRanAt set with an empty agentVerdict is a real, deliberate state:
			// qa captured the screen and left the judgement to a person, because
			// paint / focus / timing / feel are not machine-judgeable. Reading it as
			// "not run yet" sends the person to fix the wrong thing.
			checks = [check({ agentRanAt: RAN, agentSha: HEAD })];
			renderView();
			expect(await screen.findByText("qa · evidence only")).toBeInTheDocument();
			expect(screen.getByText(/left the judgement to you/)).toBeInTheDocument();
			expect(screen.getByText(/does not settle this one/)).toBeInTheDocument();
			// And it is never phrased as an absence.
			expect(screen.queryByText(/qa · ran/)).not.toBeInTheDocument();
		});

		it("never renders a machine pass as a case that is done", async () => {
			checks = [check({ agentRanAt: RAN, agentVerdict: "pass", agentSha: HEAD })];
			renderView();
			// The case's own status stays "To check": the human's verdict is the
			// only thing that can retire a case from the play list.
			expect(await screen.findByText("To check")).toBeInTheDocument();
			expect(screen.getByText("qa · ran")).toBeInTheDocument();
			// The completion count does not move.
			expect(screen.getByText(/of 1 verified/)).toBeInTheDocument();
			expect(screen.getByText("0")).toBeInTheDocument();
			expect(screen.getByText("1 to check")).toBeInTheDocument();
			// And the tab says out loud what the machine pass is not.
			expect(screen.getByText(/not a check off your list/)).toBeInTheDocument();
			// The report bar (which only appears once a person has decided
			// something) stays away.
			expect(screen.queryByRole("button", { name: /Report results to worker/ })).not.toBeInTheDocument();
			// The play controls are exactly where they were.
			expect(screen.getByRole("button", { name: /Works — Pass/ })).toBeInTheDocument();
		});

		it("keeps the machine's note and evidence in their own lane, not the human's", async () => {
			checks = [
				check({
					agentRanAt: RAN,
					agentVerdict: "fail",
					agentSha: HEAD,
					agentNote: "Save went through with an empty name",
					agentEvidence: [
						{
							id: "ag1",
							checkId: "c1",
							sessionId: "s1",
							kind: "image",
							filename: "agent-shot.png",
							mime: "image/png",
							sizeBytes: 3,
							createdAt: RAN,
							source: "agent",
						},
					],
				}),
			];
			renderView();
			expect(await screen.findByText("qa · failed")).toBeInTheDocument();
			expect(screen.getByText("Save went through with an empty name")).toBeInTheDocument();
			// Labelled as qa's, and read-only: no × on a machine artefact.
			expect(screen.getByText(/CAPTURED BY QA/)).toBeInTheDocument();
			expect(screen.queryByRole("button", { name: "Remove agent-shot.png" })).not.toBeInTheDocument();
			// The human's own dropzone is untouched and still empty.
			expect(screen.getByText(/YOUR EVIDENCE/)).toBeInTheDocument();
			expect(screen.getByLabelText("Drop or paste evidence")).toBeInTheDocument();
		});

		it("marks a run stale when the commit it ran against is no longer head", async () => {
			checks = [check({ prNum: 322, agentRanAt: RAN, agentVerdict: "pass", agentSha: OLD })];
			prs = [{ number: 322, headSha: HEAD }];
			renderView();
			expect(await screen.findByText(/qa · ran · stale/)).toBeInTheDocument();
			expect(screen.getByText("Stale.")).toBeInTheDocument();
			// The sha it ran against is shown twice (the run's header line and the
			// stale note); head is shown once, beside it.
			expect(screen.getAllByText(OLD.slice(0, 7)).length).toBeGreaterThan(0);
			expect(screen.getByText(HEAD.slice(0, 7))).toBeInTheDocument();
		});

		it("does not mark it stale when the run is at head, or when head is unknown", async () => {
			checks = [check({ prNum: 322, agentRanAt: RAN, agentVerdict: "pass", agentSha: HEAD })];
			prs = [{ number: 322, headSha: HEAD }];
			renderView();
			expect(await screen.findByText("qa · ran")).toBeInTheDocument();
			expect(screen.queryByText("Stale.")).not.toBeInTheDocument();
		});

		it("shows the earlier runs collapsed under the latest one, each with its own verdict and commit", async () => {
			// The failure this whole shape fixes: a case re-run on a newer commit
			// gave the OPPOSITE result, the earlier verdict was overwritten out of
			// existence, and the only surviving trace of the inversion was a
			// sentence someone wrote in a note.
			checks = [
				check({
					agentRanAt: RAN,
					agentVerdict: "pass",
					agentSha: HEAD,
					agentNote: "renders clean",
					runs: [
						{
							id: "r1",
							checkId: "c1",
							sessionId: "s1",
							seq: 1,
							verdict: "fail",
							note: "clipped at 320px",
							sha: OLD,
							recordedAt: "2026-07-10T09:00:00Z",
							createdAt: "2026-07-10T09:00:00Z",
							updatedAt: "2026-07-10T09:00:00Z",
						},
						{
							id: "r2",
							checkId: "c1",
							sessionId: "s1",
							seq: 2,
							verdict: "pass",
							note: "passed once already",
							sha: "aaaaaaabbbbbbbcccccccdddddddeeeeeeefffffff",
							recordedAt: "2026-07-10T12:00:00Z",
							createdAt: "2026-07-10T12:00:00Z",
							updatedAt: "2026-07-10T12:00:00Z",
						},
						{
							id: "r3",
							checkId: "c1",
							sessionId: "s1",
							seq: 3,
							verdict: "pass",
							note: "renders clean",
							sha: HEAD,
							recordedAt: RAN,
							createdAt: RAN,
							updatedAt: RAN,
						},
					],
					agentEvidence: [
						{
							id: "ag1",
							checkId: "c1",
							sessionId: "s1",
							kind: "image",
							filename: "clipped.png",
							mime: "image/png",
							sizeBytes: 3,
							createdAt: RAN,
							source: "agent",
							runId: "r1",
						},
					],
				}),
			];
			renderView();
			// The latest run is the headline, and it says which round it is.
			expect(await screen.findByText("qa · ran")).toBeInTheDocument();
			expect(screen.getByText(/run 3 of 3/)).toBeInTheDocument();
			// The earlier round is there, with the verdict and commit that make the
			// inversion readable at a glance.
			expect(screen.getByText("EARLIER RUNS", { exact: false })).toBeInTheDocument();
			expect(screen.getByText("RUN 1")).toBeInTheDocument();
			expect(screen.getByText("failed")).toBeInTheDocument();
			// An earlier PASS says "passed", not the chip's "ran": in a history row
			// the verdict is the thing being read, and "ran" reads as "it executed".
			expect(screen.getByText("RUN 2")).toBeInTheDocument();
			expect(screen.getByText("passed")).toBeInTheDocument();
			expect(screen.getByText(OLD.slice(0, 7))).toBeInTheDocument();
			// Collapsed, its note and its capture are not shown under the pass.
			expect(screen.queryByText("clipped at 320px")).not.toBeInTheDocument();
			expect(screen.queryByLabelText(/clipped.png/)).not.toBeInTheDocument();
			// Opening it shows what THAT round saw.
			await userEvent.click(screen.getByTestId("qa-run-r1"));
			expect(screen.getByText("clipped at 320px")).toBeInTheDocument();
			expect(screen.getByText(/CAPTURED IN RUN 1/)).toBeInTheDocument();
		});

		it("says nothing about earlier runs when there has only been one", async () => {
			checks = [check({ agentRanAt: RAN, agentVerdict: "pass", agentSha: HEAD })];
			renderView();
			expect(await screen.findByText("qa · ran")).toBeInTheDocument();
			expect(screen.queryByText("EARLIER RUNS", { exact: false })).not.toBeInTheDocument();
			// And no "run 1 of 1" - a count is only worth reading when it counts.
			expect(screen.queryByText(/run 1 of 1/)).not.toBeInTheDocument();
		});

		it("groups captures with no run apart, and says why, instead of filing them under the current verdict", async () => {
			// The legacy shape: the result these were captured for was overwritten
			// and is gone. Showing them under the verdict that happens to be newest
			// is how a person came to read a stale image as current evidence.
			checks = [
				check({
					agentRanAt: RAN,
					agentVerdict: "pass",
					agentSha: HEAD,
					agentEvidence: [
						{
							id: "ag_old",
							checkId: "c1",
							sessionId: "s1",
							kind: "image",
							filename: "from-before.png",
							mime: "image/png",
							sizeBytes: 3,
							createdAt: "2026-07-01T09:00:00Z",
							source: "agent",
							runId: "",
						},
					],
				}),
			];
			renderView();
			expect(await screen.findByText("qa · ran")).toBeInTheDocument();
			expect(screen.getByTestId("qa-unknown-run-c1")).toBeInTheDocument();
			expect(screen.getByText("UNKNOWN RUN")).toBeInTheDocument();
			expect(screen.getByText(/Captured before AO kept a run history/)).toBeInTheDocument();
			// The capture is inside that group, not in the current run's strip - and
			// the current run shows NO strip at all, because it captured nothing.
			const group = screen.getByTestId("qa-unknown-run-c1");
			expect(group.textContent).toMatch(/run unknown/);
			expect(screen.queryByText(/not yours, and not deletable/)).not.toBeInTheDocument();
			expect(screen.getAllByText(/CAPTURED BY QA/)).toHaveLength(1);
		});

		it("leaves the human's flow exactly as it was when a machine result is present", async () => {
			checks = [check({ agentRanAt: RAN, agentVerdict: "fail", agentSha: HEAD, agentNote: "step 2 errored" })];
			renderView();
			// Same two clicks as a case with no machine result: type the note, press Pass.
			const note = await screen.findByLabelText(/Note for A fresh MR shows up/);
			await userEvent.type(note, "looks fine to me");
			await userEvent.click(screen.getByRole("button", { name: /Works — Pass/ }));
			const call = postMock.mock.calls.find(
				([p]) => p === "/api/v1/sessions/{sessionId}/smoke-checks/{checkId}/verdict",
			);
			expect(call![1].body).toEqual({ verdict: "pass", note: "looks fine to me" });
		});
	});

	describe("retired cases", () => {
		const retired = (over: Record<string, unknown> = {}) =>
			check({
				id: "gone",
				seq: 2,
				name: "The legacy toggle still writes the old key",
				retiredAt: "2026-07-10T10:00:00Z",
				retiredReason: "The legacy key was deleted; a Go test covers the migration.",
				...over,
			});

		it("keeps a retired case out of the playable list and out of the counts", async () => {
			checks = [check({}), retired()];
			renderView();
			await screen.findByText("A fresh MR shows up");
			// One case to play, not two.
			expect(screen.getByText(/of 1 verified/)).toBeInTheDocument();
			// The retired one is not rendered as a card: no play controls for it.
			expect(screen.queryByLabelText(/Note for The legacy toggle/)).not.toBeInTheDocument();
			expect(screen.queryByText("The legacy toggle still writes the old key")).not.toBeInTheDocument();
		});

		it("surfaces it, with its reason, in a collapsed record at the foot of the list", async () => {
			// Retiring is how the checklist shrinks. It has to shrink AUDITABLY:
			// "1 retired, now covered by a test" rather than a case vanishing.
			checks = [check({}), retired({ verdict: "pass", decidedAt: "2026-07-09T10:00:00Z" })];
			renderView();
			const disclosure = await screen.findByRole("button", { name: /1 retired from this checklist/ });
			expect(disclosure).toHaveAttribute("aria-expanded", "false");
			await userEvent.click(disclosure);
			expect(await screen.findByText("The legacy toggle still writes the old key")).toBeInTheDocument();
			expect(screen.getByText(/The legacy key was deleted/)).toBeInTheDocument();
			// Frozen: the verdict a person recorded before it retired is kept and
			// shown, and there is nothing here to play or change.
			expect(screen.getByText(/your verdict: passed/)).toBeInTheDocument();
			expect(screen.queryByRole("button", { name: "Change" })).not.toBeInTheDocument();
			// No play surface for it: no note field, no verdict buttons of its own
			// (the live case above still has its pair, untouched).
			expect(screen.queryByLabelText(/Note for The legacy toggle/)).not.toBeInTheDocument();
			expect(screen.getAllByRole("button", { name: /Works — Pass/ })).toHaveLength(1);
		});

		it("does not claim there is no checklist when every case has retired", async () => {
			checks = [retired()];
			renderView();
			expect(await screen.findByText("Nothing left to play")).toBeInTheDocument();
			expect(screen.queryByText("No smoke checks yet")).not.toBeInTheDocument();
			expect(screen.getByRole("button", { name: /1 retired from this checklist/ })).toBeInTheDocument();
		});
	});
});

// ---------------------------------------------------------------------------
// A SHARED checklist: who wrote each case, and the two things an empty tab used
// to say at once.

describe("SmokeTestView shared authorship", () => {
	it("names the member who wrote each case, and leaves an unattributable one unnamed", async () => {
		checks = [
			check({
				id: "c1",
				seq: 1,
				name: "From dev",
				authoredBy: "mer-1",
				authoredByRole: "dev",
				authoredAt: "2026-07-11T09:00:00Z",
			}),
			check({
				id: "c2",
				seq: 2,
				name: "From qa",
				authoredBy: "mer-2",
				authoredByRole: "qa",
				authoredAt: "2026-07-11T09:30:00Z",
			}),
			check({ id: "c3", seq: 3, name: "From nobody AO knows" }),
		];
		renderView();

		// The human's stated reason for wanting attribution: seeing which cases
		// came from dev, who knows the call sites, and which from qa.
		expect(await screen.findByText(/written by dev/)).toBeInTheDocument();
		expect(screen.getByText(/written by qa/)).toBeInTheDocument();
		// Three cases, two authors: the third shows no author rather than a
		// guessed one.
		expect(screen.queryAllByText(/written by/)).toHaveLength(2);
	});

	it("falls back to the session id when the author has no crew role", async () => {
		checks = [check({ authoredBy: "solo-7", authoredByRole: "" })];
		renderView();
		expect(await screen.findByText(/written by @solo-7/)).toBeInTheDocument();
	});

	it("says nobody has decided yet when the checklist is empty", async () => {
		checks = [];
		renderView();
		expect(await screen.findByText("No smoke checks yet")).toBeInTheDocument();
		expect(screen.getByText(/Nobody has decided what a person should look at yet/)).toBeInTheDocument();
	});

	// THE DISTINCTION THIS FEATURE EXISTS FOR. The same empty list used to render
	// identically whether nobody had decided or somebody had decided there was
	// nothing worth looking at - opposite answers, one panel.
	it("renders a stood-down checklist as an answer, not as an empty list", async () => {
		checks = [];
		standDown = {
			sessionId: "s1",
			at: "2026-07-11T10:00:00Z",
			by: "mer-2",
			byRole: "qa",
			reason: "pure refactor; behaviour is covered by TestReplaceSmokeChecks",
			createdAt: "2026-07-11T10:00:00Z",
			updatedAt: "2026-07-11T10:00:00Z",
		};
		renderView();

		expect(await screen.findByText("qa stood down")).toBeInTheDocument();
		expect(screen.getByText(/pure refactor; behaviour is covered/)).toBeInTheDocument();
		expect(screen.getByText(/This is an answer, not an empty list/)).toBeInTheDocument();
		// And it must NOT fall back to the "nobody has decided" copy, which is the
		// opposite claim.
		expect(screen.queryByText("No smoke checks yet")).not.toBeInTheDocument();
		expect(screen.queryByText(/Nobody has decided/)).not.toBeInTheDocument();
	});

	it("names the worker when AO could not identify who stood down", async () => {
		checks = [];
		standDown = {
			sessionId: "s1",
			at: "2026-07-11T10:00:00Z",
			reason: "no runtime surface",
			createdAt: "2026-07-11T10:00:00Z",
			updatedAt: "2026-07-11T10:00:00Z",
		};
		renderView();
		expect(await screen.findByText("the worker stood down")).toBeInTheDocument();
	});

	// A stand-down is a claim about the WHOLE list, so a case on the list wins:
	// the daemon retracts it, but the tab must not render both even for the one
	// poll where a stale cache could carry both.
	it("shows the cases, not the stand-down, when both arrive together", async () => {
		checks = [check({ name: "Actually, look at the header" })];
		standDown = {
			sessionId: "s1",
			at: "2026-07-11T10:00:00Z",
			byRole: "qa",
			reason: "no runtime surface",
			createdAt: "2026-07-11T10:00:00Z",
			updatedAt: "2026-07-11T10:00:00Z",
		};
		renderView();
		expect(await screen.findByText("Actually, look at the header")).toBeInTheDocument();
		expect(screen.queryByText("qa stood down")).not.toBeInTheDocument();
	});
});
