import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { afterEach, describe, expect, it } from "vitest";

/**
 * The gesture bridge script, run for real.
 *
 * It lives in the Go tree and every Go test around it fakes the process away,
 * so nothing exercised the script itself - and the two things it is now
 * responsible for are exactly the things a fake cannot check: that it stays
 * resident across requests, and that a held touch is NOT lifted when the batch
 * ends. A mutation removing that second rule survived the whole suite.
 *
 * The native addon is stubbed with a plain module, so this needs neither a mac,
 * a simulator nor the vendored binary - only node, which is what runs this test.
 */

const HERE = path.dirname(fileURLToPath(import.meta.url));
const SCRIPT = path.resolve(HERE, "../../../../backend/internal/simbridge/assets/bridge.mjs");

const STUB_ADDON = `
// Records every call the script makes, one line per call, so the test can read
// back exactly what reached "the device".
const fs = require("node:fs");
const log = process.env.STUB_LOG;
const note = (line) => fs.appendFileSync(log, line + "\\n");
class SimHID {
  constructor(udid) { note("new SimHID " + udid); }
  async touch(type, x, y) { note("touch " + type + " " + x + "," + y); }
  async key(type, usage) { note("key " + type + " " + usage); }
  async button(name) { note("button " + name); }
}
module.exports = {
  SimHID,
  axDescribe: async () => JSON.stringify([]),
  axFrontmost: async () => JSON.stringify({ bundleId: "com.example.app", pid: 1 }),
};
`;

type Bridge = {
	child: ChildProcessWithoutNullStreams;
	send: (request: unknown) => Promise<Record<string, unknown>>;
	calls: () => string[];
	close: () => Promise<void>;
};

let running: Bridge | null = null;

function startBridge(): Bridge {
	const dir = mkdtempSync(path.join(tmpdir(), "ao-bridge-"));
	const addonPath = path.join(dir, "stub-addon.cjs");
	const logPath = path.join(dir, "calls.log");
	writeFileSync(addonPath, STUB_ADDON);
	writeFileSync(logPath, "");

	const child = spawn(process.execPath, [SCRIPT], {
		stdio: ["pipe", "pipe", "pipe", "pipe"],
		env: { ...process.env, STUB_LOG: logPath },
	}) as ChildProcessWithoutNullStreams;

	// Answers come back on descriptor 3, never stdout: the real addon prints to
	// stdout unbidden and would corrupt a shared channel.
	const answers = child.stdio[3] as NodeJS.ReadableStream;
	const pending: ((value: Record<string, unknown>) => void)[] = [];
	let buffered = "";
	answers.on("data", (chunk: Buffer) => {
		buffered += chunk.toString();
		for (;;) {
			const newline = buffered.indexOf("\n");
			if (newline === -1) return;
			const line = buffered.slice(0, newline).trim();
			buffered = buffered.slice(newline + 1);
			if (line) pending.shift()?.(JSON.parse(line));
		}
	});

	const bridge: Bridge = {
		child,
		send: (request) =>
			new Promise((resolve) => {
				pending.push(resolve);
				child.stdin.write(JSON.stringify({ addonPath, ...(request as object) }) + "\n");
			}),
		calls: () =>
			readFileSync(logPath, "utf8")
				.split("\n")
				.filter((line) => line.length > 0),
		close: () =>
			new Promise((resolve) => {
				child.once("exit", () => resolve());
				child.stdin.end();
				setTimeout(() => {
					child.kill("SIGKILL");
					resolve();
				}, 3_000);
			}),
	};
	running = bridge;
	return bridge;
}

afterEach(async () => {
	await running?.close();
	running = null;
});

const touches = (calls: string[]) => calls.filter((line) => line.startsWith("touch "));

describe("the gesture bridge script", () => {
	// The rule the whole drag rests on: a held touch stays down when the batch
	// ends, and an ordinary gesture never does.
	it("leaves the finger down for a held touch and lifts it for an ordinary one", async () => {
		const bridge = startBridge();

		await bridge.send({
			op: "hold",
			udid: "UDID-A",
			events: [{ kind: "touch", type: "begin", x: 0.5, y: 0.9 }],
		});
		expect(touches(bridge.calls())).toEqual(["touch begin 0.5,0.9"]);

		await bridge.send({
			op: "hold",
			udid: "UDID-A",
			events: [{ kind: "touch", type: "move", x: 0.5, y: 0.4 }],
		});
		expect(touches(bridge.calls())).toEqual(["touch begin 0.5,0.9", "touch move 0.5,0.4"]);

		// And an ordinary gesture still ends with the finger up, on the same
		// process, right after a held one.
		await bridge.send({
			op: "perform",
			udid: "UDID-A",
			events: [{ kind: "touch", type: "end", x: 0.5, y: 0.4 }],
		});
		expect(touches(bridge.calls()).at(-1)).toBe("touch end 0.5,0.4");
	});

	// A gesture that puts the finger down and forgets to lift it is the case
	// that wedges a device, and it is the bridge's own job to catch it.
	it("lifts a finger an ordinary gesture left down", async () => {
		const bridge = startBridge();

		const answer = await bridge.send({
			op: "perform",
			udid: "UDID-A",
			events: [{ kind: "touch", type: "begin", x: 0.2, y: 0.2 }],
		});

		expect(answer.ok).toBe(true);
		expect(answer.lifted).toBe(true);
		expect(touches(bridge.calls())).toEqual(["touch begin 0.2,0.2", "touch end 0.2,0.2"]);
	});

	// The reason it stays resident: the injector is built once, and everything
	// after the first touch is free.
	it("builds one injector per device however many requests arrive", async () => {
		const bridge = startBridge();

		for (let i = 0; i < 3; i++) {
			await bridge.send({ op: "perform", udid: "UDID-A", events: [{ kind: "button", name: "swipe_home" }] });
		}
		await bridge.send({ op: "perform", udid: "UDID-B", events: [{ kind: "button", name: "swipe_home" }] });

		expect(bridge.calls().filter((line) => line.startsWith("new SimHID"))).toEqual([
			"new SimHID UDID-A",
			"new SimHID UDID-B",
		]);
	});

	// The parent going away is the one path a request-scoped guard cannot cover,
	// and a resident bridge makes it the likeliest way a finger is left down.
	it("lifts a held finger when the caller goes away", async () => {
		const bridge = startBridge();
		await bridge.send({
			op: "hold",
			udid: "UDID-A",
			events: [{ kind: "touch", type: "begin", x: 0.5, y: 0.9 }],
		});
		expect(touches(bridge.calls())).toEqual(["touch begin 0.5,0.9"]);

		await bridge.close();
		running = null;

		expect(touches(bridge.calls()).at(-1)).toBe("touch end 0.5,0.9");
	});

	// A batch it cannot read must not read as a gesture that happened.
	it("reports an unparsable request rather than answering nothing", async () => {
		const bridge = startBridge();

		const answer = await new Promise<Record<string, unknown>>((resolve) => {
			const child = bridge.child;
			const answers = child.stdio[3] as NodeJS.ReadableStream;
			answers.once("data", (chunk: Buffer) => resolve(JSON.parse(chunk.toString().trim())));
			child.stdin.write("{ not json\n");
		});

		expect(answer.ok).toBe(false);
		expect((answer.error as { code: string }).code).toBe("bad_request");
	});
});
