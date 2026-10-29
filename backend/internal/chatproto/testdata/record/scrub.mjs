import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

const [, , inPath, workDir, outPath, flag, deltasPath] = process.argv;
if (!outPath) {
  console.error('usage: node scrub.mjs <in.jsonl> <workDir> <out.jsonl> [--deltas <out-deltas.jsonl>]');
  process.exit(2);
}
const home = os.homedir();
const user = path.basename(home);
const dashed = (p) => p.replace(/[^A-Za-z0-9-]/g, '-');

const scrubText = (s) => s
  .split(workDir).join('/work/project')
  .split(dashed(workDir)).join('-work-project')
  .split(home).join('/home/dev')
  .split(dashed(home)).join('-home-dev')
  .replace(new RegExp(`\\b${user}\\b`, 'g'), 'dev')
  .replace(/\/tmp\/cc-socks\/\d+\.sock/g, '/tmp/cc-socks/0.sock')
  .split(path.basename(workDir)).join('project')
  .replace(/ao-chat-[a-z]+-?/g, 'proj')
  .replace(/claude-\d+\//g, 'tmp/')
  .split(path.basename(workDir).split('-').pop()).join('x')
  .replace(/msg_[A-Za-z0-9]{12,}/g, (m) => msgIds.get(m) ?? msgIds.set(m, `msg_example_${msgIds.size + 1}`).get(m));
const msgIds = new Map();

const fakeInit = {
  slash_commands: ['compact', 'clear', 'review', 'tdd', 'open-pr'],
  terminal_slash_commands: ['model', 'resume'],
  skills: ['tdd', 'open-pr'],
  plugins: [{ name: 'example-plugin', path: '/home/dev/.claude/plugins/example-plugin' }],
  agents: ['general-purpose', 'Explore'],
  mcp_servers: [],
};

const lines = fs.readFileSync(inPath, 'utf8').split('\n').filter(Boolean);
const kept = [];
const deltas = [];
let seq = 0;
for (const line of lines) {
  const e = JSON.parse(scrubText(line));
  if (e.dir === 'out' && e.payload.type === 'stream_event') {
    if (deltas.length < 40) deltas.push(e.payload);
    continue;
  }
  if (e.dir === 'out' && e.payload.type === 'system' && e.payload.subtype === 'init') Object.assign(e.payload, fakeInit);
  const initResp = e.dir === 'out' && e.payload.type === 'control_response' ? e.payload.response?.response : undefined;
  if (initResp?.account) {
    initResp.account = { email: 'dev@example.com', organization: 'Example Org', subscriptionType: 'max', apiProvider: 'firstParty' };
    initResp.commands = [
      { name: 'compact', description: 'Clear history but keep a summary in context', argumentHint: '', builtin: true },
      { name: 'review', description: 'Review a pull request', argumentHint: '', builtin: true },
      { name: 'tdd', description: 'Write the failing test first', argumentHint: '', builtin: false },
    ];
    initResp.agents = [{ name: 'general-purpose', description: 'General-purpose agent' }, { name: 'Explore', description: 'Read-only search agent' }];
  }
  e.seq = ++seq;
  kept.push(JSON.stringify(e));
}
fs.writeFileSync(outPath, kept.join('\n') + '\n');
if (flag === '--deltas') fs.writeFileSync(deltasPath, deltas.map((d) => JSON.stringify(d)).join('\n') + '\n');
console.log(`${kept.length} entries -> ${outPath}${flag === '--deltas' ? `, ${deltas.length} deltas -> ${deltasPath}` : ''}`);
