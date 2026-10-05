import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useWorkspaceChanges } from "./useWorkspaceChanges";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock },
	apiErrorMessage: (_error: unknown, fallback = "Request failed") => fallback,
}));

function wrapper({ children }: { children: ReactNode }) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

const current = { available: true, targetBranch: "main", targetFetch: "current", files: [], truncated: false };

beforeEach(() => {
	vi.useFakeTimers({ shouldAdvanceTime: true });
	getMock.mockReset();
	getMock.mockResolvedValue({ data: current, error: undefined });
});

afterEach(() => {
	vi.useRealTimers();
});

describe("useWorkspaceChanges", () => {
	// While the list is on screen it rereads on its own, so the daemon gets a
	// chance to refresh the target and the human rarely needs the button.
	it("rereads every minute while auto-refresh is on", async () => {
		renderHook(() => useWorkspaceChanges("s1"), { wrapper });
		await waitFor(() => expect(getMock).toHaveBeenCalledTimes(1));

		await act(async () => {
			await vi.advanceTimersByTimeAsync(60_000);
		});
		await waitFor(() => expect(getMock).toHaveBeenCalledTimes(2));
	});

	it("does not poll while the list is off screen", async () => {
		renderHook(() => useWorkspaceChanges("s1", { autoRefresh: false }), { wrapper });
		await waitFor(() => expect(getMock).toHaveBeenCalledTimes(1));

		await act(async () => {
			await vi.advanceTimersByTimeAsync(120_000);
		});
		expect(getMock).toHaveBeenCalledTimes(1);
	});

	// A retry after a failure keeps reading "failed" until it lands; polling
	// through it is what shows the recovery as soon as it happens.
	it("polls fast while a retry runs after a failure", async () => {
		getMock.mockResolvedValueOnce({
			data: { ...current, targetFetch: "failed", targetFetchInFlight: true },
			error: undefined,
		});
		renderHook(() => useWorkspaceChanges("s1", { autoRefresh: false }), { wrapper });
		await waitFor(() => expect(getMock).toHaveBeenCalledTimes(1));

		await act(async () => {
			await vi.advanceTimersByTimeAsync(1_000);
		});
		await waitFor(() => expect(getMock).toHaveBeenCalledTimes(2));
	});

	// A fetch in flight is polled fast, so its result lands within a second
	// rather than on the next minute's tick.
	it("polls fast while the target is being fetched", async () => {
		getMock.mockResolvedValueOnce({ data: { ...current, targetFetch: "refreshing" }, error: undefined });
		renderHook(() => useWorkspaceChanges("s1", { autoRefresh: false }), { wrapper });
		await waitFor(() => expect(getMock).toHaveBeenCalledTimes(1));

		await act(async () => {
			await vi.advanceTimersByTimeAsync(1_000);
		});
		await waitFor(() => expect(getMock).toHaveBeenCalledTimes(2));
	});
});
