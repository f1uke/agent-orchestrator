// One-shot bridge between `ao sim` and the vendored serve-sim native addon.
//
// It is deliberately the dumbest thing that can work: read ONE JSON request on
// stdin, do it, write ONE JSON response to the reply file named in argv[2],
// exit. There is no server, no port, no WebSocket, no frame capture and no
// daemon - a command's process is its own lifetime, so nothing can be orphaned
// and nothing keeps encoding video after the command that started it is gone.
//
// Every decision that needs testing (which events make a tap, where an element's
// tap point is, how text becomes key usages, what the tree looks like) lives in
// Go. This file only transports.
//
// The one piece of real logic here is the stuck-finger guard. The simulator's
// HID layer has a single finger and no caller identity: a gesture that sends
// `begin` and never sends `end` leaves the guest with a finger held down, which
// wedges input until the device is rebooted. So a touch-down is always followed
// by a lift - on the happy path, on a thrown error, and on a signal.

import { createRequire } from "node:module";
import { writeFileSync } from "node:fs";

const require = createRequire(import.meta.url);

// The reply goes to a file, not to stdout, because the native addon writes to
// stdout itself (`button home` relaunches SpringBoard and prints its pid). One
// stray line from the addon on a shared channel would turn a completed gesture
// into an unparsable answer, so the answer gets a channel the addon cannot
// reach. stdout and stderr stay what they are: diagnostics, quoted back by the
// caller when something fails.
const replyPath = process.argv[2];

const ERR_BAD_REQUEST = "bad_request";
const ERR_ADDON = "addon_load_failed";
const ERR_AX = "ax_unavailable";
const ERR_PERFORM = "perform_failed";

function reply(payload) {
  const body = JSON.stringify(payload);
  if (replyPath) {
    writeFileSync(replyPath, body);
    return;
  }
  process.stdout.write(body + "\n");
}

function fail(code, message) {
  reply({ ok: false, error: { code, message: String(message) } });
}

async function readRequest() {
  const chunks = [];
  for await (const chunk of process.stdin) chunks.push(chunk);
  const raw = Buffer.concat(chunks).toString("utf8").trim();
  if (!raw) throw new Error("no request on stdin");
  return JSON.parse(raw);
}

function loadAddon(path) {
  try {
    return require(path);
  } catch (err) {
    // The addon dlopens private Apple frameworks. An Xcode or macOS upgrade
    // that moves or renames them surfaces here, and the caller has to be able
    // to tell that apart from "the file is missing".
    const error = new Error(err?.message ?? String(err));
    error.code = ERR_ADDON;
    throw error;
  }
}

// A gesture, guarded. The finger is tracked here rather than by the caller
// because this process is the only thing that can still lift it once something
// has gone wrong.
class Finger {
  constructor(hid) {
    this.hid = hid;
    this.down = false;
    this.x = 0.5;
    this.y = 0.5;
    this.lifted = false;
  }

  async touch(type, x, y) {
    this.x = x;
    this.y = y;
    await this.hid.touch(type, x, y, 0, 0, 0);
    this.down = type !== "end";
  }

  // lift is safe to call any number of times, including when nothing is down.
  async lift(reason) {
    if (!this.down) return;
    this.down = false;
    this.lifted = true;
    this.liftReason = reason;
    await this.hid.touch("end", this.x, this.y, 0, 0, 0);
  }
}

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function perform(addon, udid, events) {
  const hid = new addon.SimHID(udid);
  const finger = new Finger(hid);
  // A signal between begin and end is the one path a `finally` alone cannot
  // cover, and it is exactly how a killed command wedges a device.
  const onSignal = (signal) => {
    finger.lift(`signal ${signal}`).finally(() => process.exit(1));
  };
  process.once("SIGINT", onSignal);
  process.once("SIGTERM", onSignal);
  process.once("SIGHUP", onSignal);

  try {
    for (const event of events) {
      switch (event.kind) {
        case "touch":
          await finger.touch(event.type, event.x, event.y);
          break;
        case "key":
          await hid.key(event.type, event.usage);
          break;
        case "button":
          await hid.button(event.name);
          break;
        case "sleep":
          await sleep(event.ms);
          break;
        default:
          throw new Error(`unknown event kind: ${event.kind}`);
      }
    }
  } finally {
    await finger.lift("gesture ended without a lift");
  }
  return { lifted: finger.lifted, liftReason: finger.liftReason };
}

async function main() {
  let request;
  try {
    request = await readRequest();
  } catch (err) {
    fail(ERR_BAD_REQUEST, err);
    return;
  }
  const { addonPath, op, udid } = request;
  if (!addonPath || !op || !udid) {
    fail(ERR_BAD_REQUEST, "addonPath, op and udid are required");
    return;
  }

  let addon;
  try {
    addon = loadAddon(addonPath);
  } catch (err) {
    fail(ERR_ADDON, err.message);
    return;
  }

  try {
    switch (op) {
      case "ax": {
        // Two separate reads: the tree, and which app is actually on screen.
        // The second is what turns "no elements" from a mystery into "you are
        // looking at SpringBoard, not your app".
        const [tree, frontmost] = await Promise.all([addon.axDescribe(udid), addon.axFrontmost(udid)]);
        reply({ ok: true, tree: JSON.parse(tree), frontmost: JSON.parse(frontmost) });
        return;
      }
      case "perform": {
        const result = await perform(addon, udid, request.events ?? []);
        reply({ ok: true, ...result });
        return;
      }
      default:
        fail(ERR_BAD_REQUEST, `unknown op: ${op}`);
        return;
    }
  } catch (err) {
    fail(op === "ax" ? ERR_AX : ERR_PERFORM, err?.message ?? err);
  }
}

await main();
