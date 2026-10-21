import { test } from "node:test";
import assert from "node:assert/strict";
import { buildReport, dedupe } from "../src/report.js";

const e = (id, client, seconds) => ({ id, client, seconds });

test("dedupe drops repeated ids", () =>
	assert.equal(dedupe([e("1", "a", 1), e("1", "a", 1), e("2", "a", 1)]).length, 2));
test("report sums per client, biggest first", () => {
	const rows = buildReport([e("1", "a", 3600), e("2", "b", 7200), e("3", "a", 1800)]);
	assert.deepEqual(
		rows.map((r) => r.client),
		["b", "a"],
	);
	assert.equal(rows[1].hours, 1.5);
});
