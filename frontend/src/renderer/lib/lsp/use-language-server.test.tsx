import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { useDocumentStatus, useLanguageServer } from "./use-language-server";

type StateEvent = { handleId: string; key: string; state: string; detail?: string; need?: string };

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

	test("a workspace that is not set up yet waits, then comes alive once main says it is", async () => {
		// 🗝 The 2026-10-02 report: a moved worktree with no Xcode build of its new
		// path. That is a wait for one build, not a failure - and once the build
		// lands, main reports `stopped` and the pane must re-attach on its own
		// rather than ask the person to close and reopen the file.
		const reason = "Xcode has never built NterWorkspace.xcworkspace from this worktree.";
		let calls = 0;
		harness.bridge.attach = vi.fn(async (input: { root: string; languageId: string }) => {
			calls++;
			return calls === 1
				? {
						handleId: "wait-1",
						key: `${input.languageId} ${input.root}`,
						state: "unconfigured" as const,
						detail: reason,
						need: "build",
					}
				: { handleId: "h2", key: `${input.languageId} ${input.root}`, state: "ready" as const };
		}) as unknown as typeof harness.attach;
		const { result } = renderHook(() => useLanguageServer("/root", "swift"));
		await waitFor(() => expect(result.current.state).toBe("unconfigured"));
		expect(result.current).toMatchObject({ client: null, detail: reason, need: "build" });

		harness.emitState({ handleId: "wait-1", key: "swift /root", state: "stopped", detail: "set up" });
		await waitFor(() => expect(result.current.state).toBe("ready"));
		expect(result.current.client).not.toBeNull();
		expect(calls).toBe(2);
	});

	test("a waiting pane follows a change of reason without re-attaching", async () => {
		harness.bridge.attach = vi.fn(async () => ({
			handleId: "wait-1",
			key: "swift /root",
			state: "unconfigured" as const,
			detail: "install xcode-build-server",
			need: "tool",
		})) as unknown as typeof harness.attach;
		const { result } = renderHook(() => useLanguageServer("/root", "swift"));
		await waitFor(() => expect(result.current.need).toBe("tool"));
		harness.emitState({
			handleId: "wait-1",
			key: "swift /root",
			state: "unconfigured",
			detail: "build it",
			need: "build",
		});
		await waitFor(() => expect(result.current.need).toBe("build"));
		expect(result.current.detail).toBe("build it");
		expect(harness.bridge.attach).toHaveBeenCalledTimes(1);
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

describe("useDocumentStatus", () => {
	type Status = { built: true } | { built: false; reason: string };
	const WAITING: Status = {
		built: false,
		reason: "No Xcode build of this worktree that AO has read compiled this file yet.",
	};

	function installStatusBridge(initial: Status) {
		let status = initial;
		let settingsListener: (e: { handleId: string; key: string }) => void = () => {};
		const documentStatus = vi.fn(async () => status);
		Object.assign(harness.bridge, {
			documentStatus,
			onSettings: (cb: (e: { handleId: string; key: string }) => void) => {
				settingsListener = cb;
				return () => {
					settingsListener = () => {};
				};
			},
		});
		return {
			documentStatus,
			set: (next: Status) => {
				status = next;
			},
			emitSettings: (e: { handleId: string; key: string }) => act(() => settingsListener(e)),
		};
	}

	function renderBoth(path: string) {
		return renderHook(
			({ p }) => {
				const server = useLanguageServer("/root", "swift");
				return { server, status: useDocumentStatus(server.client, p) };
			},
			{ initialProps: { p: path } },
		);
	}

	test("asks main about THIS file on THIS server, and is null until the answer is in", async () => {
		const bridge = installStatusBridge(WAITING);
		const { result } = renderBoth("/root/Chat/ChatNotice.swift");
		expect(result.current.status).toBeNull();
		await waitFor(() => expect(result.current.status).toEqual(WAITING));
		expect(bridge.documentStatus).toHaveBeenCalledWith(
			result.current.server.client?.handleId,
			"/root/Chat/ChatNotice.swift",
		);
	});

	test("asks again when main says the settings moved, and the wait clears", async () => {
		const bridge = installStatusBridge(WAITING);
		const { result } = renderBoth("/root/Chat/ChatNotice.swift");
		await waitFor(() => expect(result.current.status).toEqual(WAITING));
		const handleId = result.current.server.client?.handleId ?? "";

		bridge.set({ built: true });
		// Another server's news is not this file's.
		bridge.emitSettings({ handleId: "someone-else", key: "swift /other" });
		await new Promise((r) => setTimeout(r, 20));
		expect(result.current.status).toEqual(WAITING);

		bridge.emitSettings({ handleId, key: "swift /root" });
		await waitFor(() => expect(result.current.status).toEqual({ built: true }));
	});

	test("another file never inherits the last file's answer", async () => {
		const bridge = installStatusBridge(WAITING);
		const { result, rerender } = renderBoth("/root/A.swift");
		await waitFor(() => expect(result.current.status).toEqual(WAITING));
		bridge.set({ built: true });
		rerender({ p: "/root/B.swift" });
		expect(result.current.status).toBeNull();
		await waitFor(() => expect(result.current.status).toEqual({ built: true }));
	});

	test("an older bridge without the question means built: it never withholds for ever", async () => {
		const { result } = renderBoth("/root/A.swift");
		await waitFor(() => expect(result.current.status).toEqual({ built: true }));
	});

	test("a failed question means built too, rather than a file with no diagnostics for ever", async () => {
		const bridge = installStatusBridge(WAITING);
		bridge.documentStatus.mockRejectedValueOnce(new Error("ipc gone"));
		const { result } = renderBoth("/root/A.swift");
		await waitFor(() => expect(result.current.status).toEqual({ built: true }));
	});
});
