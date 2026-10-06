import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { TestinyView } from "./TestinyView";
import type { TestinyCase, TestinyRun } from "../lib/testiny";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";

const { getMock, postMock, deleteMock } = vi.hoisted(() => ({
	getMock: vi.fn(),
	postMock: vi.fn(),
	deleteMock: vi.fn(),
}));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock, POST: postMock, DELETE: deleteMock },
	apiErrorMessage: (error: unknown, fallback = "Request failed") =>
		typeof error === "object" && error !== null && "message" in error
			? String((error as { message: unknown }).message)
			: fallback,
}));

const dev: WorkspaceSession = {
	id: "task-1",
	workspaceId: "mobile",
	workspaceName: "mobile",
	title: "Share sheet empty state",
	provider: "claude-code",
	kind: "worker",
	branch: "feat/share",
	status: "working",
	updatedAt: "2026-10-06T10:00:00Z",
	prs: [],
	crew: { id: "task-1", role: "dev", hasRun: true },
};
const qa: WorkspaceSession = { ...dev, id: "task-1-qa", crew: { id: "task-1", role: "qa", hasRun: true } };

vi.mock("../hooks/useWorkspaceQuery", () => ({
	useWorkspaceQuery: () => ({ data: [{ id: "mobile", sessions: [dev, qa] } as unknown as WorkspaceSummary] }),
}));

const minutesAgo = (m: number) => new Date(Date.now() - m * 60_000).toISOString();

const tc = (id: number, status: string, title = `case ${id}`, script?: string): TestinyCase => ({
	id,
	title,
	status,
	...(script ? { script } : {}),
});

const run = (runId: number, over: Partial<TestinyRun> = {}): TestinyRun => ({
	link: { sessionId: "task-1", runId, linkedBy: "task-1-qa", createdAt: minutesAgo(120) },
	title: `Run ${runId}`,
	url: `https://app.testiny.io/MOB/testruns/tr/${runId}`,
	closed: false,
	counts: null,
	cases: [],
	evidenceDir: "",
	fetchedAt: minutesAgo(0),
	...over,
});

function serve(runs: TestinyRun[], project = "MOB") {
	getMock.mockResolvedValue({ data: { project, runs }, error: undefined });
}

function renderView(session: WorkspaceSession = dev) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<TestinyView session={session} project="MOB" />
		</QueryClientProvider>,
	);
}

const card = (runId: number) => screen.getByRole("article", { name: new RegExp(`^TR-${runId}\\b`) });

beforeEach(() => {
	getMock.mockReset();
	postMock.mockReset();
	deleteMock.mockReset();
});

afterEach(() => {
	vi.restoreAllMocks();
});

