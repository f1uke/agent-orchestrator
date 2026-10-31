import type { ChatEntry, Direction } from "./chat-event";
import { READ_ME, SHOW_WIDGET } from "./chat-widget";

export const MOCK_WIDGETS_CHAT_SESSION = "demo-chat-widgets";

const SESSION = "5b0c3e5e-7c4d-4f5b-9a77-0a1d6f4b2c11";
const START = Date.parse("2026-10-31T09:00:00.000Z");

const CALCULATOR = `<style>
.dca{display:grid;gap:16px;font-size:14px}
.dca label{display:block;color:var(--text-secondary)}.dca label input{display:block;margin-top:8px}
.dca label b{color:var(--text-primary);font-weight:500}
.dca input[type=range]{width:100%;accent-color:var(--accent)}
.dca .stats{display:grid;grid-template-columns:repeat(3,1fr);gap:12px}
.dca .stat{padding:12px;border-radius:var(--radius);background:var(--surface-2)}
.dca .stat span{display:block;color:var(--text-muted);font-size:12px}
.dca .stat strong{display:block;margin-top:2px;font-family:var(--font-mono);font-size:18px;font-weight:500}
.dca .gain{color:var(--success)}
.dca svg{width:100%;height:auto}
.dca button{justify-self:start;padding:6px 12px;border:1px solid var(--border-strong);border-radius:var(--radius);background:transparent;color:var(--text-primary);font:inherit;cursor:pointer}
.dca button:hover{background:var(--surface-2)}
</style>
<div class="dca">
  <label>Monthly amount: <b id="amount-out">5,000</b> THB
    <input id="amount" type="range" min="1000" max="50000" step="500" value="5000">
  </label>
  <label>Years: <b id="years-out">10</b>
    <input id="years" type="range" min="1" max="30" step="1" value="10">
  </label>
  <div class="stats">
    <div class="stat"><span>Invested</span><strong id="invested">-</strong></div>
    <div class="stat"><span>Value at 7% a year</span><strong id="value">-</strong></div>
    <div class="stat"><span>Gain</span><strong id="gain" class="gain">-</strong></div>
  </div>
  <svg id="chart" viewBox="0 0 600 180" role="img" aria-label="Portfolio value by year"></svg>
  <button id="ask" type="button">Ask how this works ↗</button>
</div>
<script>
var RATE = 0.07 / 12;
function fmt(n) { return Math.round(n).toLocaleString("en-US"); }
function series(amount, years) {
  var points = [], value = 0, put = 0;
  for (var m = 0; m <= years * 12; m++) {
    if (m > 0) { value = value * (1 + RATE) + amount; put += amount; }
    if (m % 12 === 0) points.push({ year: m / 12, value: value, put: put });
  }
  return points;
}
function line(points, key, top) {
  return points.map(function (p, i) {
    var x = 30 + (points.length === 1 ? 0 : (i / (points.length - 1)) * 550);
    var y = 160 - (p[key] / top) * 140;
    return (i ? "L" : "M") + x.toFixed(1) + " " + y.toFixed(1);
  }).join(" ");
}
function draw() {
  var amount = +document.getElementById("amount").value;
  var years = +document.getElementById("years").value;
  var pts = series(amount, years);
  var end = pts[pts.length - 1];
  document.getElementById("amount-out").textContent = fmt(amount);
  document.getElementById("years-out").textContent = years;
  document.getElementById("invested").textContent = fmt(end.put);
  document.getElementById("value").textContent = fmt(end.value);
  document.getElementById("gain").textContent = "+" + fmt(end.value - end.put);
  document.getElementById("chart").innerHTML =
    '<line x1="30" y1="160" x2="580" y2="160" stroke="var(--border-strong)"/>' +
    '<path d="' + line(pts, "put", end.value) + '" fill="none" stroke="var(--text-muted)" stroke-width="2"/>' +
    '<path d="' + line(pts, "value", end.value) + '" fill="none" stroke="var(--accent)" stroke-width="2"/>' +
    '<text x="30" y="14" fill="var(--text-muted)" font-size="11">Value</text>' +
    '<text x="580" y="176" fill="var(--text-muted)" font-size="11" text-anchor="end">Year ' + years + "</text>";
}
document.getElementById("amount").addEventListener("input", draw);
document.getElementById("years").addEventListener("input", draw);
document.getElementById("ask").addEventListener("click", function () {
  sendPrompt("Explain how dollar-cost averaging smooths out a falling market");
});
draw();
</script>`;

