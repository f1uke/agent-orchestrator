import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { useLanguageServer } from "./use-language-server";

type StateEvent = { handleId: string; key: string; state: string; detail?: string };

function installBridge() {
	let seq = 0;
	let stateListener: (e: StateEvent) => void = () => {};
	const attach = vi.fn(async (input: { root: string; languageId: string }) => ({
		handleId: `h${++seq}`,
		key: `${input.languageId} ${input.root}`,
		state: "ready" as const,
	}));
	const detach = vi.fn();
	const bridge = {
		attach,
		detach,
		send: vi.fn(),
		noteResult: vi.fn(),
		health: vi.fn(async () => []),
		onMessage: () => () => undefined,
		onState: (cb: (e: StateEvent) => void) => {
			stateListener = cb;
			return () => {
				stateListener = () => {};
			};
		},
	};
	(globalThis as unknown as { ao: { lsp: unknown } }).ao = { lsp: bridge };
	return { bridge, attach, detach, emitState: (e: StateEvent) => act(() => stateListener(e)) };
}

let harness: ReturnType<typeof installBridge>;
let previousAo: unknown;

beforeEach(() => {
	previousAo = (globalThis as unknown as { ao?: unknown }).ao;
	harness = installBridge();
});

afterEach(() => {
	(globalThis as unknown as { ao?: unknown }).ao = previousAo;
});

describe("useLanguageServer", () => {
	test("attaches once and hands back a ready client", async () => {
		const { result } = renderHook(() => useLanguageServer("/root", "go"));
		await waitFor(() => expect(result.current.state).toBe("ready"));
		expect(result.current.client).not.toBeNull();
		expect(harness.attach).toHaveBeenCalledTimes(1);
		expect(harness.attach).toHaveBeenCalledWith({ root: "/root", languageId: "go" });
	});

	test("detaches on unmount", async () => {
		const { result, unmount } = renderHook(() => useLanguageServer("/root", "go"));
		await waitFor(() => expect(result.current.client).not.toBeNull());
		unmount();
		expect(harness.detach).toHaveBeenCalledTimes(1);
	});

	test("a stopped server re-attaches, with a FRESH client", async () => {
		// The self-heal the spike's renderer sweep proved, and the reason the client
		// must be NEW rather than reused: the replacement server knows nothing about
		// the documents the old one had open, so carrying the old client's `opened`
		// set forward is what left the prototype's pane dead.
		const { result } = renderHook(() => useLanguageServer("/root", "go"));
		await waitFor(() => expect(result.current.client).not.toBeNull());
		const first = result.current.client;
		harness.emitState({ handleId: first!.handleId, key: "go /root", state: "stopped", detail: "idle" });
		await waitFor(() => expect(result.current.client).not.toBe(first));
		await waitFor(() => expect(result.current.state).toBe("ready"));
		expect(harness.attach).toHaveBeenCalledTimes(2);
	});

	test("a failed attach surfaces `failed` with the reason, never silence", async () => {
		harness.bridge.attach = vi.fn(async () => {
			throw new Error("gopls: spawn ENOENT");
		});
		const { result } = renderHook(() => useLanguageServer("/root", "go"));
		await waitFor(() => expect(result.current.state).toBe("failed"));
		expect(result.current.detail).toContain("ENOENT");
		expect(result.current.client).toBeNull();
	});

	test("a failed server does NOT re-attach in a loop", async () => {
		// `stopped` self-heals; `failed` must not, or a missing gopls becomes an
		// infinite spawn loop that is invisible except as CPU.
		const { result } = renderHook(() => useLanguageServer("/root", "go"));
		await waitFor(() => expect(result.current.client).not.toBeNull());
		harness.emitState({
			handleId: result.current.client!.handleId,
			key: "go /root",
			state: "failed",
			detail: "gopls exited (1)",
		});
		await waitFor(() => expect(result.current.state).toBe("failed"));
		await new Promise((r) => setTimeout(r, 50));
		expect(harness.attach).toHaveBeenCalledTimes(1);
	});

	test("no language and no root both mean `unavailable`, and never attach", () => {
		const a = renderHook(() => useLanguageServer("/root", null));
		const b = renderHook(() => useLanguageServer(undefined, "go"));
		expect(a.result.current.state).toBe("unavailable");
		expect(b.result.current.state).toBe("unavailable");
		expect(harness.attach).not.toHaveBeenCalled();
	});

	test("no Electron bridge means `unavailable`, not a crash", async () => {
		delete (globalThis as unknown as { ao?: unknown }).ao;
		const { result } = renderHook(() => useLanguageServer("/root", "go"));
		await waitFor(() => expect(result.current.state).toBe("unavailable"));
		expect(result.current.client).toBeNull();
	});
});
