import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
	useCompleteWikiTask,
	useSaveWikiNote,
	useSaveWikiTasksSettings,
	wikiTasksQueryKey,
	type WikiTasksSettings,
} from "./useWiki";

const { putMock, postMock } = vi.hoisted(() => ({ putMock: vi.fn(), postMock: vi.fn() }));

vi.mock("../lib/api-client", () => ({
	apiClient: { PUT: putMock, POST: postMock },
	apiErrorMessage: (_error: unknown, fallback?: string) => fallback ?? "failed",
}));

function wrapper({ children }: { children: ReactNode }) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

const settings: WikiTasksSettings = {
	folders: ["Areas"],
	sections: ["Tasks"],
	cutoff: "2026-01-01",
	ownerAliases: ["fluke"],
	requireCreated: false,
};

/** What the daemon sends back, so the mutation has something to settle with. */
function echo(next: WikiTasksSettings) {
	putMock.mockResolvedValue({ data: next, error: undefined });
}

beforeEach(() => {
	putMock.mockReset();
	postMock.mockReset();
});

/**
 * 🗝 A write the APP made is the one vault change the tab can know about
 * instantly, and it used to be the one it ignored. Nothing re-read the task
 * list after a tick, so the row the reader had just ticked off stayed on the
 * list until the next poll - and the poll does not run while the window is
 * hidden, which is how a ticked row survived for as long as the reader was
 * looking somewhere else.
 */
describe("re-reading the tasks after a write the app made", () => {
	function withClient() {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
		const invalidated = vi.spyOn(client, "invalidateQueries");
		const wrap = ({ children }: { children: ReactNode }) => (
			<QueryClientProvider client={client}>{children}</QueryClientProvider>
		);
		const askedForTasks = () =>
			invalidated.mock.calls.some((call) => String(call[0]?.queryKey) === String(wikiTasksQueryKey));
		return { wrap, askedForTasks };
	}

	it("re-reads the list once a tick has been written", async () => {
		postMock.mockResolvedValue({ data: { moved: false }, error: undefined });
		const { wrap, askedForTasks } = withClient();

		const { result } = renderHook(() => useCompleteWikiTask(), { wrapper: wrap });
		result.current.mutate({ path: "Areas/a.md", line: 4, raw: "- [ ] the row" });

		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		expect(askedForTasks()).toBe(true);
	});

	// Task rows live in notes, so saving a note in the editor can tick one off,
	// reword it or remove it. The Tasks tab has no other way to hear about it.
	it("re-reads the list once a note has been saved", async () => {
		putMock.mockResolvedValue({ data: { contentHash: "h2", size: 12, modifiedAt: "now" }, error: undefined });
		const { wrap, askedForTasks } = withClient();

		const { result } = renderHook(() => useSaveWikiNote(), { wrapper: wrap });
		result.current.mutate({ path: "Areas/a.md", content: "- [x] the row", baseHash: "h1" });

		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		expect(askedForTasks()).toBe(true);
	});
});

/**
 * 🗝 These assert the REQUEST BODY, not that the form called its callback.
 *
 * The bug this file exists for lived exactly here and nowhere else: the form
 * held `requireCreated` correctly and the daemon stored it correctly, while the
 * mutation hand-listed four of the five fields on its way out. Every field on
 * the wire is optional, so the omission did not merely fail to save - it wrote
 * `false` over a stored `true` on every save. A test that stops at the
 * component boundary passes through all of that.
 */
describe("useSaveWikiTasksSettings", () => {
	it("sends requireCreated: true", async () => {
		const next = { ...settings, requireCreated: true };
		echo(next);

		const { result } = renderHook(() => useSaveWikiTasksSettings(), { wrapper });
		result.current.mutate(next);

		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		expect(putMock).toHaveBeenCalledWith("/api/v1/settings/wiki/tasks", { body: next });
		expect(putMock.mock.calls[0][1].body.requireCreated).toBe(true);
	});

	// Turning the switch OFF has to reach the daemon too: a fix that only ever
	// sends `true` is the same bug wearing the other sign.
	it("sends requireCreated: false", async () => {
		echo(settings);

		const { result } = renderHook(() => useSaveWikiTasksSettings(), { wrapper });
		result.current.mutate(settings);

		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		expect(putMock.mock.calls[0][1].body.requireCreated).toBe(false);
		expect("requireCreated" in putMock.mock.calls[0][1].body).toBe(true);
	});

	// The other four fields are what the request USED to carry, so they are the
	// thing a "send it whole" rewrite could plausibly drop.
	it("sends every field the caller passed", async () => {
		echo(settings);

		const { result } = renderHook(() => useSaveWikiTasksSettings(), { wrapper });
		result.current.mutate(settings);

		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		expect(putMock.mock.calls[0][1].body).toEqual({
			folders: ["Areas"],
			sections: ["Tasks"],
			cutoff: "2026-01-01",
			ownerAliases: ["fluke"],
			requireCreated: false,
		});
	});
});