describe("TestinyView runs", () => {
	it("shows a card per run in the order they were linked, under the project eyebrow", async () => {
		serve([run(633, { title: "Android" }), run(632, { title: "iOS" })]);
		renderView();

		const cards = await screen.findAllByRole("article");
		expect(cards.map((c) => within(c).getByText(/^TR-\d+$/).textContent)).toEqual(["TR-633", "TR-632"]);
		expect(within(cards[0]).getByText("Android")).toBeInTheDocument();
		expect(screen.getByText("Testiny · MOB")).toBeInTheDocument();
	});

	it("reads the task's runs whichever member is open", async () => {
		serve([]);
		renderView(qa);
		await screen.findByText("No test runs linked");
		expect(getMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/testiny/runs", {
			params: { path: { sessionId: "task-1" }, query: undefined },
		});
	});

	it("counts results per status and hides the statuses no case has", async () => {
		serve([run(632, { counts: { PASSED: 5, FAILED: 1, NOTRUN: 2, BLOCKED: 0 } })]);
		renderView();

		const summary = within(await screen.findByRole("list", { name: "Results" }));
		expect(summary.getAllByRole("listitem").map((li) => li.getAttribute("aria-label"))).toEqual([
			"5 Passed",
			"1 Failed",
			"2 Not run",
		]);
	});

	it("lists the cases that did not pass first and folds the passed ones behind a count", async () => {
		const user = userEvent.setup();
		serve([
			run(632, {
				cases: [
					tc(1, "PASSED", "opens"),
					tc(2, "NOTRUN", "cold launch"),
					tc(3, "FAILED", "disclaimer"),
					tc(4, "PASSED", "closes"),
				],
			}),
		]);
		renderView();

		const cases = await screen.findByRole("list", { name: "Cases" });
		expect(
			within(cases)
				.getAllByRole("listitem")
				.map((li) => li.textContent),
		).toEqual(["Faileddisclaimer", "Not runcold launch"]);
		expect(screen.queryByText("opens")).not.toBeInTheDocument();

		const disclosure = screen.getByRole("button", { name: "2 passed" });
		expect(disclosure).toHaveAttribute("aria-expanded", "false");
		await user.click(disclosure);
		expect(disclosure).toHaveAttribute("aria-expanded", "true");
		expect(screen.getByText("opens")).toBeInTheDocument();
		expect(screen.getByText("closes")).toBeInTheDocument();
	});

	it("says every case passed when none is left open", async () => {
		serve([run(633, { cases: [tc(1, "PASSED"), tc(2, "PASSED")] })]);
		renderView();
		expect(await screen.findByRole("button", { name: "All 2 passed" })).toBeInTheDocument();
	});

	it("shows how much of the run a case script plays, and which cases", async () => {
		serve([
			run(632, {
				cases: [
					tc(1, "FAILED", "disclaimer", "projects/nter/cases/chat/disclaimer.yaml"),
					tc(2, "NOTRUN", "cold launch"),
					tc(3, "NOTRUN", "voiceover"),
				],
			}),
		]);
		renderView();

		expect(await screen.findByText("scripted 1/3")).toBeInTheDocument();
		const tag = screen.getByText("script");
		expect(tag).toHaveAttribute("title", "projects/nter/cases/chat/disclaimer.yaml");
		expect(tag.closest("li")).toHaveTextContent("disclaimer");
	});

	it("has no scripted chip when no case has a script", async () => {
		serve([run(632, { cases: [tc(1, "FAILED")] })]);
		renderView();
		await screen.findByRole("article");
		expect(screen.queryByText(/^scripted/)).not.toBeInTheDocument();
		expect(screen.queryByText("script")).not.toBeInTheDocument();
	});

	it("names the plan and milestone, and says when the run is closed", async () => {
		serve([
			run(632, { plan: { id: 193, title: "Chat" }, milestone: { id: 4, title: "MOBILITY 2026-19" }, closed: true }),
			run(634),
		]);
		renderView();

		const first = within(await waitFor(() => card(632)));
		expect(first.getByText("TP-193 Chat")).toBeInTheDocument();
		expect(first.getByText("MOBILITY 2026-19")).toBeInTheDocument();
		expect(first.getByText("closed")).toBeInTheDocument();
		expect(within(card(634)).queryByText("closed")).not.toBeInTheDocument();
	});

	it("opens the run in Testiny in the browser", async () => {
		serve([run(632)]);
		renderView();
		const link = await screen.findByRole("link", { name: /Open in Testiny/ });
		expect(link).toHaveAttribute("href", "https://app.testiny.io/MOB/testruns/tr/632");
		expect(link).toHaveAttribute("target", "_blank");
	});

	it("reveals the run's evidence folder in Finder", async () => {
		const user = userEvent.setup();
		const reveal = vi.spyOn(window.ao!.shell, "showItemInFolder").mockResolvedValue(undefined);
		const dir = "/Users/me/Desktop/QA Evidence/MOBILITY/2026/M-19/TP-193 Chat/TR-632 - iOS";
		serve([run(632, { evidenceDir: dir }), run(633)]);
		renderView();

		await user.click(await screen.findByRole("button", { name: "Reveal in Finder" }));
		expect(reveal).toHaveBeenCalledWith(dir);
		const shown = within(card(632)).getByTitle(dir);
		expect(shown).toHaveTextContent("~/Desktop/QA Evidence/…/TR-632 - iOS");
		expect(within(card(633)).getByText("no folder yet")).toBeInTheDocument();
		expect(screen.getAllByRole("button", { name: "Reveal in Finder" })).toHaveLength(1);
	});

	it("says who linked each run and when", async () => {
		serve([
			run(632, { link: { sessionId: "task-1", runId: 632, linkedBy: "task-1-qa", createdAt: minutesAgo(125) } }),
			run(633, { link: { sessionId: "task-1", runId: 633, linkedBy: "", createdAt: minutesAgo(3) } }),
		]);
		renderView();

		expect(await within(await waitFor(() => card(632))).findByText("linked by qa · 2 h ago")).toBeInTheDocument();
		expect(within(card(633)).getByText("linked by you · 3 min ago")).toBeInTheDocument();
	});

	it("unlinks a run at once, with no confirm", async () => {
		const user = userEvent.setup();
		serve([run(632), run(633)]);
		deleteMock.mockResolvedValue({ error: undefined });
		renderView();

		const unlink = within(await waitFor(() => card(632))).getByRole("button", { name: "Unlink" });
		serve([run(633)]);
		await user.click(unlink);
		expect(deleteMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/testiny/runs/{runId}", {
			params: { path: { sessionId: "task-1", runId: "632" } },
		});
		await waitFor(() => expect(screen.queryByRole("article", { name: /^TR-632\b/ })).not.toBeInTheDocument());
	});

	it("refreshes past the daemon's cache and keeps the cards up meanwhile", async () => {
		const user = userEvent.setup();
		serve([run(632)]);
		renderView();
		await screen.findByRole("article");

		let answer: (value: unknown) => void = () => undefined;
		getMock.mockImplementation(() => new Promise((resolve) => (answer = resolve)));
		await user.click(screen.getByRole("button", { name: "Refresh" }));
		expect(getMock).toHaveBeenLastCalledWith("/api/v1/sessions/{sessionId}/testiny/runs", {
			params: { path: { sessionId: "task-1" }, query: { refresh: "1" } },
		});
		expect(screen.getByRole("article")).toBeInTheDocument();

		answer({ data: { project: "MOB", runs: [run(632, { title: "Renamed" })] }, error: undefined });
		expect(await screen.findByText("Renamed")).toBeInTheDocument();
	});
});

