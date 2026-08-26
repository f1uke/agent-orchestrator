import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock },
	apiErrorMessage: (_error: unknown, fallback: string) => fallback,
}));

function wrapper({ children }: { children: ReactNode }) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, retryDelay: 0 } } });
	return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

/**
 * The hook reads `import.meta.env.VITE_NO_ELECTRON` at MODULE scope, the way
 * every other daemon-backed hook in this app does. So the flag has to be stubbed
 * before the module is evaluated, which means resetting the registry and
 * importing dynamically rather than at the top of the file.
 */
async function loadHook(previewFlag: string | undefined) {
	vi.resetModules();
	if (previewFlag === undefined) vi.unstubAllEnvs();
	else vi.stubEnv("VITE_NO_ELECTRON", previewFlag);
	return (await import("./useWorkspaceFile")).useWorkspaceFile;
}

beforeEach(() => {
	getMock.mockReset();
});

afterEach(() => {
	vi.unstubAllEnvs();
});

describe("useWorkspaceFile", () => {
	// The regression this hook exists for: with no preview branch the request
	// went to a daemon that is not there, and the viewer rendered NOTHING — no
	// editor, no "Loading file…", no error — because a query between retries has
	// no data, no error and isLoading false.
	it("serves mock content with no daemon under VITE_NO_ELECTRON", async () => {
		const useWorkspaceFile = await loadHook("1");

		const { result } = renderHook(() => useWorkspaceFile("demo-working", "frontend/src/renderer/lib/utils.ts"), {
			wrapper,
		});

		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		expect(getMock).not.toHaveBeenCalled();
		expect(result.current.data?.available).toBe(true);
		expect(result.current.data?.lines.length).toBeGreaterThan(20);
		expect(result.current.data?.contentHash).toMatch(/^sha256:/);
		expect(result.current.data?.changedLines.length).toBeGreaterThan(0);
	});

	it("reads through the daemon when there is one", async () => {
		const useWorkspaceFile = await loadHook(undefined);
		getMock.mockResolvedValue({
			data: {
				available: true,
				path: "a.go",
				lines: [{ kind: "context", text: "package main", oldLine: 1, newLine: 1 }],
				changedLines: [],
				contentHash: "sha256:real",
				trailingNewline: true,
				truncated: false,
			},
			error: undefined,
		});

		const { result } = renderHook(() => useWorkspaceFile("ao-1", "a.go"), { wrapper });

		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		expect(getMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/workspace/file", {
			params: { path: { sessionId: "ao-1" }, query: { path: "a.go" } },
		});
		expect(result.current.data?.contentHash).toBe("sha256:real");
	});

	it("surfaces a daemon error rather than resolving to nothing", async () => {
		const useWorkspaceFile = await loadHook(undefined);
		getMock.mockResolvedValue({ data: undefined, error: { code: "SESSION_NOT_FOUND" } });

		const { result } = renderHook(() => useWorkspaceFile("ao-1", "a.go"), { wrapper });

		await waitFor(() => expect(result.current.isError).toBe(true));
		expect(result.current.error?.message).toBe("Unable to load file");
	});
});
