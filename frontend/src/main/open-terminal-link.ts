// Gate for links emitted by terminal content (OSC 8 hyperlinks) before they are
// handed to the OS default handler. Terminal output is untrusted agent text, so
// only well-known safe schemes are opened; anything else is refused. http(s)
// normally route through the window-open handler in main.ts, but file:// — the
// scheme Claude Code / Superpowers use for .md file links — is denied there, so
// those arrive through the shell:openExternal IPC that calls this.
//
// `https:` is already in this allowlist, so the terminal's constructed PR/MR
// links (`#<num>` GitHub, `!<num>` GitLab — see terminal-scm-links.ts) open with
// no change here: they are https to github.com or the project's own GitLab host,
// built by the renderer from the session's trusted remote base (never from raw
// agent text), so the host is the session's own remote host by construction.
const ALLOWED_TERMINAL_LINK_SCHEMES = new Set(["http:", "https:", "file:"]);

export function isAllowedTerminalLink(url: unknown): url is string {
	if (typeof url !== "string" || url === "") return false;
	try {
		return ALLOWED_TERMINAL_LINK_SCHEMES.has(new URL(url).protocol);
	} catch {
		// Not a parseable absolute URL (bare text, relative path) — refuse.
		return false;
	}
}
