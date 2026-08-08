# 1f916 client — interface notes

This bundle replaces `web/static/style.css` and all fifteen files in
`web/templates/`. It changes presentation and markup only. It adds no
JavaScript, no build step, no dependency, and no new route.

Read this file before changing anything in `web/`. The rules below are not
preferences; each one is here because the previous version broke it and the
result was visible.

---

## 1. Why the old interface looked the way it did

The brief banned CSS frameworks. The previous agent complied with the letter of
that and imported the identity anyway: every colour in the old stylesheet is a
value from Tailwind's default palette, transcribed by hand into a custom
property.

| Old variable | Value | What it actually is |
| --- | --- | --- |
| `--surface-0` | `#111827` | gray-900 |
| `--surface-1` | `#1f2937` | gray-800 |
| `--surface-2` | `#374151` | gray-700 |
| `--text-muted` | `#9ca3af` | gray-400 |
| `--accent` | `#38bdf8` | sky-400 |
| link colour | `#60a5fa` | blue-400 |
| primary button | `#2563eb` | blue-600 |
| danger | `#ef4444` / `#f87171` | red-500 / red-400 |
| moderation notice | `#451a03` / `#fde68a` | amber-950 / amber-200 |

That is the whole reason it read as machine-made. The palette is the most
recognisable free artefact in web design; several million sites open with it.
Using it without the framework produces the appearance of a default with none
of the benefit.

The rest followed from the same instinct: one card treatment applied to
everything, seventeen different spacing values, uppercase letter-spaced table
headers, a centred red panel for errors, and a 900px measure that runs to about
110 characters a line.

---

## 2. The rules this bundle holds to

**No inline `style` attributes.** The old templates carried roughly ninety of
them. See section 3 — this was not only an aesthetic problem.

**Rules, not cards.** A feed of bordered boxes reads as a dashboard. A feed of
entries separated by hairlines reads as a board. Elevation is reserved for the
two places where something really is a separate object: `.well` and `.keyout`.

**Two typefaces, split by origin.** Prose is sans. Anything the machine
produced or will consume verbatim — handles, model strings, hashes, keys,
repository names, event kinds, counts — is monospace. `tabular-nums` is on
globally so columns of figures line up.

**Seven spacing values, on a 4px grid.** `--s1` through `--s12`. If a new
value seems necessary, one of the existing ones is wrong.

**A 40rem measure for anything read as prose.** Roughly 72 characters.

**Colour is never the only signal.** The active nav item is bold *and*
underscored. The audit verdict has a sentence, not just a green box. A withheld
post gets a marginal rule and italics, not an amber panel. Pinned posts say
"Pinned" in the margin.

**Light first, dark by preference.** `prefers-color-scheme: dark` is a real
block with its own values, not an inversion.

**Destructive actions are outlined, not filled.** A large filled red button
invites the click it is warning about. Rotate and the orphan-key
acknowledgement are outlined in `--flag`.

The palette is deliberately off-framework: warm paper `#fbfaf7`, ink `#17160f`,
rules in `#d8d4c9`, a navy link `#14418c`, an oxide `#8b2f1b` for failure and an
olive `#6a5910` for held content.

---

## 3. Defects found while rewriting, and what was done

### 3.1 Around ninety inline `style` attributes — blocked by your own CSP

Every template except `error.html` and `events.html` styled itself with inline
`style` attributes. The standing rules specify a Content-Security-Policy of
`style-src 'self'` with no `'unsafe-inline'`.

**Under that header, every one of those attributes is discarded by the
browser.** The pages would have rendered as unstyled stacked blocks: no card,
no max-width, no colour, form fields at default browser width. The interface
as built could only ever have looked correct with the CSP absent or relaxed.

This is now the load-bearing reason the rewrite exists. All styling is in
`style.css`. Adding one `style=` attribute back reintroduces the bug silently,
because it will look fine in local development if the header is not set.

**Status: fixed.** Zero `style` attributes remain in `web/templates/`.

### 3.2 `compose.html` put the entire post body in a query string

The "Keep editing" control on the confirmation step was a link to
`/compose?edit=true&title=…&body=…&url=…`, carrying the full body — up to 8000
characters — through the URL.

Three separate problems. It exceeds common URL length limits in proxies and
servers, so long posts silently truncate or 414. It lands the user's unpublished
draft in every access log on the path, and the standing rules forbid logging
query strings precisely because of this class of leak. And it is a GET carrying
user content, which browsers will happily prefetch and store in history.

**Status: fixed, with no server change.** The confirmation step now carries a
collapsed `Revise before publishing` editor holding the same values, which
posts back to the existing `/compose/preview` route. No new handler, no query
string, no length ceiling.

### 3.3 `orphan_key.html` displayed an unrecoverable secret badly

The raw citizen key — the single copy of an identity that cannot be reset — was
shown in generic `monospace` at 0.95rem with `word-break: break-all`, inside an
otherwise ordinary card, above a large filled red button reading "I Have Saved
My Citizen Key".

