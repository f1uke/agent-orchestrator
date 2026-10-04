import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { TooltipProvider } from "../ui/tooltip";
import { MemoryPage } from "./MemoryPage";
import { diffFiles, queueOrder } from "./model";

const getMock = vi.fn();
const postMock = vi.fn();

vi.mock("../../lib/api-client", () => ({
	apiClient: {
		GET: (...args: unknown[]) => getMock(...args),
		POST: (...args: unknown[]) => postMock(...args),
	},
	apiErrorMessage: (error: unknown) => (error as { message?: string })?.message ?? "failed",
	hasTrustedApiBaseUrl: () => true,
	subscribeApiBaseUrl: () => () => undefined,
}));

const memoryDiff =
	"--- /dev/null\n+++ b/home/me/.claude/projects/-repo/memory/feedback_dev.md\n@@ -0,0 +1,2 @@\n+---\n+Test with the Dev build.\n" +
	"--- a/home/me/.claude/projects/-repo/memory/MEMORY.md\n+++ b/home/me/.claude/projects/-repo/memory/MEMORY.md\n@@ -1,1 +1,2 @@\n - [Old](old.md) - old\n+- [Dev build](feedback_dev.md) - Test with the Dev build\n";

function proposal(over: Record<string, unknown> = {}) {
	return {
		id: 1,
		projectId: "advisor",
		taskKey: "solo:a-1",
		action: "create_memory",
		targetPath: "/home/me/.claude/projects/-repo/memory/feedback_dev.md",
		scope: "project:advisor",
		title: "Test with the Dev build only",
		rationale: "The person said to install only the Dev build.",
		newContent: "---\nname: feedback-dev\n---\n\nTest with the Dev build.\n",
		indexLine: "- [Dev build](feedback_dev.md) - Test with the Dev build",
		diff: memoryDiff,
		confidence: 0.8,
		outcome: "merged",
		ruleVerdicts: [],
		verifier: { contradictsRule: false, grounded: true, sensitiveData: false, notes: "Grounded." },
		status: "pending",
		evidenceIds: [9],
		createdAt: "2026-10-04T00:00:00Z",
		updatedAt: "2026-10-04T00:00:00Z",
		...over,
	};
}

const conflict = proposal({
	id: 2,
	action: "conflict",
	title: "Thai allowed in the MR Note",
	targetPath: "rule:protected-7",
	newContent: "A little Thai is fine in the MR Note.",
	indexLine: "",
	diff: "",
	confidence: 0.5,
	ruleVerdicts: [{ ruleId: "protected-7", verdict: "contradicts", note: "PR text is English" }],
});

let proposals: unknown[] = [];
let learning = true;
/** Per proposal id: the detail's written state and history. */
let extra: Record<number, Record<string, unknown>> = {};

function routeGets() {
	getMock.mockImplementation((path: string, init?: { params?: { path?: { id?: number } } }) => {
		if (path === "/api/v1/learning/proposals")
			return Promise.resolve({
				data: {
					proposals,
					run: {
						running: false,
						manual: false,
						tasks: 0,
						failed: 0,
						proposals: 0,
						dropped: 0,
						costUsd: 0,
						budgetUsd: 0,
					},
				},
			});
		if (path === "/api/v1/learning/status")
			return Promise.resolve({
				data: {
					projects: [{ project: "advisor", enabled: learning, drafts: { open: 4 } }],
					collect: { todaySpendUsd: 1.2, dailyBudgetUsd: 2 },
				},
			});
		if (path === "/api/v1/learning/proposals/{id}") {
			const id = init?.params?.path?.id;
			return Promise.resolve({
				data: {
					proposal: proposals.find((p) => (p as { id: number }).id === id),
					evidence: [
						{
							id: 9,
							sessionId: "advisor-9",
							quote: "ติดตั้ง build จาก Dev เท่านั้น",
							anchorSourceClass: "typed",
							about: "agent_practice",
							confidence: 0.85,
						},
					],
					rules: [{ id: "protected-7", text: "PR text is fully English.", source: "protected rule", protected: true }],
					history: [],
					...(extra[id ?? 0] ?? {}),
				},
			});
		}
		return Promise.resolve({ data: {} });
	});
}

function renderPage() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<TooltipProvider>
				<MemoryPage />
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	getMock.mockReset();
	postMock.mockReset();
	proposals = [proposal(), conflict];
	learning = true;
	extra = {};
	routeGets();
});

