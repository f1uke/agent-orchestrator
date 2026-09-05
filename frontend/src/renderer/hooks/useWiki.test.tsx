import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useSaveWikiTasksSettings, type WikiTasksSettings } from "./useWiki";

const { putMock } = vi.hoisted(() => ({ putMock: vi.fn() }));

vi.mock("../lib/api-client", () => ({
	apiClient: { PUT: putMock },
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
