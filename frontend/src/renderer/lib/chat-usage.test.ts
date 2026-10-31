import { describe, expect, it } from "vitest";
import type { ChatEntry } from "./chat-event";
import { foldEntries } from "./chat-fold";
import { newestUsage } from "./chat-usage";

const event = (seq: number, ts: string, fiveHour: number, sevenDay?: number): ChatEntry => ({
	seq,
	ts,
	dir: "out",
	payload: {
		type: "rate_limit_event",
		rate_limit_info: {
			status: "allowed",
			unifiedWindows: {
				five_hour: { utilization: fiveHour, resetsAt: 1793428800, status: "allowed" },
				...(sevenDay === undefined
					? {}
					: { seven_day: { utilization: sevenDay, resetsAt: 1793692800, status: "allowed_warning" } }),
			},
		},
	},
});

describe("subscription usage from the session's own log", () => {
	it("folds the newest rate_limit_event with the time it was logged", () => {
		const tr = foldEntries([
			event(1, "2026-10-31T04:00:00.000Z", 0.1, 0.5),
			event(2, "2026-10-31T04:36:16.000Z", 0.32, 0.53),
		]);
		expect(tr.usage).toEqual({
			fiveHour: { utilization: 0.32, resetsAt: "2026-10-31T06:40:00.000Z", status: "allowed" },
			sevenDay: { utilization: 0.53, resetsAt: "2026-11-03T08:00:00.000Z", status: "allowed_warning" },
			updatedAt: "2026-10-31T04:36:16.000Z",
		});
	});

	it("keeps a window the newest event did not report out of it", () => {
		const tr = foldEntries([event(1, "2026-10-31T04:00:00.000Z", 0.1)]);
		expect(tr.usage?.sevenDay).toBeUndefined();
		expect(tr.usage?.fiveHour?.utilization).toBe(0.1);
	});

	it("ignores an event it cannot read", () => {
		const bad: ChatEntry = {
			seq: 2,
			ts: "2026-10-31T05:00:00.000Z",
			dir: "out",
			payload: { type: "rate_limit_event", rate_limit_info: { unifiedWindows: { five_hour: { utilization: "1%" } } } },
		};
		expect(foldEntries([event(1, "2026-10-31T04:00:00.000Z", 0.1), bad]).usage?.updatedAt).toBe(
			"2026-10-31T04:00:00.000Z",
		);
	});
});

describe("newestUsage", () => {
	const at = (updatedAt: string, pct: number) => ({
		fiveHour: { utilization: pct, resetsAt: updatedAt, status: "allowed" },
		updatedAt,
	});

	it("prefers the session's own value when it is newer than the daemon's", () => {
		expect(newestUsage(at("2026-10-31T05:00:00Z", 0.2), at("2026-10-31T04:00:00Z", 0.1))?.fiveHour?.utilization).toBe(
			0.2,
		);
		expect(newestUsage(at("2026-10-31T03:00:00Z", 0.2), at("2026-10-31T04:00:00Z", 0.1))?.fiveHour?.utilization).toBe(
			0.1,
		);
	});

	it("uses whichever one exists", () => {
		expect(newestUsage(undefined, at("2026-10-31T04:00:00Z", 0.1))?.fiveHour?.utilization).toBe(0.1);
		expect(newestUsage(at("2026-10-31T04:00:00Z", 0.2), undefined)?.fiveHour?.utilization).toBe(0.2);
		expect(newestUsage(undefined, undefined)).toBeUndefined();
	});
});