describe("MemoryPage", () => {
	it("queues conflicts first and opens the first proposal", async () => {
		renderPage();
		const list = await screen.findByRole("list", { name: "Proposals" });
		const rows = within(list).getAllByRole("button");
		expect(rows.map((r) => (r.textContent ?? "").includes("Thai allowed"))).toEqual([true, false]);
		expect(await screen.findByText("The rule you have (pinned)")).toBeTruthy();
		expect(await screen.findByText("PR text is fully English.")).toBeTruthy();
	});

	it("shows what would be written - the memory file and its MEMORY.md line - and approves it", async () => {
		renderPage();
		await userEvent.click(await screen.findByText("Test with the Dev build only"));
		expect(await screen.findByText("ติดตั้ง build จาก Dev เท่านั้น")).toBeTruthy();
		expect(screen.getAllByText("feedback_dev.md").length).toBe(2); // the header and the diff's file

		expect(screen.getByText("MEMORY.md")).toBeTruthy();
		postMock.mockResolvedValue({ data: proposal({ status: "applied" }) });
		await userEvent.click(screen.getByRole("button", { name: /Approve/ }));
		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/learning/proposals/{id}/approve", {
				params: { path: { id: 1 } },
				body: { content: undefined, resolution: undefined, via: "app" },
			}),
		);
		expect(await screen.findByRole("status")).toHaveProperty(
			"textContent",
			expect.stringContaining("Wrote feedback_dev.md and its MEMORY.md line"),
		);
	});

	it("rejects with the person's reason and approves an edit as written", async () => {
		renderPage();
		await userEvent.click(await screen.findByText("Test with the Dev build only"));
		await userEvent.click(await screen.findByRole("button", { name: /Reject/ }));
		await userEvent.type(screen.getByPlaceholderText(/Why not/), "only that week");
		postMock.mockResolvedValue({ data: proposal({ status: "rejected", rejectReason: "only that week" }) });
		const bar = screen.getByPlaceholderText(/Why not/).closest("div.sticky") as HTMLElement;
		await userEvent.click(within(bar).getByRole("button", { name: /Reject/ }));
		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/learning/proposals/{id}/reject", {
				params: { path: { id: 1 } },
				body: { reason: "only that week", via: "app" },
			}),
		);
	});

	it("needs a side before a conflict can be decided, and says what each side does", async () => {
		renderPage();
		expect(await screen.findByText("Pick which wins to decide.")).toBeTruthy();
		await userEvent.click(screen.getByRole("radio", { name: /Your newer words win/ }));
		expect(screen.getByText(/pinned rule's text becomes your newer words/)).toBeTruthy();
		postMock.mockResolvedValue({ data: { ...conflict, status: "applied", resolution: "words_win" } });
		await userEvent.click(screen.getByRole("button", { name: /Use my newer words/ }));
		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/learning/proposals/{id}/approve", {
				params: { path: { id: 2 } },
				body: { content: undefined, resolution: "words_win", via: "app" },
			}),
		);
	});

	it("says nothing waits, and how many lessons are on their way", async () => {
		proposals = [];
		renderPage();
		expect((await screen.findAllByText("Nothing to decide")).length).toBeGreaterThan(0);
		expect(screen.getAllByText(/4 candidate lessons are waiting/).length).toBeGreaterThan(0);
	});

	it("explains learning when no project learns", async () => {
		proposals = [];
		learning = false;
		renderPage();
		expect(await screen.findByText("No project learns from sessions")).toBeTruthy();
	});

	it("keeps the proposals safe and offers a retry when the daemon cannot be reached", async () => {
		getMock.mockImplementation((path: string) =>
			Promise.resolve(
				path === "/api/v1/learning/proposals"
					? { error: { message: "connection refused" } }
					: { data: { projects: [] } },
			),
		);
		renderPage();
		expect(await screen.findByText("Could not reach the daemon", undefined, { timeout: 4000 })).toBeTruthy();
		expect(screen.getByRole("button", { name: /Retry now/ })).toBeTruthy();
	});
});

describe("memory model", () => {
	it("splits a memory diff into its file and MEMORY.md", () => {
		const files = diffFiles(memoryDiff);
		expect(files.map((f) => [f.path.split("/").pop(), f.created])).toEqual([
			["feedback_dev.md", true],
			["MEMORY.md", false],
		]);
		expect(files[1].lines.filter((l) => l.kind === "add").map((l) => l.text)).toEqual([
			"- [Dev build](feedback_dev.md) - Test with the Dev build",
		]);
	});

	it("orders conflicts first, then by confidence", () => {
		const order = queueOrder([
			{ id: 1, action: "create_memory", confidence: 0.9 },
			{ id: 2, action: "conflict", confidence: 0.4 },
			{ id: 3, action: "create_memory", confidence: 0.95 },
		]).map((p) => p.id);
		expect(order).toEqual([2, 3, 1]);
	});
});

