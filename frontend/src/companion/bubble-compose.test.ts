import { describe, expect, it } from "vitest";
import { composeBubble } from "./bubble-compose";
import type { ActivitySlots } from "./activity-decay";

const T = Date.parse("2026-07-22T09:00:00.000Z");

function slots(detail?: Partial<ActivitySlots["detail"]>, coarse?: ActivitySlots["coarse"]): ActivitySlots {
	return {
		detail: detail ? { kind: "tool_start", atMs: T, ttlMs: 20_000, ...detail } : undefined,
		coarse,
	};
}

describe("composing the bubble's words", () => {
	// The feed emits DATA — a tool name, a base name, a model-authored sentence — and
	// never an English UI string. The words are the overlay's job, which is also what
	// keeps the daemon free of presentation.

	it("shows a model-authored sentence as-is, because it already reads like speech", () => {
		const said = composeBubble(slots({ tool: "Bash", text: "Running the test suite" }), T + 1_000);

		expect(said?.text).toBe("Running the test suite");
	});

	it("builds a sentence from a curated file name", () => {
		expect(composeBubble(slots({ tool: "Read", target: "hooks.go" }), T + 1)?.text).toBe("Reading hooks.go");
		expect(composeBubble(slots({ tool: "Edit", target: "FileTree.tsx" }), T + 1)?.text).toBe("Editing FileTree.tsx");
		expect(composeBubble(slots({ tool: "Write", target: "notes.md" }), T + 1)?.text).toBe("Writing notes.md");
		expect(composeBubble(slots({ tool: "Grep", target: "PostToolUse" }), T + 1)?.text).toBe(
			"Searching for PostToolUse",
		);
		expect(composeBubble(slots({ tool: "WebFetch", target: "docs.example.test" }), T + 1)?.text).toBe(
			"Fetching docs.example.test",
		);
	});

	it("says a tool ran without inventing a target it was not given", () => {
		expect(composeBubble(slots({ tool: "TodoWrite" }), T + 1)?.text).toBe("Updating its to-do list");
	});

	it("REFUSES to guess when a tool frame arrives with no tool", () => {
		// "Something happened, we don't know what." An invented action here would be
		// the feed's one unforgivable failure, so the fallback says only what is true.
		const said = composeBubble(slots({ tool: undefined, target: undefined, text: undefined }), T + 1);

		expect(said?.text).toBe("Working…");
	});

	it("names an unknown future tool without pretending to know what it does", () => {
		expect(composeBubble(slots({ tool: "SomeNewTool", target: "thing" }), T + 1)?.text).toBe("Running SomeNewTool");
	});

	it("marks a failed tool as a failure rather than as progress", () => {
		const said = composeBubble(slots({ kind: "tool_failed", tool: "Bash", text: "Running the test suite" }), T + 1);

		expect(said?.text).toBe("Running the test suite — failed");
	});

	it("truncates anything long rather than letting it stretch across the desktop", () => {
		const long = "x".repeat(400);
		const said = composeBubble(slots({ kind: "message", text: long }), T + 1);

		// The bubble clamps to three lines, so this is the hard bound on what is laid
		// out at all — comfortably past three lines' worth, so what a reader sees end
		// the sentence is the CLAMP, not a cut in the middle of line one.
		expect(said!.text.length).toBeLessThanOrEqual(160);
		expect(said!.text.endsWith("…")).toBe(true);
	});

	it("keeps a real three-line sentence whole", () => {
		const sentence = "Rewriting the coupon search ranking so expired offers stop being promoted to the top";
		const said = composeBubble(slots({ text: sentence }), T + 1);

		expect(said?.text).toBe(sentence);
	});

	it("drops to the coarse truth when the detail has expired", () => {
		const said = composeBubble(
			slots({ text: "Running the test suite" }, { coarse: "working", atMs: T, ttlMs: 600_000 }),
			T + 30_000,
		);

		expect(said?.text).toBe("Working…");
	});

	it("says nothing at all once even the coarse truth has gone", () => {
		// Not an empty bubble, not a placeholder — no bubble. A Proc with nothing to
		// say is just a Proc.
		expect(composeBubble(slots(undefined, { coarse: "idle", atMs: T, ttlMs: 45_000 }), T + 46_000)).toBeNull();
	});

	it("says nothing for a session that has never emitted anything", () => {
		// Every hook-less harness — codex and about nine others — lives here. The
		// silence must look normal, never like a broken or unsupported pet.
		expect(composeBubble({}, T)).toBeNull();
	});

	it("marks a genuine wait as an alert, and nothing else", () => {
		const waiting = composeBubble(slots(undefined, { coarse: "waiting", atMs: T, ttlMs: 0 }), T + 10_000);
		const working = composeBubble(slots(undefined, { coarse: "working", atMs: T, ttlMs: 600_000 }), T + 1_000);

		expect(waiting?.tone).toBe("alert");
		expect(waiting?.text).toBe("Waiting for you");
		expect(working?.tone).not.toBe("alert");
	});

	it("dims a claim that is running on its coarse fallback", () => {
		// A weaker claim should look weaker.
		const fresh = composeBubble(slots({ text: "Running the test suite" }), T + 1_000);
		const stale = composeBubble(
			slots({ text: "Running the test suite" }, { coarse: "working", atMs: T, ttlMs: 600_000 }),
			T + 30_000,
		);

		expect(fresh?.decay).toBe("fresh");
		expect(stale?.decay).toBe("settled");
	});
});

describe("a message names who actually sent it", () => {
	// `→ orchestrator:` was a guess made when the sender was unknown, and it was
	// printing the raw `[from @…]` stamp alongside it — so a real message read
	// "→ orchestrator: [from @agent-orchestrator-105] P1 is fixed". The stamp IS
	// the sender; using it is both tidier and more honest.
	it("says the sender's handle, not a guess at one", () => {
		const said = composeBubble(slots({ kind: "message", text: "[from @agent-orchestrator-105] P1 is fixed" }), T + 1);

		expect(said?.text).toBe("@agent-orchestrator-105: P1 is fixed");
	});

	it("never leaves the raw stamp in the bubble", () => {
		const said = composeBubble(slots({ kind: "message", text: "[from @demo-app-1] ping" }), T + 1);

		expect(said?.text).not.toContain("[from");
	});

	it("shows an unstamped message as speech without inventing a sender", () => {
		const said = composeBubble(slots({ kind: "message", text: "please rebase onto main" }), T + 1);

		expect(said?.text).toBe("“please rebase onto main”");
	});
});
