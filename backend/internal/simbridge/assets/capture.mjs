// Streams one iOS Simulator's screen for a human to watch.
//
// It is the long-lived sibling of bridge.mjs and follows the same rules: it
// transports, it does not decide. Every policy that needs testing - who is
// allowed to look, who gets which frame, when a picture group has to restart -
// lives in Go.
//
// Four things here are load-bearing:
//
//  1. Frames go out on descriptor 3, never stdout. The native addon writes to
//     stdout itself ("[capture] Framebuffer: 1320x2868 ..."), and one stray line
//     on a shared channel would desynchronize a binary stream permanently rather
//     than merely garbling one message.
//  2. stdin is the heartbeat AND the control channel. When the parent dies its
//     end of the pipe closes, this process reads EOF and shuts the capture down.
//     A capture that outlived its parent would keep a video encoder running with
//     nobody watching, which is the whole failure this design exists to prevent.
//  3. The codec is H.264 (kind 1), not JPEG (kind 0). Measured on the same
//     device under the same activity: same frame rate, a twenty-fifth of the
//     bytes, less than half the CPU. An earlier reading that H.264 "reports
//     encodingFailed" came from the single frame the encoder drops while
//     VideoToolbox builds its session; every frame after it encodes - unless the
//     device's framebuffer is one VideoToolbox will not take at all, which is
//     why the codec can be switched from outside (see `op: "codec"`).
//  4. Frames are dropped, never queued, but only whole ones. A delta means
//     nothing without the frames before it, so the reader is told which kind
//     each chunk is and Go decides who may receive it.

import { createRequire } from "node:module";
import { createWriteStream } from "node:fs";

const require = createRequire(import.meta.url);

// 0 is MJPEG, 1 is H.264 in AVCC framing. The addon takes one per subscription;
// switching is re-subscribing, which is the same thing a keyframe request does.
const CODEC_MJPEG = 0;
const CODEC_AVCC = 1;

// The addon's own envelope: a 4-byte big-endian length covering the tag byte,
// then the tag, then the payload. The tag is redundant with the flags argument,
// so the envelope is stripped here and the kind travels in our own header.
const ENVELOPE_PREFIX = 5;

// Flags the addon sets on a chunk. Anything else is a delta.
const FLAG_DESCRIPTION = 1 << 0;
const FLAG_KEYFRAME = 1 << 1;

// What Go reads in the header's kind byte.
const FRAME_DESCRIPTION = 1;
const FRAME_KEYFRAME = 2;
const FRAME_DELTA = 3;
// A whole JPEG. It stands on its own - no description, nothing before it - which
// is the entire reason it is worth carrying a second codec.
const FRAME_IMAGE = 4;

// Above this many bytes buffered for the reader, frames are dropped rather than
// queued. H.264 deltas are a few KB and a keyframe a few hundred, so this is
// far more slack than a healthy reader ever needs.
const MAX_PENDING_BYTES = 2 * 1024 * 1024;

const FRAME_HEADER_SIZE = 10;

const frames = createWriteStream(null, { fd: 3 });
let pending = 0;
let dropped = 0;

function fail(message) {
  process.stderr.write(`${message}\n`);
  process.exit(1);
}

// Reads newline-delimited JSON from stdin: the first line is the capture
// request, every line after it is a command.
function readLines(onLine) {
  let raw = "";
  process.stdin.setEncoding("utf8");
  process.stdin.on("data", (chunk) => {
    raw += chunk;
    for (;;) {
      const newline = raw.indexOf("\n");
      if (newline === -1) return;
      const line = raw.slice(0, newline).trim();
      raw = raw.slice(newline + 1);
      if (line) onLine(line);
    }
  });
}

function writeFrame(payload, width, height, kind) {
  if (pending > MAX_PENDING_BYTES) {
    dropped++;
    return;
  }
  const head = Buffer.allocUnsafe(FRAME_HEADER_SIZE);
  head.writeUInt32BE(payload.byteLength, 0);
  head.writeUInt16BE(width & 0xffff, 4);
  head.writeUInt16BE(height & 0xffff, 6);
  head.writeUInt8(kind, 8);
  head.writeUInt8(0, 9);
  const bytes = Buffer.from(payload.buffer, payload.byteOffset, payload.byteLength);
  pending += bytes.length;
  frames.write(head);
  frames.write(bytes, () => {
    pending -= bytes.length;
  });
}

function kindOf(flags) {
  if (flags & FLAG_DESCRIPTION) return FRAME_DESCRIPTION;
  if (flags & FLAG_KEYFRAME) return FRAME_KEYFRAME;
  return FRAME_DELTA;
}

// MJPEG frames carry no envelope and no flags - the addon hands over the JPEG
// itself - so the two codecs differ in both the bytes to take and the kind to
// stamp on them, and nothing else.
function frameOf(codec, payload, flags) {
  if (codec === CODEC_MJPEG) return { bytes: payload, kind: FRAME_IMAGE };
  return { bytes: payload.subarray(ENVELOPE_PREFIX), kind: kindOf(flags) };
}

async function main() {
  const request = await new Promise((resolve, reject) => {
    let first = true;
    readLines((line) => {
      if (!first) return void handleCommand(line);
      first = false;
      try {
        resolve(JSON.parse(line));
      } catch (err) {
        reject(err);
      }
    });
    process.stdin.on("end", () => reject(new Error("no capture request on stdin")));
  });
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
  let stopping = false;

  // subscribe() hands back the function that undoes it. Each subscription gets
  // its own encoder, so re-subscribing is how a fresh picture group - avcC
  // description followed by a keyframe - is produced for a viewer that joined
  // after the stream started.
  //
  // The kind is a required Int and a callback-only call rejects asynchronously
  // with "Could not convert parameter 0 to type Int" - which subscribes nothing
  // and delivers no frames, silently. Passing it explicitly is not optional.
  let unsubscribe = null;
  let restarting = false;
  let codec = CODEC_AVCC;

  const subscribe = async () => {
    const active = codec;
    unsubscribe = await capture.subscribe(active, (payload, width, height, flags) => {
      const frame = frameOf(active, payload, flags);
      writeFrame(frame.bytes, width, height, frame.kind);
    });
  };

  // Restarting the subscription is cheap but not free, and two viewers joining
  // together only need one fresh group between them, so a request that arrives
  // mid-restart is folded into the one already running - and then run once more
  // after it. Folding alone was enough while every restart wanted the same
  // thing; a codec change does not, and dropping it would leave the stream on a
  // codec this device has already shown it cannot encode.
  let again = false;
  const restart = async () => {
    if (stopping) return;
    if (restarting) {
      again = true;
      return;
    }
    restarting = true;
    try {
      do {
        again = false;
        const previous = unsubscribe;
        unsubscribe = null;
        if (previous) await previous();
        if (!stopping) await subscribe();
      } while (again && !stopping);
    } catch (err) {
      process.stderr.write(`capture restart failed: ${err?.message ?? err}\n`);
    } finally {
      restarting = false;
    }
  };

  function handleCommand(line) {
    let command;
    try {
      command = JSON.parse(line);
    } catch {
      return;
    }
    if (command?.op === "keyframe") return void restart();
    // Which codec a device can actually be encoded in is not something this
    // process is allowed to decide - it transports. Go watches whether any
    // frame arrived and says so.
    if (command?.op === "codec") {
      const next = command.codec === "mjpeg" ? CODEC_MJPEG : CODEC_AVCC;
      if (next === codec) return;
      codec = next;
      void restart();
    }
  }

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

  await subscribe();
  await capture.start();
  process.stdin.resume();
}

await main().catch((err) => fail(String(err?.message ?? err)));