describe("a decided conflict card", () => {
	it("shows the side that won and cannot change", async () => {
		proposals = [{ ...conflict, status: "applied", resolution: "words_win", decidedAt: "2026-10-04T01:00:00Z" }];
		renderPage();
		await userEvent.click(await screen.findByRole("tab", { name: /Decided/ }));
		const won = await screen.findByRole("radio", { name: /Your newer words win/ });
		expect(won.getAttribute("aria-checked")).toBe("true");
		expect((screen.getByRole("radio", { name: /Keep the rule/ }) as HTMLButtonElement).disabled).toBe(true);
		expect(screen.queryByRole("button", { name: /Use my newer words/ })).toBeNull();
	});

	it("can be undone, putting the pinned rule's text back", async () => {
		proposals = [{ ...conflict, status: "applied", resolution: "words_win", decidedAt: "2026-10-04T01:00:00Z" }];
		renderPage();
		await userEvent.click(await screen.findByRole("tab", { name: /Decided/ }));
		await userEvent.click(await screen.findByRole("button", { name: /^Undo$/ }));
		expect(screen.getByText(/Puts your pinned rule's earlier text back/)).toBeTruthy();
		postMock.mockResolvedValue({ data: { ...conflict, status: "pending" } });
		await userEvent.click(screen.getByRole("button", { name: /^Undo$/ }));
		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/learning/proposals/{id}/undo", {
				params: { path: { id: 2 } },
				body: { confirmToken: undefined, via: "app" },
			}),
		);
		expect(await screen.findByRole("tab", { name: /To decide/, selected: true })).toBeTruthy();
	});
});

const filePath = "/home/me/.claude/projects/-repo/memory/feedback_dev.md";
const written = (over: Record<string, unknown> = {}) => ({
	path: filePath,
	exists: true,
	content: "---\nname: feedback-dev\n---\n\nTest with the Dev build.\n",
	indexPath: "/home/me/.claude/projects/-repo/memory/MEMORY.md",
	indexLine: "- [Dev build](feedback_dev.md) - Test with the Dev build",
	indexLinePresent: true,
	changed: false,
	token: "t0",
	...over,
});

describe("a snoozed proposal", () => {
	const snoozed = proposal({ snoozedUntil: "2099-01-02T00:00:00Z" });

	it("can still be approved, edited or rejected, or brought back now", async () => {
		proposals = [snoozed];
		extra = {
			1: {
				history: [
					{
						kind: "snoozed",
						status: "pending",
						snoozedUntil: "2099-01-02T00:00:00Z",
						via: "app",
						at: "2026-10-04T02:00:00Z",
					},
				],
			},
		};
		renderPage();
		await userEvent.click(await screen.findByRole("tab", { name: /Snoozed/ }));
		expect(await screen.findByText(/Decide it now, or bring it back to To decide/)).toBeTruthy();
		for (const name of [/^Approve$/, /Edit first/, /Unsnooze/, /^Reject$/]) {
			expect(screen.getByRole("button", { name })).toBeTruthy();
		}
		expect(within(await screen.findByRole("list", { name: "History" })).getByText(/Snoozed until/)).toBeTruthy();
		postMock.mockResolvedValue({ data: proposal() });
		await userEvent.click(screen.getByRole("button", { name: /Unsnooze/ }));
		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/learning/proposals/{id}/unsnooze", {
				params: { path: { id: 1 } },
				body: { via: "app" },
			}),
		);
		expect(await screen.findByRole("tab", { name: /To decide/, selected: true })).toBeTruthy();
		expect(await screen.findByRole("status")).toHaveProperty(
			"textContent",
			expect.stringContaining("back in To decide"),
		);
	});

	it("rejects straight from the snooze", async () => {
		proposals = [snoozed];
		renderPage();
		await userEvent.click(await screen.findByRole("tab", { name: /Snoozed/ }));
		await userEvent.click(await screen.findByRole("button", { name: /^Reject$/ }));
		await userEvent.type(screen.getByPlaceholderText(/Why not/), "do not record this");
		postMock.mockResolvedValue({ data: proposal({ status: "rejected", rejectReason: "do not record this" }) });
		const bar = screen.getByPlaceholderText(/Why not/).closest("div.sticky") as HTMLElement;
		await userEvent.click(within(bar).getByRole("button", { name: /Reject/ }));
		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/learning/proposals/{id}/reject", {
				params: { path: { id: 1 } },
				body: { reason: "do not record this", via: "app" },
			}),
		);
	});
});

