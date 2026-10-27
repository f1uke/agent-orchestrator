# Release web

The release web is a read-only copy of the Release Train board for your team. It is a separate program from the AO daemon. It runs on your Mac, listens on `127.0.0.1` only, and asks for one shared key.

AO never exposes it to the internet. Putting it behind a public address is your step. See [Exposing it](#exposing-it).

## What it does

- Shows the same board as the Releases page in the app.
- Lets people look and never change anything. Every write route answers 403.
- Keeps its own database and polls GitLab, Jira and Confluence itself. It works when the daemon is stopped.
- Reads your plans for long-lived branches, hides, rule toggles and No-MR marks from the app's database, read-only. It never writes them.
- Leaves out what people typed and who did it: plan notes, the reasons of accepted MRs, hide reasons, No-MR notes, and the "set by" and "marked by" names. Everyone with the key sees the plans, hides and marks themselves, not that text.

## Commands

Run these with the `ao` binary.

| Command                     | What it does                                                                                                                                                                                 |
| --------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `ao release-web start`      | Starts the web in the background. Creates a key first if there is none. Prints the status. `--host NAME` sets the public names it answers; see [Host names](#host-names-the-server-answers). |
| `ao release-web stop`       | Stops the web. Signals only a process it verifies as the web; see [Stopping safely](#stopping-safely).                                                                                       |
| `ao release-web status`     | Shows whether it runs, its address, signed-in sessions, public host names and key generation. `--json` for scripts.                                                                          |
| `ao release-web rotate-key` | Makes a new key. Everyone is signed out. Prints the generation and the key file path.                                                                                                        |
| `ao release-web copy-key`   | Puts the key on the clipboard without printing it. On macOS it uses `pbcopy`.                                                                                                                |
| `ao release-web serve`      | Runs the web in the foreground. Takes `--port` (default 7420), `--assets DIR` and `--host NAME`.                                                                                             |

No command prints the key. On a machine without `pbcopy`, read the key from `key.json`.

The app has the same controls. Open Settings, then Team web. The switch starts and stops the web. The panel shows the local address, the number of people signed in and the public host names. It has Copy key and Rotate key buttons. Rotate key asks for confirmation first.

The daemon exposes the controls as `GET /api/v1/release-web` and `POST /api/v1/release-web/{start,stop,rotate-key,copy-key}`. The daemon copies the key to the clipboard itself. The key never crosses HTTP.

The web outlives the daemon. Restarting the app does not stop it.

### Stopping safely

`run.json` records the web's pid and the start time the OS reports for that process. `stop` reads the start time of the pid again and compares the two, within 2 seconds.

- The pid is gone. `stop` deletes `run.json` and `status.json`.
- The pid is alive and the start times match. `stop` sends SIGTERM, then SIGKILL after 5 seconds.
- The pid is alive, but its start time does not match or cannot be read. Another process can have the pid now, or a clock step can change the start time that `ps` reports. `stop` signals nothing, keeps `run.json`, and fails with an error that names the pid. Check that process yourself. If it is not the web, delete `run.json`.

`status` shows such a pid as stopped, so `start` starts a new web. The new web fails to listen if the old one still holds the port.

## Files

All files live in `~/.ao/release-web`. The directory is mode 0700.

| File          | Content                                                                                                              |
| ------------- | -------------------------------------------------------------------------------------------------------------------- |
| `key.json`    | The shared key, its generation and its creation time. Mode 0600.                                                     |
| `access.log`  | One JSON line per sign-in, failed sign-in, lockout, sign-out and rotation. `access.log.1` holds the previous 10 MiB. |
| `run.json`    | The pid, port, start time and the process start time the OS reports, while the web runs.                             |
| `status.json` | The signed-in session count while the web runs.                                                                      |
| `hosts`       | The public host names the web answers, one per line. `start --host` writes it. Mode 0600.                            |
| `web.log`     | Output of a web started with `start`. At 10 MiB it moves to `web.log.1`, at start and while the web runs.            |
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
- No limit counts failures across clients. Such a lock would let anyone on the internet keep your team out without the key, and it adds no protection: nobody can guess a 256-bit key.
- Rotating the key signs out every session. A session belongs to the key that signed it in, not to the generation number, which starts over at 1 when `key.json` was deleted.
- The server trusts `X-Forwarded-For` and `X-Forwarded-Proto` only when the TCP peer is loopback. It uses the last `X-Forwarded-For` value, the one the local proxy wrote.
- Every response carries a content security policy, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, `X-Content-Type-Options: nosniff` and a `noindex` header.
- Sign-in and sign-out reject cross-site posts.
- The API is read-only. It serves the board and avatars. Any other method answers 403.
- The board leaves out the paths of your config and people files and their error messages. It keeps their state: absent, invalid or fine.
- `access.log` records the time, the client address and the event. It never records the key, a token, a form body or a query string.
- `access.log` stays small under a flood. At 10 MiB it moves to `access.log.1`, which replaces the old one. While a client is locked out, the log writes its first refused attempt once per minute. A `signin_locked_repeated` line with a `count` stands for the rest.
- `start` and the Settings switch give the web only the environment it needs: `HOME`, `PATH`, `USER`, `LOGNAME`, `TMPDIR`, `LANG`, `LC_*`, `TZ`, `SSL_CERT_*`, the proxy variables, `AO_DATA_DIR`, `AO_RELEASE_WEB_DIR`, `AO_RELEASE_WEB_ASSETS`, `AO_GITLAB_HOST`, the GitLab token variables (`AO_GITLAB_TOKEN`, `GITLAB_TOKEN`), `glab`'s config location (`GLAB_CONFIG_DIR`, `XDG_CONFIG_HOME`) and the Atlassian variables (`AO_JIRA_*`, `JIRA_SERVER`, `JIRA_LOGIN`, `JIRA_API_TOKEN`, `JIRA_CONFIG_FILE`). Other tokens in the daemon's environment do not reach it. The list is `releaseWebEnvNames` in `backend/internal/daemon/release_web_control.go`.
- The server refuses to listen on any address except `127.0.0.1`. It answers only known `Host` names. See [Host names the server answers](#host-names-the-server-answers).

## Cost of two pollers

The app's daemon polls GitLab, Jira and Confluence every 15 seconds. The release web does the same with its own store. While both run, the read load on those hosts doubles. The web never polls faster than the app.

The pollers share credentials. Both use the same GitLab token chain and Atlassian config. Both obey the same per-host budgets, but each counts its own requests.

Stop the web when nobody needs it.

## Exposing it

The release web listens on `127.0.0.1:7420`. Tailscale Funnel can put that port on a public HTTPS address. This is your step. AO never runs `tailscale` and never changes network settings.

To do it:

1. Find your machine's Funnel name, `<machine>.<tailnet>.ts.net`. `tailscale status --self` or the Tailscale admin console shows it.
2. Run `ao release-web start --host <machine>.<tailnet>.ts.net`. If the web already runs, run `ao release-web stop` first: a running web keeps the names it started with.
3. Run `ao release-web status` and check the `hosts:` line.
4. Run `ao release-web copy-key` and share the key with your team over a private channel.
5. Run `tailscale funnel 7420`. Check the Tailscale documentation for the exact syntax of your version.
6. Stop Funnel when you want the address closed. The Team web switch in Settings does not control Funnel.

### Use the HTTPS proxy mode only

Use `tailscale funnel <port>`, the HTTPS proxy mode. Do not use `--tcp` or `--tls-terminated-tcp`.

In HTTPS proxy mode, Funnel terminates TLS and proxies HTTP to the loopback port. It adds `X-Forwarded-For` with the real client address and `X-Forwarded-Proto: https`. The server depends on both:

- The sign-in limits count each client by its `X-Forwarded-For` address.
- The session cookie gets `Secure`, and responses get HSTS, only when `X-Forwarded-Proto` is `https`.

The TCP modes pass raw bytes and add no headers. Every visitor then looks like `127.0.0.1`. So one guesser's failures lock out everyone, the per-client limits stop working, and the cookie loses `Secure`.

### Host names the server answers

The server answers only these `Host` headers:

- `127.0.0.1:<port>`, `localhost:<port>` and `[::1]:<port>`, where the port is the one it listens on.
- Each name in the `hosts` file, `~/.ao/release-web/hosts`.
- Each name you give with `--host` on `ao release-web serve`, for example a custom domain in front of the web. Repeat the flag for more names.

Set the `hosts` file with `ao release-web start --host <machine>.<tailnet>.ts.net`, your own Funnel name. Repeat `--host` for more names. Each `start --host` replaces the whole list. `start` without `--host` keeps it. To answer no public name, delete the file. A web reads the file when it starts, so stop and start it after a change. `ao release-web status` and Settings show the names the running web answers, or the names in the file while it is stopped.

Any other `Host` gets `421 Misdirected Request` before the server looks at keys or sessions. Without a `hosts` file, Funnel visitors get 421 too. This stops DNS rebinding: a web page on another name that resolves to `127.0.0.1` cannot use your browser to reach the web. It also stops a Funnel name that is not yours, such as `attacker-machine.tail0000.ts.net`.
