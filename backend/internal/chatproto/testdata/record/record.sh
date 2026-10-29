#!/usr/bin/env bash
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
out="$here/.."
work="$(mktemp -d)"
raw="$(mktemp -d)"
clean() { env -i HOME="$HOME" USER="$USER" PATH="$(dirname "$(command -v claude)"):$(dirname "$(command -v node)"):/usr/bin:/bin" LANG=en_US.UTF-8 TERM=dumb "$@"; }

clean node "$here/rec.mjs" "$raw/first.jsonl" "$work" - "always,ask" \
  "Create a file named hello.txt containing the single word hi, using the Write tool. Do nothing else." \
  "Use the AskUserQuestion tool once with two questions: first, which colour I prefer (Red, Green, Blue; single choice); second, which snacks I like (Chips, Fruit, Nuts; multiple choice). Then reply with one sentence naming my answers." \
  "!aosend:billing-portal-12:Please reply with the name of the file you created. No tools." \
  "!burst:Count from 1 to 3, one number per line, no tools.|Now say the word queued."

sid="$(node -e 'const fs=require("fs");for(const l of fs.readFileSync(process.argv[1],"utf8").split("\n")){if(!l)continue;const e=JSON.parse(l);if(e.payload.type==="system"&&e.payload.subtype==="init"){console.log(e.payload.session_id);break}}' "$raw/first.jsonl")"

clean node "$here/rec.mjs" "$raw/resumed.jsonl" "$work" "$sid" \
  "deny:Rejected: name the file goodbye.txt instead and present the revised plan.,approve,allow,deny:The user denied rm from the chat UI." \
  "Without tools: which file did you create and which colour did I pick?" \
  "!mode:plan" \
  "Plan how you would add a file bye.txt containing bye. Two bullet points, then present it with ExitPlanMode." \
  "Run the Bash command: rm hello.txt" \
  "!interrupt:2500:Write a 600-word essay about tide pools. Do not use tools." \
  "Reply with just the word ok."

node "$here/scrub.mjs" "$raw/first.jsonl" "$work" "$out/first_session.jsonl" --deltas "$out/stream_events.jsonl"
node "$here/scrub.mjs" "$raw/resumed.jsonl" "$work" "$out/resumed_session.jsonl"
rm -rf "$work" "$raw"
echo "Re-recorded. Run gitleaks over $out before committing."
