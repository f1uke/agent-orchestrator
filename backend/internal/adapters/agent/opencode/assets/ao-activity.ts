// agent-orchestrator: managed opencode activity plugin (do not edit)
//
// It maps opencode's native lifecycle events onto AO's normalized activity
// events:
//   session.created                        -> `ao hooks opencode session-start`
//   message.updated / message.part.updated -> `ao hooks opencode user-prompt-submit`
//   message.part.updated (tool part)       -> `ao hooks opencode tool-start`
//                                             `ao hooks opencode tool-end`
//                                             `ao hooks opencode tool-failed`
//   session.status (status.type == idle)   -> `ao hooks opencode stop`
//
// SAFETY. A tool part carries state.input, state.output and state.title — a
// command with an inline token, a file body, raw command output. AO has not
// verified the shape of any of it, and none of it may reach a desktop overlay,
// so ONLY the tool NAME is reported. AO's Go side then maps that name through
// its own per-tool whitelist (adapters/agent/toolcurate) and drops anything it
// does not recognise.
//
// The opencode-native session id (and prompt/model where known) is piped to the
// hook command as JSON on stdin, run with cwd set to the worktree so AO can
// correlate the opencode session to its AO session. Every invocation is
// best-effort and must never crash the user's opencode session: a missing `ao`
// binary is a guarded no-op (`command -v ao`), and spawn exceptions, non-zero
// exit codes, and malformed event payloads are caught and surfaced through
// opencode's structured logger (client.app.log) for diagnosis — never rethrown.
//
// `import type` is erased at runtime by Bun's transpiler, so this loads even
// before opencode has installed @opencode-ai/plugin into the config dir.
import type { Plugin } from "@opencode-ai/plugin"

