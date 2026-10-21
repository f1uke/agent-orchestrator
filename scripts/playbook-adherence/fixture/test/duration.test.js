import { test } from "node:test";
import assert from "node:assert/strict";
import { parseDuration, toHours } from "../src/duration.js";

test("hours", () => assert.equal(parseDuration("2h"), 7200));
test("seconds", () => assert.equal(parseDuration("90s"), 90));
test("toHours rounds to cents of an hour", () => assert.equal(toHours(5400), 1.5));
