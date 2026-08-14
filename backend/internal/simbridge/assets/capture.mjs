// Streams one iOS Simulator's screen for a human to watch.
//
// It is the long-lived sibling of bridge.mjs and follows the same rules: it
// transports, it does not decide. Every policy that needs testing - which frames
// are worth forwarding, how fast, who is allowed to look - lives in Go.
//
// Three things here are load-bearing:
//
//  1. Frames go out on descriptor 3, never stdout. The native addon writes to
//     stdout itself ("[capture] Framebuffer: 1320x2868 ..."), and one stray line
//     on a shared channel would desynchronize a binary stream permanently rather
//     than merely garbling one message.
//  2. stdin is the heartbeat. When the parent dies its end of the pipe closes,
//     this process reads EOF and shuts the capture down. A capture that outlived
//     its parent would keep a video encoder running with nobody watching, which
//     is the whole failure this design exists to prevent.
//  3. Frames are dropped, never queued. If the reader falls behind, the newest
//     frame matters and the backlog does not; queueing would turn a slow viewer
//     into unbounded memory.

import { createRequire } from "node:module";
import { createWriteStream } from "node:fs";

const require = createRequire(import.meta.url);

// MJPEG. The addon's other encoder (1, AVCC/H.264) reports `encodingFailed` on
// the hardware this was built against, so the one that works is the one used.
const KIND_MJPEG = 0;

// Above this many bytes buffered for the reader, frames are dropped rather than
// queued. One full-screen frame is around 430 KB, so this is a couple of frames
// of slack and no more.
const MAX_PENDING_BYTES = 2 * 1024 * 1024;

const FRAME_HEADER_SIZE = 8;

const frames = createWriteStream(null, { fd: 3 });
let pending = 0;
let dropped = 0;

function fail(message) {
  process.stderr.write(`${message}\n`);
  process.exit(1);
}

function readRequest() {
  return new Promise((resolve, reject) => {
    let raw = "";
    const onData = (chunk) => {
      raw += chunk;
      const newline = raw.indexOf("\n");
      if (newline === -1) return;
      process.stdin.off("data", onData);
      try {
        resolve(JSON.parse(raw.slice(0, newline)));
      } catch (err) {
        reject(err);
      }
    };
    process.stdin.setEncoding("utf8");
    process.stdin.on("data", onData);
    process.stdin.on("end", () => reject(new Error("no capture request on stdin")));
  });
}

function writeFrame(payload, width, height) {
  if (pending > MAX_PENDING_BYTES) {
    dropped++;
    return;
  }
  const head = Buffer.allocUnsafe(FRAME_HEADER_SIZE);
  head.writeUInt32BE(payload.length, 0);
  head.writeUInt16BE(width & 0xffff, 4);
  head.writeUInt16BE(height & 0xffff, 6);
  pending += payload.length;
  frames.write(head);
  frames.write(Buffer.from(payload.buffer, payload.byteOffset, payload.byteLength), () => {
    pending -= payload.length;
  });
}

async function main() {
  const request = await readRequest();
  const { addonPath, udid } = request;
  if (!addonPath || !udid) fail("addonPath and udid are required");

  let addon;
  try {
    addon = require(addonPath);
  } catch (err) {
    // The addon dlopens private Apple frameworks; an Xcode or macOS upgrade
    // that moves them surfaces exactly here, and has to be told apart from a
    // missing file.
    fail(`addon_load_failed: ${err?.message ?? err}`);
  }

  const capture = new addon.SimCapture(udid);
  // The kind is a required Int and a callback-only call rejects asynchronously
  // with "Could not convert parameter 0 to type Int" - which subscribes nothing
  // and delivers no frames, silently. Passing it explicitly is not optional.
  capture.subscribe(KIND_MJPEG, (payload, width, height) => {
    writeFrame(payload, width, height);
  });

  let stopping = false;
  const shutdown = async (why) => {
    if (stopping) return;
    stopping = true;
    try {
      await capture.stop();
    } catch {
      // A stop that fails still ends with this process exiting, which releases
      // everything the addon holds.
    }
    if (dropped > 0) process.stderr.write(`dropped ${dropped} frames the reader could not keep up with (${why})\n`);
    process.exit(0);
  };

  // The parent going away is the ordinary end of a capture, not an error.
  process.stdin.on("end", () => void shutdown("stdin closed"));
  process.stdin.on("close", () => void shutdown("stdin closed"));
  for (const signal of ["SIGINT", "SIGTERM", "SIGHUP"]) {
    process.once(signal, () => void shutdown(signal));
  }
  // A frame reader that goes away mid-write must not become an unhandled error.
  frames.on("error", () => void shutdown("frame reader gone"));

  await capture.start();
  process.stdin.resume();
}

await main().catch((err) => fail(String(err?.message ?? err)));
