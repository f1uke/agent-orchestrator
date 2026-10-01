// A stand-in for llama-server, for the supervisor and service tests: it listens
// on the `--host <path>.sock` it is given, answers GET /health (503 while
// "loading", then 200) and POST /infill, and can be told to misbehave.
//
// Env:
//   FAKE_LOAD_MS     how long /health answers 503 (default 50)
//   FAKE_EXIT_AFTER  exit(3) this many ms after start (simulates a crash)
//   FAKE_INFILL_MS   how long an /infill takes (default 0)
//   FAKE_IGNORE_TERM ignore SIGTERM (so the supervisor must SIGKILL)
import http from "node:http";

const args = process.argv.slice(2);
const host = args[args.indexOf("--host") + 1];
const loadMs = Number(process.env.FAKE_LOAD_MS ?? 50);
const infillMs = Number(process.env.FAKE_INFILL_MS ?? 0);
const started = Date.now();

if (process.env.FAKE_EXIT_AFTER) {
	setTimeout(() => {
		console.error("fake: error: simulated crash");
		process.exit(3);
	}, Number(process.env.FAKE_EXIT_AFTER));
}
if (process.env.FAKE_IGNORE_TERM) process.on("SIGTERM", () => {});

const server = http.createServer((req, res) => {
	if (req.url === "/health") {
		const ready = Date.now() - started >= loadMs;
		res.writeHead(ready ? 200 : 503, { "content-type": "application/json" });
		res.end(ready ? '{"status":"ok"}' : '{"error":{"code":503,"message":"Loading model"}}');
		return;
	}
	if (req.url === "/infill" && req.method === "POST") {
		let body = "";
		req.on("data", (d) => (body += d));
		req.on("end", () => {
			const request = JSON.parse(body);
			setTimeout(() => {
				res.writeHead(200, { "content-type": "application/json" });
				res.end(
					JSON.stringify({
						content: request.n_predict === 0 ? "" : `<${request.prompt}>`,
						timings: { prompt_n: 3, prompt_ms: 1, predicted_n: 2, predicted_ms: 1 },
					}),
				);
			}, infillMs);
		});
		return;
	}
	res.writeHead(404);
	res.end();
});
server.listen(host, () => console.log(`fake llama-server listening on ${host}`));
