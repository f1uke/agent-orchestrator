/**
 * LSP's base protocol framing: `Content-Length: N\r\n\r\n<N bytes of JSON>`.
 *
 * Buffer-based end to end, never string-based. A decoder that converts each
 * chunk to a string before reassembling corrupts any multi-byte character that
 * lands on a chunk boundary, and the JSON then fails to parse - intermittently,
 * and only on files whose content is not ASCII.
 */
export type JsonRpcMessage = Record<string, unknown>;

const SEPARATOR = "\r\n\r\n";
const CONTENT_LENGTH = /content-length:\s*(\d+)/i;

export function encodeMessage(message: JsonRpcMessage): Buffer {
	const body = Buffer.from(JSON.stringify(message), "utf8");
	// Byte length, not character length: the header describes octets on the wire.
	return Buffer.concat([Buffer.from(`Content-Length: ${body.length}${SEPARATOR}`, "ascii"), body]);
}

export function createFrameDecoder() {
	let buffer = Buffer.alloc(0);
	return {
		push(chunk: Buffer): JsonRpcMessage[] {
			buffer = buffer.length === 0 ? chunk : Buffer.concat([buffer, chunk]);
			const out: JsonRpcMessage[] = [];
			for (;;) {
				const sep = buffer.indexOf(SEPARATOR);
				if (sep < 0) return out;
				const header = buffer.subarray(0, sep).toString("ascii");
				const length = Number(CONTENT_LENGTH.exec(header)?.[1] ?? -1);
				if (length < 0) {
					// No length we can trust: drop the header and resynchronise on the
					// next separator rather than stalling forever on one bad frame.
					buffer = buffer.subarray(sep + SEPARATOR.length);
					continue;
				}
				const start = sep + SEPARATOR.length;
				if (buffer.length < start + length) return out;
				const body = buffer.subarray(start, start + length).toString("utf8");
				buffer = buffer.subarray(start + length);
				try {
					out.push(JSON.parse(body) as JsonRpcMessage);
				} catch {
					// A body we cannot parse is one message lost, not a dead channel.
				}
			}
		},
		get pendingBytes() {
			return buffer.length;
		},
	};
}
