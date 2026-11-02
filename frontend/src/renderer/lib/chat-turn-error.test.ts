import { describe, expect, it } from "vitest";
import { describeTurnError } from "./chat-turn-error";

const GATEWAY_RATE_LIMIT =
	'API Error: 503 [claude/claude-opus-5-5] [429]: {"type":"error","error":{"type":"rate_limit_error","message":"Synthetic rate limit message."},"request_id":"req_x"} (reset after 1m 45s). This is a server-side issue, usually temporary - try again in a moment.';

describe("describeTurnError", () => {
	it("names a gateway-wrapped rate limit, its reset, and the nested message", () => {
		expect(describeTurnError("success", GATEWAY_RATE_LIMIT)).toEqual({
			label: "Rate limited - resets in 1m 45s",
			detail: "Synthetic rate limit message.",
			raw: GATEWAY_RATE_LIMIT,
		});
	});

	it("names a gateway rate limit with no reset without inventing one", () => {
		const text =
			'API Error: 503 [claude/claude-sonnet-5-5] [429]: {"type":"error","error":{"type":"rate_limit_error","message":"Slow down."}}';
		expect(describeTurnError("success", text)).toEqual({ label: "Rate limited", detail: "Slow down.", raw: text });
	});

	it("names a direct spend limit and its reset clock, with no raw fold for readable text", () => {
		const text =
			"You've hit your monthly spend limit · raise it at https://example.test · your limit resets 10:30pm (UTC)";
		expect(describeTurnError("success", text)).toEqual({ label: "Spend limit reached - resets 10:30pm", detail: text });
	});

	it("falls back to the status for an API error it cannot classify", () => {
		const text = 'API Error: 500 {"type":"error","error":{"type":"api_error","message":"Internal server error"}}';
		expect(describeTurnError("success", text)).toEqual({
			label: "API error 500",
			detail: "Internal server error",
			raw: text,
		});
	});

	it("names an overloaded API", () => {
		const text = 'API Error: 529 {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}';
		expect(describeTurnError("success", text).label).toBe("API overloaded");
	});

	it("keeps a plain API error message as the detail", () => {
		expect(describeTurnError("success", "API Error: upstream connection lost")).toEqual({
			label: "API error",
			detail: "upstream connection lost",
		});
	});

	it("reads an interrupted turn as Interrupted", () => {
		expect(describeTurnError("error_during_execution", "")).toEqual({ label: "Interrupted" });
	});

	it("reads the CLI's limit subtypes in plain words", () => {
		expect(describeTurnError("error_max_turns", "").label).toBe("Turn limit reached");
		expect(describeTurnError("error_max_budget_usd", "").label).toBe("Budget limit reached");
	});

	it("never reads an error as success", () => {
		expect(describeTurnError("success", "").label).toBe("Turn failed");
		expect(describeTurnError("success", "Something odd happened")).toEqual({
			label: "Turn failed",
			detail: "Something odd happened",
		});
	});

	it("shortens a long multi-line detail to its first line and folds the rest", () => {
		const text = `First line of the failure.\n${"x".repeat(400)}`;
		const got = describeTurnError("success", text);
		expect(got.detail).toBe("First line of the failure.");
		expect(got.raw).toBe(text);
	});
});
