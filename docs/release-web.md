# Release web

The release web is a read-only copy of the Release Train board for your team. It is a separate program from the AO daemon. It runs on your Mac, listens on `127.0.0.1` only, and asks for one shared key.

AO never exposes it to the internet. Putting it behind a public address is your step. See [Exposing it](#exposing-it).

## What it does

- Shows the same board as the Releases page in the app.
- Lets people look and never change anything. Every write route answers 403.
- Keeps its own database and polls GitLab, Jira and Confluence itself. It works when the daemon is stopped.
- Reads your plans for long-lived branches, hides, rule toggles and No-MR marks from the app's database, read-only. It never writes them.

## Commands

Run these with the `ao` binary.

| Command                     | What it does                                                                                     |
| --------------------------- | ------------------------------------------------------------------------------------------------ |
| `ao release-web start`      | Starts the web in the background. Creates a key first if there is none. Prints the status.       |
| `ao release-web stop`       | Stops the web.                                                                                   |
| `ao release-web status`     | Shows whether it runs, its address, signed-in sessions and key generation. `--json` for scripts. |
| `ao release-web rotate-key` | Makes a new key. Everyone is signed out. Prints the generation and the key file path.            |
| `ao release-web copy-key`   | Puts the key on the clipboard without printing it. On macOS it uses `pbcopy`.                    |
| `ao release-web serve`      | Runs the web in the foreground. Takes `--port` (default 7420) and `--assets DIR`.                |

No command prints the key. On a machine without `pbcopy`, read the key from `key.json`.

The app has the same controls. Open Settings, then Team web. The switch starts and stops the web. The panel shows the local address and the number of people signed in. It has Copy key and Rotate key buttons. Rotate key asks for confirmation first.

The daemon exposes the controls as `GET /api/v1/release-web` and `POST /api/v1/release-web/{start,stop,rotate-key,copy-key}`. The daemon copies the key to the clipboard itself. The key never crosses HTTP.

The web outlives the daemon. Restarting the app does not stop it.

## Files

All files live in `~/.ao/release-web`. The directory is mode 0700.

| File          | Content                                                                                                              |
| ------------- | -------------------------------------------------------------------------------------------------------------------- |
| `key.json`    | The shared key, its generation and its creation time. Mode 0600.                                                     |
| `access.log`  | One JSON line per sign-in, failed sign-in, lockout, sign-out and rotation. `access.log.1` holds the previous 10 MiB. |
| `run.json`    | The pid, port and start time while the web runs.                                                                     |
| `status.json` | The signed-in session count while the web runs.                                                                      |
| `web.log`     | Output of a web started with `start`.                                                                                |
| `db/`         | The web's own SQLite database.                                                                                       |
| `avatars/`    | Jira avatars the web downloaded.                                                                                     |

Set `AO_RELEASE_WEB_DIR` to use another directory. Without it, the directory sits beside the AO data dir. A sandbox daemon with its own `AO_DATA_DIR` gets its own release web.

The web reads two files from the AO data dir (`~/.ao/data` by default):

- `release-train.json`. The same config the app uses. The web never writes it.
- `ao.db`. The app's database. The web opens it read-only. When it is missing or unreadable, the web shows the board without plans, hides, rule toggles and No-MR marks, and logs the cause once.

## The web bundle

`serve` needs the built web bundle. It looks in this order:

1. The `--assets DIR` flag.
2. The `AO_RELEASE_WEB_ASSETS` environment variable.
3. `release-web/` in the directory of the `ao` binary.

When none exists, `serve` stops and names the flag.

## People

The board's person picker maps Jira accounts to GitLab users with `~/.ao/data/release-train-people.json`:

```json
{
	"people": [{ "jiraAccountId": "…", "gitlabUsername": "…", "name": "…" }]
}
```

The release web reads this file from the AO data dir. It never writes it.

## Security model

The web is written as if the internet reaches it.

- The key is 256 random bits from the system random source. `rotate-key` makes a new one.
- The server compares keys in constant time, over SHA-256 digests.
- A correct key creates a session. The session token is random. The server keeps only its SHA-256 and only in memory. A restart signs everyone out.
- The session cookie is `httpOnly`, `SameSite=Strict` and, over HTTPS, `Secure`.
- A session ends after 12 hours without a request, or 7 days after sign-in.
- Sign-ins are limited per client. A client is an IP address, or the /64 for IPv6.
  - Ten failures from one client within 15 minutes lock that client out for 5 minutes. Each repeat doubles the lockout, up to 24 hours. An office behind one address can make nine typos and still sign in.
  - Twenty successful sign-ins from one client within 15 minutes refuse the next one until the oldest is 15 minutes old. When the session store is full, a sign-in drops the least recently used session of its own client first. So one person with the key cannot sign everyone else out by signing in over and over.
- Thirty failures from all clients within 15 minutes lock sign-in for 15 minutes. Existing sessions keep working. The lock does not apply to a known client: one that signed in successfully within the last 7 days. The server remembers up to 1000 known clients and forgets the least recent first. A guesser without the key never becomes known, so he cannot lock your team out.
- Trade-off: a teammate who never signed in from his current address cannot sign in while the global lock lasts. He waits up to 15 minutes, or signs in from an address he used before.
- Rotating the key signs out every session. A session belongs to the key that signed it in, not to the generation number, which starts over at 1 when `key.json` was deleted.
- The server trusts `X-Forwarded-For` and `X-Forwarded-Proto` only when the TCP peer is loopback. It uses the last `X-Forwarded-For` value, the one the local proxy wrote.
- Every response carries a content security policy, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, `X-Content-Type-Options: nosniff` and a `noindex` header.
- Sign-in and sign-out reject cross-site posts.
- The API is read-only. It serves the board and avatars. Any other method answers 403.
- The board leaves out the paths of your config and people files and their error messages. It keeps their state: absent, invalid or fine.
- `access.log` records the time, the client address and the event. It never records the key, a token, a form body or a query string.
- `access.log` stays small under a flood. At 10 MiB it moves to `access.log.1`, which replaces the old one. While a client is locked out, the log writes its first refused attempt once per minute. A `signin_locked_repeated` line with a `count` stands for the rest.
- The server refuses to listen on any address except `127.0.0.1`.

## Cost of two pollers

The app's daemon polls GitLab, Jira and Confluence every 15 seconds. The release web does the same with its own store. While both run, the read load on those hosts doubles. The web never polls faster than the app.

The pollers share credentials. Both use the same GitLab token chain and Atlassian config. Both obey the same per-host budgets, but each counts its own requests.

Stop the web when nobody needs it.

## Exposing it

The release web listens on `127.0.0.1:7420`. Tailscale Funnel can put that port on a public HTTPS address. This is your step. AO never runs `tailscale` and never changes network settings.

To do it:

1. Run `ao release-web start`.
2. Run `ao release-web copy-key` and share the key with your team over a private channel.
3. Point Funnel at port 7420 with the `tailscale funnel` command. Check the Tailscale documentation for the syntax of your version.
4. Stop Funnel when you want the address closed. The Team web switch in Settings does not control Funnel.

Funnel terminates TLS and proxies to the loopback port. The server then sees the real client address in `X-Forwarded-For`.
