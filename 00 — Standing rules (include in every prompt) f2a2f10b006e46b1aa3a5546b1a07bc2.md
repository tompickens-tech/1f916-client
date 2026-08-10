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

- Bind **`127.0.0.1` only**. If configured otherwise, warn loudly at startup and say what the risk is.
- The browser never talks to `1f916.ai` or to `api.github.com`. The Go backend makes every outbound call. No `fetch` to a third party exists anywhere in this codebase.
- Outbound requests go to exactly two hosts: `1f916.ai` and `api.github.com`. No analytics, no fonts, no avatars, no error reporting, no update check.
- Set a timeout on every outbound call. Never use `http.DefaultClient`.

## Rendering

- **All board content is untrusted.** Every string from 1f916 goes through `html/template` escaping. **Never construct `template.HTML` from board data** under any circumstance, including for links.
- **The ban is on board data, not on markup we generate ourselves — and there is a third option that avoids the question entirely.** No Go function in this project returns a string containing markup. Markup lives in templates; Go supplies **typed values** that templates place into text nodes and attributes. A function returning `"<svg>…"` as a plain `string` is a bug even though it looks like the safe choice: `html/template` escapes it and the source text appears on the page. This is not hypothetical — it shipped, and it broke the comment view. The identicon spec in the v0.1 brief shows the pattern to follow instead.
- Never assemble markup in the browser from board data.
- Board content and client chrome never share a background, an elevation, or a type style. The user must always be able to tell which is which.
- Board text renders as plain escaped text. **No Markdown renderer, no HTML sanitiser** — both are attack surface for a feature nobody asked for.

## Honesty in the interface

- Never show a number the server did not give you. If a value is unknown, say it is unknown rather than showing a plausible zero.
- Never report success before the result has been read back from the network.
- Error messages state what actually happened — including when what happened is genuinely ambiguous. "This could be either X or Y" is a better message than a confident wrong guess.

## Code style

- Standard library idioms, `gofmt`, no clever abstractions, no premature interfaces.
- Handle every error. No `_ =` on anything that can fail.
- No `TODO` that hides a decision. If something is unresolved, stop and ask rather than guessing.
- Comments explain *why*, never *what*.

## When in doubt

Do the boring thing. Every interesting choice on this project has already been made and written down; if the brief does not mention something, it is almost certainly meant to be plain.