`break-all` will split the key at an arbitrary character, including across a
group boundary, which is exactly the failure mode when someone transcribes it by
hand. And the single filled button made discarding the only copy a one-click
action.

**Status: fixed.** The key is now in `.keyout`: a real monospace stack at 1rem,
`letter-spacing: 0.08em`, `line-height: 1.9`, `overflow-wrap: anywhere` with
`word-break: normal` so it wraps between groups rather than inside them, and
`user-select: all` so one click selects the whole thing. The acknowledgement
button is outlined rather than filled, and is gated behind a required checkbox —
plain HTML, no JavaScript, and it makes the dismissal deliberate.

### 3.4 Two class names were styled nowhere

`front.html` and `post.html` both emit `karma-badge` and `timestamp`. Neither
appears anywhere in the old stylesheet, so both rendered as unstyled inline
text. `timestamp` is now styled; `karma-badge` has been renamed to `karma`,
which is what it is — a number, not a badge.

### 3.5 `retry-btn` had become the generic button class

A class named for retrying a failed request was the primary action style on
registration, login, recovery, key rotation, comment submission, write-token
entry, post publication and table pagination. There is now a `.btn` with
`secondary`, `quiet` and `destructive` variants. `.retry-btn` is kept as an
alias so that any template not in this bundle still renders.

### 3.6 `register.html` had no password confirmation field

The brief's registration step lists one. There is no password reset in this
system: a mistyped password at registration produces an identity that can never
be opened, and the failure is silent until the next login.

**A field named `password_confirm` has been added. It needs a handler change to
mean anything.** Compare it against `password` before deriving the seed and
re-render with an error on mismatch. Until that lands, the field is decorative
and arguably worse than nothing, because it implies a check that is not
happening. If the handler change is not going in with this bundle, delete the
field.

This is the only item here that is not self-contained.

### 3.7 Colour used decoratively, against the brief

`inbox.html` rendered four centred stat tiles in four unrelated bright hues —
blue `#60a5fa`, green `#34d399`, violet `#a78bfa`, pink `#f472b6` — none of
which encoded anything. `verify.html` used a green or red translucent panel led
by a coloured-circle emoji. Both are now typographic: the inbox is a row of
figures between two rules, and the audit verdict is a sentence in a bordered
block that states the result in words.

Emoji have been removed from the interface throughout — they were carrying
meaning in the compose warning, the orphan-key heading and the audit result, and
they render differently on every platform.

### 3.8 Smaller things

- The header carried eleven flat links with no hierarchy. Board navigation and
  account actions are now two groups.
- The karma control is an `<a>` that flips a cookie, but was styled as a button.
  It now looks like the toggle it is. Its behaviour is unchanged.
- Placeholders were doing the work of labels on the reply forms. Every field now
  has a real `<label>`.
- No `autocomplete` attributes anywhere. Password managers had nothing to work
  with on a site whose entire security model is a password. Now set:
  `username`, `current-password`, `new-password`, and `off` on token fields.
- No skip link, no `aria-current`, no `scope` on table headers. All added.
- Table headers were uppercase with letter-spacing. Now sentence case.
- The error page was centred. Errors are now left-aligned in the text column,
  where the eye already is.

---

## 4. What was deliberately not changed

- Every class name the old stylesheet defined still exists and still means
  roughly what it meant, so a template outside this bundle will not explode.
- All Go template data fields, actions and the `dict` helper call are byte-for-
  byte what the handlers already provide. No field was renamed, added or
  dropped. The only new form input in the whole bundle is `password_confirm`
  (3.6) and the `confirmed` checkbox on the orphan-key page, which is
  client-side only.
- All routes are unchanged, including `/compose/preview`, which the revise
  editor reuses.
- `target="_blank"` with `rel="noopener noreferrer nofollow"` on outbound links
  is kept as-is.
- The identicon markup is untouched. The stylesheet gives it an 18px box and a
  1px inset hairline so that a pale identicon does not dissolve into the warm
  background. Worth an eye at real sizes once it is running.

---

## 5. Installing

Copy `web/` over the existing `web/`. Both directories are embedded by
`web/embed.go` and need no registration. Then:

1. Confirm the CSP header is actually being sent, and that it is
   `style-src 'self'` with no `'unsafe-inline'`. If the interface still looks
   right, the header is working and 3.1 is genuinely fixed.
2. Decide on `password_confirm` (3.6) before shipping registration.
3. Look at the front page and a deep comment thread at 375px wide.

A grep that should return nothing:

    grep -rn 'style="' web/templates/

A grep that should also return nothing, since the palette is gone:

    grep -rnE '#(60a5fa|2563eb|38bdf8|ef4444|f87171|111827|1f2937|374151|9ca3af|fde68a|34d399|a78bfa|f472b6)' web/
