import { describe, expect, test } from "vitest";
import type { InfillRequest, InfillResult } from "./infill";
import { createRequestLane } from "./request-lane";

const req = (prompt: string, nPredict?: number): InfillRequest => ({
	inputPrefix: "",
	prompt,
	inputSuffix: "",
	inputExtra: [],
	nIndent: 0,
	nPredict,
});

/** A server that answers when told to, so the test controls the interleaving. */
function controllable() {
	const pending: {
		request: InfillRequest;
		signal: AbortSignal;
		resolve: (r: InfillResult) => void;
		reject: (e: unknown) => void;
	}[] = [];
	const send = (request: InfillRequest, signal: AbortSignal) =>
		new Promise<InfillResult>((resolve, reject) => {
			const job = { request, signal, resolve, reject };
			pending.push(job);
			signal.addEventListener(
				"abort",
				() => {
					pending.splice(pending.indexOf(job), 1);
					reject(signal.reason);
				},
				{ once: true },
			);
		});
	const answer = (i = 0) => {
		const job = pending.splice(i, 1)[0];
		job.resolve({
			content: `<${job.request.prompt}>`,
			promptTokens: 0,
			promptMs: 0,
			predictedTokens: 0,
			predictedMs: 0,
			wallMs: 0,
		});
	};
	return { send, pending, answer };
}

const tick = () => new Promise((r) => setTimeout(r, 0));

describe("createRequestLane", () => {
	test("one on the wire; the newest replaces whatever was waiting", async () => {
		const server = controllable();
		const lane = createRequestLane(server.send, { timeoutMs: 5_000 });
		const a = lane.submit("a", req("f"));
		const b = lane.submit("b", req("fo"));
		const c = lane.submit("c", req("foo"));
		await tick();
		expect(server.pending.map((p) => p.request.prompt)).toEqual(["f"]);
		// "fo" was superseded by "foo" before it ever reached the server.
		expect(await b).toBeNull();
		server.answer();
		expect((await a)?.content).toBe("<f>");
		await tick();
		expect(server.pending.map((p) => p.request.prompt)).toEqual(["foo"]);
		server.answer();
		expect((await c)?.content).toBe("<foo>");
	});

	test("cancelling the request on the wire lets it finish - its answer is still delivered", async () => {
		const server = controllable();
		const lane = createRequestLane(server.send, { timeoutMs: 5_000 });
		const a = lane.submit("a", req("f"));
		await tick();
		lane.cancel("a");
		expect(server.pending[0].signal.aborted).toBe(false);
		server.answer();
		expect((await a)?.content).toBe("<f>");
	});

	test("cancelling a waiting request drops it before it is sent", async () => {
		const server = controllable();
		const lane = createRequestLane(server.send, { timeoutMs: 5_000 });
		const a = lane.submit("a", req("f"));
		const b = lane.submit("b", req("fo"));
		lane.cancel("b");
		expect(await b).toBeNull();
		server.answer();
		await a;
		await tick();
		expect(server.pending).toHaveLength(0);
	});

	test("a warm-up on the wire IS aborted when cancelled", async () => {
		const server = controllable();
		const lane = createRequestLane(server.send, { timeoutMs: 5_000 });
		const warm = lane.submit("w", req("", 0));
		await tick();
		lane.cancel("w");
		expect(await warm).toBeNull();
		const next = lane.submit("a", req("f"));
		await tick();
		expect(server.pending.map((p) => p.request.prompt)).toEqual(["f"]);
		server.answer();
		expect((await next)?.content).toBe("<f>");
	});

	test("a warm-up never displaces a prediction that is waiting", async () => {
		const server = controllable();
		const lane = createRequestLane(server.send, { timeoutMs: 5_000 });
		const a = lane.submit("a", req("f"));
		const b = lane.submit("b", req("fo"));
		const warm = lane.submit("w", req("", 0));
		expect(await warm).toBeNull();
		server.answer();
		await a;
		await tick();
		expect(server.pending.map((p) => p.request.prompt)).toEqual(["fo"]);
		server.answer();
		expect((await b)?.content).toBe("<fo>");
	});

	test("a request that hangs is given up at the timeout and the lane moves on", async () => {
		const server = controllable();
		const errors: unknown[] = [];
		const lane = createRequestLane(server.send, { timeoutMs: 30, onError: (e) => errors.push(e) });
		const stuck = lane.submit("a", req("f"));
		const next = lane.submit("b", req("fo"));
		expect(await stuck).toBeNull();
		await tick();
		expect(server.pending.at(-1)?.request.prompt).toBe("fo");
		server.answer(server.pending.length - 1);
		expect((await next)?.content).toBe("<fo>");
		// A timeout is an abort, not a server error: nothing to log.
		expect(errors).toHaveLength(0);
	});

	test("clear() answers everything null", async () => {
		const server = controllable();
		const lane = createRequestLane(server.send, { timeoutMs: 5_000 });
		const a = lane.submit("a", req("f"));
		const b = lane.submit("b", req("fo"));
		await tick();
		lane.clear();
		expect(await a).toBeNull();
		expect(await b).toBeNull();
	});
});
