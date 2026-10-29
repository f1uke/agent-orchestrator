import { spawn } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import fs from 'node:fs';
import readline from 'node:readline';

const [, , outPath, workDir, resumeId, answerSpec, ...steps] = process.argv;
if (steps.length === 0) {
  console.error(`usage: node rec.mjs <out.jsonl> <workDir> <resumeId|-> <answer,...> <step>...
  answers, one per can_use_tool in order: always | allow | approve | ask | deny:<message>
  steps: <text>                 send a user message and wait for its result
         !mode:<permissionMode> switch the permission mode
         !interrupt:<ms>:<text> send a message, interrupt it after <ms>
         !burst:<a>|<b>         send several messages back to back (queued)
         !aosend:<from>:<text>  send a message with an ao_send origin note`);
  process.exit(2);
}
const answers = answerSpec.split(',').filter(Boolean);
const out = fs.createWriteStream(outPath);
let seq = 0;
const record = (dir, payload) => out.write(JSON.stringify({ seq: ++seq, ts: new Date().toISOString(), dir, payload }) + '\n');

const args = ['-p', '--input-format', 'stream-json', '--output-format', 'stream-json', '--verbose',
  '--replay-user-messages', '--include-partial-messages', '--permission-prompt-tool=stdio',
  '--permission-mode', 'default', '--setting-sources', 'project', '--strict-mcp-config', '--model', 'sonnet'];
if (resumeId !== '-') args.push('--resume', resumeId);
record('host', { kind: 'spawn', argv: ['claude', ...args], resume: resumeId === '-' ? '' : resumeId });

const child = spawn('claude', args, { cwd: workDir, stdio: ['pipe', 'pipe', 'inherit'] });
const send = (obj) => { record('in', obj); child.stdin.write(JSON.stringify(obj) + '\n'); };
const user = (text) => { const uuid = randomUUID(); send({ type: 'user', uuid, message: { role: 'user', content: text }, parent_tool_use_id: null, session_id: '' }); return uuid; };

let outstanding = 0;
const next = () => {
  if (outstanding > 0) { outstanding--; return; }
  const s = steps.shift();
  if (s === undefined) { child.stdin.end(); return; }
  if (s.startsWith('!mode:')) { send({ type: 'control_request', request_id: randomUUID(), request: { subtype: 'set_permission_mode', mode: s.slice(6) } }); return next(); }
  let m = s.match(/^!interrupt:(\d+):(.*)$/);
  if (m) { user(m[2]); setTimeout(() => send({ type: 'control_request', request_id: randomUUID(), request: { subtype: 'interrupt' } }), Number(m[1])); return; }
  if (s.startsWith('!burst:')) { const parts = s.slice(7).split('|'); outstanding = parts.length - 1; parts.forEach(user); return; }
  m = s.match(/^!aosend:([^:]+):(.*)$/);
  if (m) {
    const uuid = randomUUID();
    record('host', { kind: 'user_origin', uuid, origin: { via: 'ao_send', from: m[1] } });
    send({ type: 'user', uuid, message: { role: 'user', content: `[from @${m[1]}] ${m[2]}` }, parent_tool_use_id: null, session_id: '' });
    return;
  }
  user(s);
};

const answer = (ev) => {
  const r = ev.request;
  const a = answers.shift() ?? 'allow';
  let resp;
  if (a === 'always') resp = { behavior: 'allow', updatedInput: r.input, updatedPermissions: r.permission_suggestions ?? [] };
  else if (a === 'allow' || a === 'approve') resp = { behavior: 'allow', updatedInput: r.input };
  else if (a.startsWith('deny:')) resp = { behavior: 'deny', message: a.slice(5) };
  else if (a === 'ask') {
    const picks = {};
    for (const q of r.input.questions) picks[q.question] = q.multiSelect ? q.options.slice(0, 2).map((o) => o.label).join(', ') : q.options[0].label;
    resp = { behavior: 'allow', updatedInput: { ...r.input, answers: picks } };
  } else throw new Error(`bad answer ${a}`);
  setTimeout(() => send({ type: 'control_response', response: { subtype: 'success', request_id: ev.request_id, response: resp } }), 800);
};

send({ type: 'control_request', request_id: randomUUID(), request: { subtype: 'initialize' } });
let initialized = false;
readline.createInterface({ input: child.stdout }).on('line', (line) => {
  let ev;
  try { ev = JSON.parse(line); } catch { record('host', { kind: 'non_json', text: line.slice(0, 200) }); return; }
  record('out', ev);
  if (ev.type === 'control_response' && !initialized) { initialized = true; next(); }
  else if (ev.type === 'control_request' && ev.request?.subtype === 'can_use_tool') answer(ev);
  else if (ev.type === 'result') next();
});
child.on('exit', (code, signal) => { record('host', { kind: 'exit', code, signal }); out.end(); console.log(`exit ${code}, ${seq} entries -> ${outPath}`); });