describe("a rejected proposal", () => {
	it("can be reopened", async () => {
		proposals = [proposal({ status: "rejected", rejectReason: "one-off", decidedAt: "2026-10-04T01:00:00Z" })];
		renderPage();
		await userEvent.click(await screen.findByRole("tab", { name: /Decided/ }));
		postMock.mockResolvedValue({ data: proposal() });
		await userEvent.click(await screen.findByRole("button", { name: /Reopen/ }));
		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/learning/proposals/{id}/reopen", {
				params: { path: { id: 1 } },
				body: { via: "app" },
			}),
		);
		expect(await screen.findByRole("tab", { name: /To decide/, selected: true })).toBeTruthy();
	});
});

describe("an approved proposal", () => {
	const applied = proposal({ status: "applied", decidedAt: "2026-10-04T01:00:00Z" });

	it("undoes it, saying first what goes", async () => {
		proposals = [applied];
		extra = { 1: { written: written() } };
		renderPage();
		await userEvent.click(await screen.findByRole("tab", { name: /Decided/ }));
		expect(await screen.findByText("Unchanged since it was written.")).toBeTruthy();
		await userEvent.click(screen.getByRole("button", { name: /^Undo$/ }));
		expect(screen.getByText(/Removes feedback_dev.md and the line it added to MEMORY.md/)).toBeTruthy();
		postMock.mockResolvedValue({ data: proposal() });
		await userEvent.click(screen.getByRole("button", { name: /^Undo$/ }));
		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/learning/proposals/{id}/undo", {
				params: { path: { id: 1 } },
				body: { confirmToken: undefined, via: "app" },
			}),
		);
		expect(await screen.findByRole("status")).toHaveProperty(
			"textContent",
			expect.stringContaining("Removed feedback_dev.md and its MEMORY.md line"),
		);
	});

	it("shows a change made since and confirms it by its token before undoing", async () => {
		proposals = [applied];
		extra = {
			1: {
				written: written({
					changed: true,
					token: "t9",
					content: "edited by hand\n",
					diff: `--- a${filePath}\n+++ b${filePath}\n@@ -1,1 +1,1 @@\n-Test with the Dev build.\n+edited by hand\n`,
				}),
			},
		};
		renderPage();
		await userEvent.click(await screen.findByRole("tab", { name: /Decided/ }));
		expect(
			await screen.findByText(/changed since AO wrote it - by hand, by an agent, or by another proposal/),
		).toBeTruthy();
		expect(document.body.textContent).toContain("edited by hand"); // the diff's added line
		await userEvent.click(screen.getByRole("button", { name: /^Undo$/ }));
		postMock.mockResolvedValue({ data: proposal() });
		await userEvent.click(screen.getByRole("button", { name: /Undo anyway/ }));
		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/learning/proposals/{id}/undo", {
				params: { path: { id: 1 } },
				body: { confirmToken: "t9", via: "app" },
			}),
		);
	});

	it("edits the file as it is now", async () => {
		proposals = [applied];
		extra = { 1: { written: written({ content: "as it is now\n", token: "t3" }) } };
		renderPage();
		await userEvent.click(await screen.findByRole("tab", { name: /Decided/ }));
		await userEvent.click(await screen.findByRole("button", { name: /Edit memory/ }));
		const editor = screen.getByRole("textbox");
		expect((editor as HTMLTextAreaElement).value).toBe("as it is now\n");
		await userEvent.type(editor, "more");
		postMock.mockResolvedValue({ data: { ...applied, updatedAt: "2026-10-04T02:00:00Z" } });
		await userEvent.click(screen.getByRole("button", { name: /Save my edit/ }));
		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/learning/proposals/{id}/edit", {
				params: { path: { id: 1 } },
				body: { content: "as it is now\nmore", confirmToken: "t3", via: "app" },
			}),
		);
		// Saved: out of the editor, back to what was written.
		expect(await screen.findByRole("button", { name: /Edit memory/ })).toBeTruthy();
		expect(screen.queryByRole("button", { name: /Save my edit/ })).toBeNull();
	});
});
