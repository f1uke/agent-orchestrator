// Who said what to whom, read off a `message` frame.
//
// The feed frame names only the RECIPIENT — it is the session the message was
// accepted for. The sender is stamped onto the body by `ao send` itself
// (`[from @<project>-<num>] …`, so the recipient's terminal can linkify its way
// back), and the daemon's one-line sanitiser keeps that prefix because it is at
// the front. So both ends of a conversation are already on the wire; nothing had
// to be added to it.
//
// A message typed into the app by a person carries no stamp. That is not a parse
// failure — there is genuinely no second Proc to run to — and it reads as an
// absent sender, not an unknown one.

const SENDER_STAMP = /^\[from\s+@?([A-Za-z0-9][A-Za-z0-9._-]*)\]\s*/;

export type ParsedMessage = {
	/** The sending session's id, when `ao send` stamped one on. */
	sender?: string;
	/** The message without the stamp — what the Proc actually says. */
	body: string;
};

export function parseMessageFrom(text: string): ParsedMessage {
	const match = SENDER_STAMP.exec(text);
	if (!match) return { sender: undefined, body: text.trim() };
	return { sender: match[1], body: text.slice(match[0].length).trim() };
}
