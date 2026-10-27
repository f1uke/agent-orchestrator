import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, renderHook, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { demoBoard } from "../../lib/releases/fixture";
import { NoMrButton } from "./NoMr";
import { ReadOnlyError, ReleasesCapabilitiesProvider } from "./ReleasesCapabilities";
import { useFixes, useFixPlan, useRefreshSources, useSavePlan, useSetRule } from "../../hooks/useReleaseBoard";

const { getMock, postMock, putMock, deleteMock } = vi.hoisted(() => ({
	getMock: vi.fn(),
	postMock: vi.fn(),
	putMock: vi.fn(),
	deleteMock: vi.fn(),
}));

vi.mock("../../lib/api-client", () => ({
	apiClient: { GET: getMock, POST: postMock, PUT: putMock, DELETE: deleteMock },
	apiErrorMessage: (error: unknown, fallback = "Request failed") =>
		(error as { message?: string } | null)?.message ?? fallback,
	getApiBaseUrl: () => "http://127.0.0.1:3001",
	hasTrustedApiBaseUrl: () => true,
	subscribeApiBaseUrl: () => () => {},
}));

function wrapper(readOnly: boolean) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return ({ children }: { children: ReactNode }) => (
		<QueryClientProvider client={client}>
			<ReleasesCapabilitiesProvider readOnly={readOnly}>{children}</ReleasesCapabilitiesProvider>
		</QueryClientProvider>
	);
}

const board = demoBoard("2026-10-14");
const markable = board.trains.flatMap((t) => t.versions.flatMap((v) => v.cards)).find((u) => u.code.kind === "none")
	?.key as string;

beforeEach(() => {
	for (const m of [getMock, postMock, putMock, deleteMock])
		m.mockReset().mockResolvedValue({ data: {}, error: undefined });
});

describe("the No MR needed control", () => {
	it("is drawn by default and not drawn when the host is read-only", () => {
		const { unmount } = render(<NoMrButton board={board} k={markable} variant="row" />, { wrapper: wrapper(false) });
		expect(screen.getByTestId("no-mr-toggle")).toBeTruthy();
		unmount();
		render(<NoMrButton board={board} k={markable} variant="link" />, { wrapper: wrapper(true) });
		expect(screen.queryByTestId("no-mr-toggle")).toBeNull();
	});
});

describe("a read-only host", () => {
	it("refuses every write before it reaches the daemon", async () => {
		const w = wrapper(true);
		const rule = renderHook(() => useSetRule(), { wrapper: w });
		const refresh = renderHook(() => useRefreshSources(), { wrapper: w });
		const plan = renderHook(() => useSavePlan(), { wrapper: w });
		await act(async () => {
			await expect(rule.result.current.mutateAsync({ id: "A1", on: false })).rejects.toBeInstanceOf(ReadOnlyError);
			await expect(refresh.result.current.mutateAsync()).rejects.toBeInstanceOf(ReadOnlyError);
			await expect(plan.result.current.mutateAsync({ id: "x", draft: {} as never })).rejects.toBeInstanceOf(
				ReadOnlyError,
			);
		});
		expect([putMock, postMock, deleteMock].map((m) => m.mock.calls.length)).toEqual([0, 0, 0]);
	});

	it("does not ask for the fixes or a fix plan", async () => {
		const w = wrapper(true);
		renderHook(() => useFixes(), { wrapper: w });
		renderHook(() => useFixPlan("C1~v/21-2026/di", undefined, true), { wrapper: w });
		await new Promise((r) => setTimeout(r, 30));
		expect(getMock).not.toHaveBeenCalled();
		expect(postMock).not.toHaveBeenCalled();
	});

	it("still writes when the host allows it, so the guard is not what makes the tests pass", async () => {
		const rule = renderHook(() => useSetRule(), { wrapper: wrapper(false) });
		await act(async () => {
			await rule.result.current.mutateAsync({ id: "A1", on: false });
		});
		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		renderHook(() => useFixes(), { wrapper: wrapper(false) });
		await waitFor(() => expect(getMock).toHaveBeenCalledWith("/api/v1/release-trains/fixes"));
	});
});