describe("TestinyView link run", () => {
	it("links what is typed on Enter and clears the field", async () => {
		const user = userEvent.setup();
		serve([]);
		postMock.mockResolvedValue({ data: run(632), error: undefined });
		renderView();

		const field = await screen.findByPlaceholderText("Run id or Testiny URL");
		await user.type(field, " https://app.testiny.io/MOB/testruns/tr/632 {Enter}");
		expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/testiny/runs", {
			params: { path: { sessionId: "task-1" } },
			body: { ref: "https://app.testiny.io/MOB/testruns/tr/632" },
		});
		await waitFor(() => expect(field).toHaveValue(""));
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
	});

	it("shows the daemon's reason under the field and keeps what was typed", async () => {
		const user = userEvent.setup();
		serve([]);
		postMock.mockResolvedValue({
			data: undefined,
			error: { code: "TESTINY_RUN_WRONG_PROJECT", message: "run 632 belongs to KERN; this project uses MOB" },
		});
		renderView();

		const field = await screen.findByPlaceholderText("Run id or Testiny URL");
		await user.type(field, "632{Enter}");
		expect(await screen.findByRole("alert")).toHaveTextContent("run 632 belongs to KERN; this project uses MOB");
		expect(field).toHaveValue("632");
	});

	it("does not post an empty field", async () => {
		const user = userEvent.setup();
		serve([]);
		renderView();
		await user.type(await screen.findByPlaceholderText("Run id or Testiny URL"), "   {Enter}");
		expect(postMock).not.toHaveBeenCalled();
	});
});

