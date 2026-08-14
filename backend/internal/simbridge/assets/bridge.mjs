// The bridge between `ao sim` and the vendored serve-sim native addon.
//
// It reads newline-delimited JSON requests on stdin and answers each one with a
// line of JSON on descriptor 3. There is no server, no port, no WebSocket and
// no daemon: stdin is the heartbeat, so when the process that started this one
// goes away the pipe closes, the read ends, and this exits. Nothing can be
// orphaned.
//
// It stays resident because the expensive part is the first touch, not the
// touches. Measured on the machine this was built for: loading the addon costs
// 80 ms, and the first HID event costs a further 290 ms while the injector
// attaches to the device - every event after that costs under half a
// millisecond. A process per gesture paid that 370 ms every time, which is what
// made clicking in the desktop app feel like a request rather than a touch.
//
// Every decision that needs testing (which events make a tap, where an element's
// tap point is, how text becomes key usages, what the tree looks like) lives in
// Go. This file only transports.
//
// The one piece of real logic here is the stuck-finger guard. The simulator's
// HID layer has a single finger and no caller identity: a gesture that sends
// `begin` and never sends `end` leaves the guest with a finger held down, which
// wedges input until the device is rebooted. So a touch-down is always followed
// by a lift - on the happy path, on a thrown error, on stdin closing, and on a
// signal. A resident process makes that guard matter more, not less: it now has
// to hold across requests as well as within one.

import { createRequire } from "node:module";
import { createWriteStream } from "node:fs";

const require = createRequire(import.meta.url);

// Replies go out on descriptor 3, not on stdout, because the native addon
// writes to stdout itself (`button home` relaunches SpringBoard and prints its
// pid). One stray line from the addon on a shared channel would turn a
// completed gesture into an unparsable answer, so the answer gets a channel the
// addon cannot reach. stdout and stderr stay what they are: diagnostics, quoted
// back by the caller when something fails.
const replies = createWriteStream(null, { fd: 3 });

const ERR_BAD_REQUEST = "bad_request";
const ERR_ADDON = "addon_load_failed";
const ERR_AX = "ax_unavailable";
const ERR_PERFORM = "perform_failed";

function reply(payload) {
  replies.write(JSON.stringify(payload) + "\n");
}

function fail(code, message) {
  reply({ ok: false, error: { code, message: String(message) } });
}

// Requests arrive one per line and are answered in order: the device has one
// finger, so there is nothing to gain from overlapping them and a great deal to
// lose - two gestures interleaved on one finger is the hazard the whole lease
// exists to prevent.
async function* requests() {
  let buffered = "";
  for await (const chunk of process.stdin) {
    buffered += chunk;
    for (;;) {
      const newline = buffered.indexOf("\n");
      if (newline === -1) break;
      const line = buffered.slice(0, newline).trim();
      buffered = buffered.slice(newline + 1);
      if (line) yield line;
    }
  }
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

// One injector per device, kept for as long as this process lives. Building it
// is what costs 290 ms; reusing it is the whole point of staying resident.
const injectors = new Map();
function injectorFor(addon, udid) {
  let entry = injectors.get(udid);
  if (!entry) {
    const hid = new addon.SimHID(udid);
    entry = { hid, finger: new Finger(hid) };
    injectors.set(udid, entry);
  }
  return entry;
}

// The finger has to come up however this process ends, including paths a
// `finally` cannot reach: a signal, or the parent going away.
async function liftEverything(reason) {
  for (const { finger } of injectors.values()) {
    try {
      await finger.lift(reason);
    } catch {
      // A lift that fails still must not stop the other devices being lifted.
    }
  }
}

// keepDown is the drag case: the events are only half a touch, because the rest
// of it has not happened yet. The finger stays where it is and the CALLER owns
// lifting it - the Go side holds the device's gesture hold for the whole drag
// and lifts on a watchdog if the far end goes quiet. The process-level lifts
// (signal, stdin closing, reply channel gone) still cover this process dying.
async function perform(addon, udid, events, keepDown = false) {
  const { hid, finger } = injectorFor(addon, udid);

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
    if (!keepDown) await finger.lift("gesture ended without a lift");
  }
  const result = { lifted: finger.lifted, liftReason: finger.liftReason };
  // The flags describe this gesture, not the process: a resident bridge that
  // reported a lift from three gestures ago would be lying about this one.
  finger.lifted = false;
  finger.liftReason = undefined;
  return result;
}

async function handle(request) {
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
      case "hold": {
        const result = await perform(addon, udid, request.events ?? [], true);
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

async function main() {
  // A signal between begin and end is the one path a `finally` alone cannot
  // cover, and it is exactly how a killed command wedges a device.
  for (const signal of ["SIGINT", "SIGTERM", "SIGHUP"]) {
    process.once(signal, () => {
      void liftEverything(`signal ${signal}`).finally(() => process.exit(1));
    });
  }
  // Losing the reader is the same emergency as a signal: nobody is left to send
  // the lift, so this process sends it before it goes.
  replies.on("error", () => {
    void liftEverything("reply channel gone").finally(() => process.exit(1));
  });

  for await (const line of requests()) {
    let request;
    try {
      request = JSON.parse(line);
    } catch (err) {
      fail(ERR_BAD_REQUEST, err);
      continue;
    }
    await handle(request);
  }

  // stdin reached EOF: the parent is gone and this process is next, but not
  // while a finger is still down on a device nobody is watching.
  await liftEverything("the caller went away");
}

await main();