export const aoActivity: Plugin = async ({ directory, client }) => {
  // ao hooks must never be able to hang opencode: cap each invocation, matching
  // the 30s timeout the claude-code and codex hook entries use.
  const HOOK_TIMEOUT_MS = 30_000
  // A user message is reported at most twice (see reportUserPrompt): an optional
  // early empty report, then an upgrade carrying the prompt text. Maps a message
  // id to whether the report we already sent included the prompt text.
  const promptReports = new Map<string, boolean>()
  // A tool part is re-emitted on every streaming update, so remember the last
  // hook reported per tool call and fire only on a status TRANSITION. Bounded so
  // a long session cannot grow it without limit.
  const toolReports = new Map<string, string>()
  const MAX_TOOL_REPORTS = 500
  // message.* events don't carry the session id, so track it from events that do.
  let currentSessionID: string | null = null
  // The model of the most recent assistant message, forwarded for context.
  let currentModel: string | null = null
  const messageStore = new Map<string, any>()

  // Wrap in `sh -c` with a guard so a missing `ao` binary is a silent no-op
  // (exit 0) rather than a per-event error in the user's session.
  function hookCmd(hookName: string): string[] {
    return ["sh", "-c", `if ! command -v ao >/dev/null 2>&1; then exit 0; fi; exec ao hooks opencode ${hookName}`]
  }

  // Report a hook failure through opencode's structured logger. Best-effort: the
  // log call must itself never throw or reject back into opencode, hence the
  // optional chaining + swallowed rejection.
  function logHookFailure(hookName: string, detail: string) {
    try {
      void client?.app
        ?.log?.({ body: { service: "ao-activity", level: "error", message: `hook ${hookName} failed: ${detail}` } })
        ?.catch?.(() => {})
    } catch {
      // The logger itself is unavailable — nothing more we can safely do.
    }
  }

  // All hooks are dispatched synchronously (Bun.spawnSync), for two reasons:
  //   1. Ordering. An async hook yields the event loop; if opencode does not
  //      await the handler's promise, a later event (e.g. message.updated ->
  //      user-prompt-submit) could complete before an in-flight async
  //      session-start, so AO would see the prompt before the session is
  //      registered. spawnSync blocks opencode's single-threaded loop until the
  //      hook returns, so events are reported strictly in dispatch order.
  //   2. `opencode run` exits on the idle event, so an async stop hook would be
  //      killed before completing.
  //
  // A non-zero exit (the guard makes a missing `ao` exit 0, so this is a real
  // `ao hooks` failure) or a spawn exception is logged with its stderr and never
  // rethrown, so reporting failures are diagnosable without crashing opencode.
  function callHookSync(hookName: string, payload: Record<string, unknown>) {
    try {
      const result = Bun.spawnSync(hookCmd(hookName), {
        cwd: directory,
        stdin: new TextEncoder().encode(JSON.stringify(payload) + "\n"),
        stdout: "ignore",
        stderr: "pipe",
        timeout: HOOK_TIMEOUT_MS,
      })
      if (!result.success) {
        const stderr = result.stderr ? new TextDecoder().decode(result.stderr).trim() : ""
        logHookFailure(hookName, `exited ${result.exitCode}${stderr ? `: ${stderr}` : ""}`)
      }
    } catch (err) {
      // The spawn itself failed (e.g. no `sh` on PATH). Never propagate.
      logHookFailure(hookName, err instanceof Error ? err.message : String(err))
    }
  }

  function switchedSession(sessionID: string): boolean {
    if (currentSessionID === sessionID) return false
    promptReports.clear()
    toolReports.clear()
    messageStore.clear()
    currentModel = null
    currentSessionID = sessionID
    return true
  }

  // Report a user prompt, preferring the one that carries the prompt text.
  // message.updated can arrive before message.part.updated with no text, so an
  // early empty report must NOT dedup away the later text report — otherwise the
  // prompt never reaches AO and title-from-prompt metadata breaks. Therefore: an
  // empty report fires at most once (so run-mode flows that omit the text part
  // still mark the session active), and a text report fires once and is terminal.
  function reportUserPrompt(sessionID: string, messageID: string, prompt: string) {
    const hasText = prompt.length > 0
    const reportedWithText = promptReports.get(messageID)
    if (reportedWithText) return // already reported with text — terminal
    if (reportedWithText === false && !hasText) return // already reported empty; no new info
    promptReports.set(messageID, hasText)
    callHookSync("user-prompt-submit", { session_id: sessionID, prompt, model: currentModel ?? "" })
  }

  // Report a tool call's lifecycle. ONLY the tool name crosses the boundary:
  // state.input / state.output / state.title are deliberately never read (see
  // the SAFETY note at the top of this file).
  function reportToolPart(part: any) {
    const sessionID = part.sessionID ?? currentSessionID
    if (!sessionID) return
    const callID = part.callID ?? part.id
    if (!callID) return
    const status = part.state?.status
    const hook =
      status === "running" ? "tool-start" : status === "completed" ? "tool-end" : status === "error" ? "tool-failed" : null
    // "pending" is queued, not running — it carries no truthful "doing it now".
    if (!hook) return
    if (toolReports.get(callID) === hook) return
    if (toolReports.size >= MAX_TOOL_REPORTS) toolReports.clear()
    toolReports.set(callID, hook)
    callHookSync(hook, { session_id: sessionID, tool: typeof part.tool === "string" ? part.tool : "" })
  }

  return {
    event: async ({ event }) => {
      try {
        switch (event.type) {
          case "session.created": {
            const session = (event as any).properties?.info
            if (!session?.id) break
            if (switchedSession(session.id)) {
              callHookSync("session-start", { session_id: session.id })
            }
            break
          }

          case "message.updated": {
            const msg = (event as any).properties?.info
            if (!msg) break
            if (msg.sessionID && switchedSession(msg.sessionID)) {
              callHookSync("session-start", { session_id: msg.sessionID })
            }
            if (msg.role === "assistant" && msg.modelID) currentModel = msg.modelID
            // Fallback: some `opencode run` flows never deliver message.part.updated
            // for the prompt, so start the turn from the user message itself.
            if (msg.role === "user") {
              messageStore.set(msg.id, msg)
              const sessionID = msg.sessionID ?? currentSessionID
              if (sessionID) reportUserPrompt(sessionID, msg.id, "")
            }
            break
          }

          case "message.part.updated": {
            const part = (event as any).properties?.part
            if (!part?.messageID) break
            // A tool part belongs to the assistant message, so it is handled
            // before (and independently of) the user-prompt lookup below.
            if (part.type === "tool") {
              reportToolPart(part)
              break
            }
            const msg = messageStore.get(part.messageID)
            if (msg?.role === "user" && part.type === "text") {
              const sessionID = msg.sessionID ?? currentSessionID
              const prompt = part.text ?? ""
              if (sessionID) reportUserPrompt(sessionID, msg.id, prompt)
              if (prompt.length > 0) messageStore.delete(part.messageID)
            }
            break
          }

          case "session.status": {
            // session.status fires in both TUI and `opencode run`; session.idle
            // is deprecated and not reliably emitted in run mode.
            // AO's "stop" hook means "the current turn is idle/finished", not
            // "the whole native session has terminated", so multi-turn TUI
            // sessions intentionally emit one stop per idle transition.
            const props = (event as any).properties
            if (props?.status?.type !== "idle") break
            const sessionID = props?.sessionID ?? currentSessionID
            if (!sessionID) break
            callHookSync("stop", { session_id: sessionID, model: currentModel ?? "" })
            break
          }
        }
      } catch (err) {
        // A malformed/unexpected event payload must never crash opencode; log
        // it (tagged with the event type) for diagnosis and move on.
        logHookFailure(`event:${(event as any)?.type ?? "unknown"}`, err instanceof Error ? err.message : String(err))
      }
    },
  }
}