const FLOW = `<svg viewBox="0 0 640 140" role="img" aria-label="How a monthly buy turns into units">
  <g font-family="var(--font-sans)" font-size="13">
    <rect x="10" y="40" width="140" height="56" rx="8" fill="var(--surface-2)" stroke="var(--border)"/>
    <text x="80" y="73" text-anchor="middle" fill="var(--text-primary)">Monthly amount</text>
    <rect x="250" y="40" width="140" height="56" rx="8" fill="var(--surface-2)" stroke="var(--border)"/>
    <text x="320" y="73" text-anchor="middle" fill="var(--text-primary)">Fund NAV that day</text>
    <rect x="490" y="40" width="140" height="56" rx="8" fill="var(--surface-2)" stroke="var(--accent)"/>
    <text x="560" y="73" text-anchor="middle" fill="var(--accent)">Units bought</text>
    <path d="M150 68 H250 M390 68 H490" stroke="var(--text-muted)" stroke-width="1.5" fill="none"/>
    <text x="200" y="60" text-anchor="middle" fill="var(--text-muted)" font-size="11">divided by</text>
    <text x="440" y="60" text-anchor="middle" fill="var(--text-muted)" font-size="11">equals</text>
  </g>
</svg>`;

type Step = [Direction, object];

const tool = (id: string, messageId: string, name: string, input: object): Step => [
	"out",
	{
		type: "assistant",
		session_id: SESSION,
		message: { id: messageId, content: [{ type: "tool_use", id, name, input }] },
	},
];
const result = (id: string, content: string): Step => [
	"out",
	{
		type: "user",
		session_id: SESSION,
		message: { role: "user", content: [{ type: "tool_result", tool_use_id: id, content, is_error: false }] },
	},
];
const say = (messageId: string, text: string): Step => [
	"out",
	{ type: "assistant", session_id: SESSION, message: { id: messageId, content: [{ type: "text", text }] } },
];

const SHOWN = "Shown to the user. Do not repeat its content in text.";

export function widgetEntries(): ChatEntry[] {
	const steps: Step[] = [
		["host", { kind: "spawn", argv: ["claude"], resume: "", pid: 1 }],
		[
			"in",
			{
				type: "user",
				uuid: "w-1",
				session_id: "",
				message: { role: "user", content: "Show me what a monthly DCA is worth over ten years." },
			},
		],
		["out", { type: "command_lifecycle", command_uuid: "w-1", state: "started" }],
		say("msg_w1", "I'll read the visual guidelines, then draw it so you can move the numbers yourself."),
		tool("w-read", "msg_w2", READ_ME, { modules: ["interactive"] }),
		result("w-read", "Visual guidelines: use the theme variables, keep the background transparent."),
		tool("w-calc", "msg_w3", SHOW_WIDGET, {
			title: "dca_calculator",
			loading_messages: ["Building the calculator…"],
			widget_code: CALCULATOR,
		}),
		result("w-calc", SHOWN),
		tool("w-flow", "msg_w4", SHOW_WIDGET, {
			title: "dca_unit_flow",
			loading_messages: ["Drawing the diagram…"],
			widget_code: FLOW,
		}),
		result("w-flow", SHOWN),
		say("msg_w5", "At 5,000 THB a month and 7% a year, ten years turns about 600,000 invested into roughly 865,000."),
		["out", { type: "result", subtype: "success", is_error: false, num_turns: 3, total_cost_usd: 0.05 }],
		["out", { type: "command_lifecycle", command_uuid: "w-1", state: "completed" }],
	];
	return steps.map(([dir, payload], i) => ({
		seq: i + 1,
		ts: new Date(START + i * 1_500).toISOString(),
		dir,
		payload,
	}));
}
