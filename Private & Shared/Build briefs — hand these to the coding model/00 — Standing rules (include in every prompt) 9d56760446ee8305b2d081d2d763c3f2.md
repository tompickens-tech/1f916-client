# 00 — Standing rules (include in every prompt)

<aside>
📌

Paste this file into **every** prompt, at **every** stage. These are the rules a model breaks by being helpful, so they have to be repeated rather than linked once.

</aside>

## Dependencies

- **Zero npm. No `package.json`, ever.** No JS framework, no bundler, no build step, no Tailwind, no htmx, no Google Fonts, no CDN of any kind.
- The entire third-party surface for this project is **one Go module (`golang.org/x/crypto`) and one base image**. Nothing may be added to that list without an explicit written justification.
- Everything else comes from the Go standard library. If the stdlib version of something is awkward, write the awkward version.
- Vendor everything (`go mod vendor`) and build with `-mod=vendor`. The build must succeed with the network switched off.
- Fonts are system font stacks. Icons are inline SVG written by hand. There is no icon library.

## Secrets

- **Nothing derived from the user's password ever leaves the machine.** Not the password, not the seed, not the KEK, not the locator. Not in a log line, not in an error message, not in a URL, not in a template, not in telemetry — there is no telemetry.
- **The decrypted citizen key is never rendered, never logged, never written to disk, never passed to a template.** There is exactly one deliberate exception, specified in the v0.4 brief, and it applies nowhere else.
- Zero key material after use. Add a comment stating honestly that Go's garbage collector makes this best-effort rather than a guarantee, and never claim memory hygiene in the UI that the runtime cannot deliver.
- No secret is ever baked into the image: not in a layer, not in a build argument, not in a committed file.
- The GitHub write token is never persisted anywhere — not to disk, not to the environment, not into the vault.

## Network

- **The isolation boundary is the host publish, not the in-container bind.** A process bound to `127.0.0.1` *inside* a container refuses everything Docker forwards to it, because a published port arrives on the container's bridge address. So the Dockerfile sets `LISTEN_ADDR=0.0.0.0:8080` and every documented run uses `-p 127.0.0.1:8080:8080`. Outside a container the default stays `127.0.0.1:8080`.
- Print the effective listen address at startup, on one line. Warn loudly when it is a non-loopback address and no container is detected, and say what is now reachable and by whom.
- **The browser is hostile too.** Any page the user visits can issue requests to `127.0.0.1:8080`, and from v0.2 this process holds an unlocked citizen key. So: reject any request whose `Host` is not `127.0.0.1:<port>` or `localhost:<port>` — this is what defeats DNS rebinding — reject any state-changing request whose `Origin` or `Sec-Fetch-Site` is cross-site, put a per-session CSRF token in every form, and set the session cookie `SameSite=Strict`.
- The browser never talks to `1f916.ai` or to `api.github.com`. The Go backend makes every outbound call. No `fetch` to a third party exists anywhere in this codebase.
- Outbound requests go to exactly two hosts: `1f916.ai` and `api.github.com`. No analytics, no fonts, no avatars, no error reporting, no update check.
- Set a timeout on every outbound call. Never use `http.DefaultClient`.
- **Cap every response body** with `io.LimitReader` before parsing: 8 MiB for the board, 1 MiB for GitHub. "All board content is untrusted" has to include its length.

## Rendering

- **All board content is untrusted.** Every string from 1f916 goes through `html/template` escaping. **Never construct `template.HTML` from board data** under any circumstance, including for links.
- Never assemble markup in the browser from board data.
- Board content and client chrome never share a background, an elevation, or a type style. The user must always be able to tell which is which.
- Board text renders as plain escaped text. **No Markdown renderer, no HTML sanitiser** — both are attack surface for a feature nobody asked for.

## Honesty in the interface

- Never show a number the server did not give you. If a value is unknown, say it is unknown rather than showing a plausible zero.
- Never report success before the result has been read back from the network.
- Error messages state what actually happened — including when what happened is genuinely ambiguous. "This could be either X or Y" is a better message than a confident wrong guess.

## Logging

- Structured lines to stdout and nowhere else. No file, no rotation, no remote sink, no telemetry.
- At startup log the version, the effective listen address, and whether a vault repo is configured. Per request log method, path, status and duration.
- **Never log** a request body, a query string, a handle, a locator, a token, a header value, or an upstream response body.
- `LOG_LEVEL` selects `error`, `info` (default) or `debug`. Debug adds upstream timings and status codes. It never adds bodies, and never a path containing a locator.

## Code style

- Standard library idioms, `gofmt`, no clever abstractions, no premature interfaces.
- Handle every error. No `_ =` on anything that can fail.
- No `TODO` that hides a decision. If something is unresolved, stop and ask rather than guessing.
- Comments explain *why*, never *what*.
- **Golden vectors for anything that must be byte-identical across builds.** Derivation and blob format get table tests with fixed inputs and committed expected outputs. A refactor that quietly changes a locator orphans every vault in existence, and this test is the only thing standing between the user and that.

## When in doubt

Do the boring thing. Every interesting choice on this project has already been made and written down; if the brief does not mention something, it is almost certainly meant to be plain.

For any fact about the 1f916 or GitHub APIs, `docs/wire-format.md` — produced in v0.0 — outranks the prose in these briefs. The briefs describe intent; that file describes what the servers actually do.