describe("TestinyView errors", () => {
	it("says once, above the cards, that the key is the problem when every run was refused for it", async () => {
		serve([
			run(632, { fetchedAt: minutesAgo(4), fetchError: { kind: "auth", message: "403" } }),
			run(633, { fetchedAt: minutesAgo(4), fetchError: { kind: "auth", message: "403" } }),
		]);
		renderView();

		expect(await screen.findByText(/Testiny key missing or rejected/)).toBeInTheDocument();
		expect(screen.getByText("testiny auth status")).toBeInTheDocument();
		expect(within(card(632)).getByText("showing data from 4 min ago")).toBeInTheDocument();
	});

	it("says once that the CLI is missing when no run could be read for it", async () => {
		serve([run(632, { fetchedAt: undefined, fetchError: { kind: "binary_missing", message: "x" } })]);
		renderView();

		expect(
			await screen.findByText("The testiny CLI is not installed (looked on PATH and in ~/go/bin)."),
		).toBeInTheDocument();
		expect(within(card(632)).getByText("couldn't read this run yet")).toBeInTheDocument();
	});

	it("keeps the reason on the card when only some runs failed", async () => {
		serve([run(632), run(640, { fetchedAt: minutesAgo(12), fetchError: { kind: "auth", message: "403" } })]);
		renderView();

		expect(
			await within(await waitFor(() => card(640))).findByText(
				"Testiny rejected the key · showing data from 12 min ago",
			),
		).toBeInTheDocument();
		expect(screen.queryByText(/Testiny key missing or rejected/)).not.toBeInTheDocument();
	});

	it("says a deleted run is gone and still offers to unlink it", async () => {
		serve([run(632, { fetchedAt: undefined, fetchError: { kind: "not_found", message: "404" } })]);
		renderView();

		const gone = within(await waitFor(() => card(632)));
		expect(gone.getByText("Run not found in Testiny (deleted?)")).toBeInTheDocument();
		expect(gone.getByRole("button", { name: "Unlink" })).toBeInTheDocument();
	});

	it("shows the empty state when no run is linked", async () => {
		serve([]);
		renderView();

		expect(await screen.findByText("No test runs linked")).toBeInTheDocument();
		expect(
			screen.getByText("Agents link a run after you approve it. You can also paste a run id or URL above."),
		).toBeInTheDocument();
		expect(screen.getByPlaceholderText("Run id or Testiny URL")).toBeInTheDocument();
	});

	it("says why the list could not load instead of rendering nothing", async () => {
		getMock.mockResolvedValue({
			data: undefined,
			error: { code: "TESTINY_OFF", message: "this project does not use Testiny" },
		});
		renderView();

		expect(await screen.findByText("this project does not use Testiny")).toBeInTheDocument();
		expect(screen.queryByRole("article")).not.toBeInTheDocument();
	});
});

