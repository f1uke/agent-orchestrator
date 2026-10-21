#!/usr/bin/env node
// Arm D's Stop hook: refuses the check-in (blocks the turn from ending) until
// the session has read a playbook file and written a todolist, at most
// STOP_MAX_REFUSALS times. $PA_STOP_STATE gets one line per refusal: the
// number of tool calls the session had made when it was refused.

import { readFileSync, writeFileSync } from "node:fs";
import { STOP_MAX_REFUSALS, STOP_REFUSAL } from "./arms.mjs";
import { anySetup, readEvents, toolCalls } from "./transcript.mjs";

const input = JSON.parse(readFileSync(0, "utf8"));
const state = process.env.PA_STOP_STATE;
let refusals = [];
try {
	refusals = readFileSync(state, "utf8").split("\n").filter(Boolean);
} catch {}

const calls = toolCalls(readEvents(input.transcript_path));
if (!anySetup(calls) && refusals.length < STOP_MAX_REFUSALS) {
	writeFileSync(state, [...refusals, String(calls.length)].join("\n") + "\n");
	process.stdout.write(JSON.stringify({ decision: "block", reason: STOP_REFUSAL }));
}
