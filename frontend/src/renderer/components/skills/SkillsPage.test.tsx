import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { TooltipProvider } from "../ui/tooltip";
import { SkillsPage } from "./SkillsPage";
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
				<SkillsPage />
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	getMock.mockReset();
	postMock.mockReset();
	proposals = [proposal(), conflict];
	learning = true;
	routeGets();
});

describe("SkillsPage", () => {
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
				body: { content: undefined, resolution: undefined },
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
				body: { reason: "only that week" },
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
				body: { content: undefined, resolution: "words_win" },
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

describe("skills model", () => {
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