describe("TestinyView setting a result", () => {
	const row = (title: string) => {
		const li = screen.getByText(title).closest("li");
		if (!li) throw new Error(`no row for ${title}`);
		return within(li);
	};
	const results = "/api/v1/sessions/{sessionId}/testiny/runs/{runId}/results";
	const posted = (runId: number, body: unknown) => [
		results,
		{ params: { path: { sessionId: "task-1", runId: String(runId) } }, body },
	];

	function serveTwoOpen() {
		serve([
			run(632, {
				counts: { FAILED: 1, NOTRUN: 1 },
				cases: [tc(1, "FAILED", "disclaimer"), tc(2, "NOTRUN", "cold launch")],
			}),
		]);
	}

	it("offers every status from the status word and marks the current one", async () => {
		const user = userEvent.setup();
		serveTwoOpen();
		renderView();

		await user.click(await waitFor(() => row("cold launch").getByRole("button", { name: "Result: Not run" })));
		const items = await screen.findAllByRole("menuitemradio");
		expect(items.map((i) => i.textContent)).toEqual(["Passed", "Failed", "Blocked", "Skipped", "Not run"]);
		expect(items.map((i) => i.getAttribute("aria-checked"))).toEqual(["false", "false", "false", "false", "true"]);
	});

	it("saves Passed at once, as the person, and counts it", async () => {
		const user = userEvent.setup();
		serveTwoOpen();
		let answer: (value: unknown) => void = () => undefined;
		postMock.mockImplementation(() => new Promise((resolve) => (answer = resolve)));
		renderView();

		await user.click(await waitFor(() => row("cold launch").getByRole("button", { name: "Result: Not run" })));
		await user.click(await screen.findByRole("menuitemradio", { name: "Passed" }));

		expect(postMock).toHaveBeenCalledWith(...posted(632, { results: [{ caseId: 2, status: "PASSED" }] }));
		expect(row("cold launch").getByRole("button", { name: "Result: Passed" })).toBeInTheDocument();
		expect(screen.getByRole("listitem", { name: "1 Passed" })).toBeInTheDocument();
		expect(screen.queryByRole("listitem", { name: /Not run$/ })).not.toBeInTheDocument();

		answer({
			data: run(632, {
				counts: { FAILED: 1, PASSED: 1 },
				cases: [
					tc(1, "FAILED", "disclaimer"),
					{
						...tc(2, "PASSED", "cold launch"),
						recorded: { status: "PASSED", comment: "", by: "", sha: "", at: minutesAgo(0) },
					},
				],
			}),
			error: undefined,
		});
		expect(await row("cold launch").findByText("set by you · just now")).toBeInTheDocument();
	});

	it("keeps a case it just passed in its place instead of folding it away", async () => {
		const user = userEvent.setup();
		serveTwoOpen();
		postMock.mockResolvedValue({
			data: run(632, {
				counts: { FAILED: 1, PASSED: 1 },
				cases: [tc(1, "FAILED", "disclaimer"), tc(2, "PASSED", "cold launch")],
			}),
			error: undefined,
		});
		renderView();

		await user.click(await waitFor(() => row("cold launch").getByRole("button", { name: "Result: Not run" })));
		await user.click(await screen.findByRole("menuitemradio", { name: "Passed" }));
		await waitFor(() => expect(postMock).toHaveBeenCalled());

		const open = within(screen.getByRole("list", { name: "Cases" }));
		expect(open.getAllByRole("listitem").map((li) => li.textContent)).toEqual([
			"Faileddisclaimer",
			"Passedcold launch",
		]);

		serve([
			run(632, {
				counts: { FAILED: 1, PASSED: 1 },
				cases: [tc(1, "FAILED", "disclaimer"), tc(2, "PASSED", "cold launch")],
			}),
		]);
		await user.click(screen.getByRole("button", { name: "Refresh" }));
		expect(await screen.findByRole("button", { name: "1 passed" })).toBeInTheDocument();
		expect(screen.queryByText("cold launch")).not.toBeInTheDocument();
	});

	it("asks why before saving Failed, and posts the reason on Enter", async () => {
		const user = userEvent.setup();
		serveTwoOpen();
		postMock.mockResolvedValue({ data: run(632), error: undefined });
		renderView();

		await user.click(await waitFor(() => row("cold launch").getByRole("button", { name: "Result: Not run" })));
		await user.click(await screen.findByRole("menuitemradio", { name: "Failed" }));

		const field = await screen.findByRole("textbox", { name: "What went wrong" });
		expect(field).toHaveFocus();
		expect(field).toHaveAttribute("placeholder", "บอกสั้น ๆ ว่าเกิดอะไรขึ้น (1-2 ประโยค)");
		expect(field).toHaveAttribute("maxLength", "300");
		const save = screen.getByRole("button", { name: "Save" });
		expect(save).toBeDisabled();
		await user.type(field, "   ");
		expect(save).toBeDisabled();
		await user.keyboard("{Enter}");
		expect(postMock).not.toHaveBeenCalled();

		await user.type(field, "ปุ่มแชร์ไม่ขึ้นหลังเปิดแอปใหม่{Enter}");
		expect(postMock).toHaveBeenCalledWith(
			...posted(632, { results: [{ caseId: 2, status: "FAILED", comment: "ปุ่มแชร์ไม่ขึ้นหลังเปิดแอปใหม่" }] }),
		);
		expect(screen.queryByRole("textbox", { name: "What went wrong" })).not.toBeInTheDocument();
	});

	it("works from the keyboard alone", async () => {
		const user = userEvent.setup();
		serveTwoOpen();
		postMock.mockResolvedValue({ data: run(632), error: undefined });
		renderView();

		const trigger = await waitFor(() => row("cold launch").getByRole("button", { name: "Result: Not run" }));
		trigger.focus();
		await user.keyboard("{Enter}");
		await screen.findByRole("menu");
		// Opening from the keyboard lands on the first status, Passed.
		await user.keyboard("{ArrowDown}{Enter}");

		const field = await screen.findByRole("textbox", { name: "What went wrong" });
		await waitFor(() => expect(field).toHaveFocus());
		await user.keyboard("ค้างที่หน้าโหลด{Enter}");
		expect(postMock).toHaveBeenCalledWith(
			...posted(632, { results: [{ caseId: 2, status: "FAILED", comment: "ค้างที่หน้าโหลด" }] }),
		);
	});

	it("keeps focus on the status while a write is on its way, and offers no second one", async () => {
		const user = userEvent.setup();
		serveTwoOpen();
		postMock.mockImplementation(() => new Promise(() => undefined));
		renderView();

		await user.click(await waitFor(() => row("cold launch").getByRole("button", { name: "Result: Not run" })));
		await user.click(await screen.findByRole("menuitemradio", { name: "Failed" }));
		await user.type(await screen.findByRole("textbox", { name: "What went wrong" }), "ค้าง{Enter}");

		const trigger = row("cold launch").getByRole("button", { name: "Result: Failed" });
		await waitFor(() => expect(trigger).toHaveAttribute("aria-disabled", "true"));
		expect(trigger).toHaveFocus();
		await user.keyboard("{Enter}");
		expect(screen.queryByRole("menu")).not.toBeInTheDocument();
		await user.click(trigger);
		expect(screen.queryByRole("menu")).not.toBeInTheDocument();
		expect(postMock).toHaveBeenCalledTimes(1);
	});

	it("cancels on Esc without saving or changing the status", async () => {
		const user = userEvent.setup();
		serveTwoOpen();
		renderView();

		const trigger = await waitFor(() => row("cold launch").getByRole("button", { name: "Result: Not run" }));
		await user.click(trigger);
		await user.click(await screen.findByRole("menuitemradio", { name: "Blocked" }));
		const field = await screen.findByRole("textbox", { name: "Why it is blocked" });
		await user.type(field, "no device{Escape}");

		expect(screen.queryByRole("textbox", { name: "Why it is blocked" })).toBeNull();
		expect(postMock).not.toHaveBeenCalled();
		expect(row("cold launch").getByRole("button", { name: "Result: Not run" })).toHaveFocus();
	});

	it("puts the case back and shows the daemon's reason when the write is refused", async () => {
		const user = userEvent.setup();
		serveTwoOpen();
		postMock.mockResolvedValue({
			data: undefined,
			error: {
				code: "TESTINY_RESULT_SET_BY_PERSON",
				message: "TC-1 was set by a person; report it in the handback instead",
			},
		});
		renderView();

		await user.click(await waitFor(() => row("disclaimer").getByRole("button", { name: "Result: Failed" })));
		await user.click(await screen.findByRole("menuitemradio", { name: "Passed" }));

		expect(await row("disclaimer").findByRole("alert")).toHaveTextContent(
			"TC-1 was set by a person; report it in the handback instead",
		);
		expect(row("disclaimer").getByRole("button", { name: "Result: Failed" })).toBeInTheDocument();
		expect(screen.getByRole("listitem", { name: "1 Failed" })).toBeInTheDocument();
		expect(screen.queryByRole("listitem", { name: /Passed$/ })).not.toBeInTheDocument();
	});

	it("says who set a result through AO, when, and on which commit", async () => {
		serve([
			run(632, {
				cases: [
					{
						...tc(1, "FAILED", "disclaimer"),
						recorded: {
							status: "FAILED",
							comment: "x",
							by: "task-1-qa",
							byRole: "qa",
							sha: "4f2c9e1d0b7a",
							at: minutesAgo(5),
						},
					},
					{
						...tc(2, "NOTRUN", "cold launch"),
						recorded: { status: "NOTRUN", comment: "", by: "", sha: "", at: minutesAgo(2) },
					},
					tc(3, "BLOCKED", "voiceover"),
				],
			}),
		]);
		renderView();

		expect(await waitFor(() => row("disclaimer").getByText("set by qa · 5 min ago · on 4f2c9e1"))).toBeInTheDocument();
		expect(row("cold launch").getByText("set by you · 2 min ago")).toBeInTheDocument();
		expect(row("voiceover").queryByText(/^set by/)).toBeNull();
	});
});
